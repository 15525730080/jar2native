// Package jdk handles JDK discovery, version detection, validation, and
// tool resolution. A JDK is required at build time for jlink (JDK 9+) or
// JRE copying (Java 8).
package jdk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Mode controls how the runtime is produced.
type Mode string

const (
	ModeAuto  Mode = "auto"  // jlink if JDK 9+, copy if Java 8
	ModeJLink Mode = "jlink" // always jlink
	ModeCopy  Mode = "copy"  // always copy legacy JRE
)

// JDK represents a resolved Java Development Kit.
type JDK struct {
	Dir     string // JAVA_HOME
	Version int    // Major version (8, 11, 17, 21, 25, ...)
	Mode    Mode
}

// Resolve finds a JDK from the given path or by searching standard locations.
// If jdkPath is empty, it searches JAVA_HOME and common directories.
func Resolve(jdkPath string, mode Mode) (*JDK, error) {
	home := jdkPath
	if home == "" {
		home = os.Getenv("JAVA_HOME")
	}
	if home == "" {
		h, err := searchHome()
		if err != nil {
			return nil, fmt.Errorf("no JDK found: set --jdk or JAVA_HOME\n%w", err)
		}
		home = h
	}

	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("resolve JDK path %q: %w", home, err)
	}

	if err := Validate(home); err != nil {
		return nil, err
	}

	version, err := DetectVersion(home)
	if err != nil {
		return nil, fmt.Errorf("detect Java version: %w", err)
	}

	if mode == "" {
		mode = ModeAuto
	}

	j := &JDK{Dir: home, Version: version, Mode: mode}

	// Resolve actual mode for auto.
	if mode == ModeAuto {
		if version >= 9 {
			j.Mode = ModeJLink
		} else {
			j.Mode = ModeCopy
		}
	}

	// jlink requires JDK 9+.
	if j.Mode == ModeJLink && version < 9 {
		return nil, fmt.Errorf("jlink requires JDK 9+, detected Java %d at %s", version, home)
	}

	return j, nil
}

// IsLegacy returns true for Java 8 and below (no jlink, copy JRE instead).
func (j *JDK) IsLegacy() bool { return j.Version < 9 }

// Bin returns the path to a tool in the JDK's bin directory.
func (j *JDK) Bin(tool string) string {
	name := tool
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(j.Dir, "bin", name)
}

// JavaVersionString returns the human-readable version (e.g. "17.0.9").
func (j *JDK) JavaVersionString() string {
	return javaVersionString(j.Dir)
}

// JREDir returns the JRE directory path for legacy JDKs.
func (j *JDK) JREDir() string {
	return filepath.Join(j.Dir, "jre")
}

// ResolveHome resolves JAVA_HOME from path, env, or search.
func ResolveHome(path string) (string, error) {
	if path != "" {
		return filepath.EvalSymlinks(path)
	}
	if h := os.Getenv("JAVA_HOME"); h != "" {
		return filepath.EvalSymlinks(h)
	}
	return searchHome()
}

// searchHome searches standard platform-specific locations for a JDK.
func searchHome() (string, error) {
	home, _ := os.UserHomeDir()

	var candidates []string

	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Library/Java/JavaVirtualMachines",
			filepath.Join(home, "Library", "Java", "JavaVirtualMachines"),
			filepath.Join(home, ".jdks"),
			"/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home",
			"/opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home",
			"/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home",
		}
	case "linux":
		candidates = []string{
			"/usr/lib/jvm",
			"/usr/java",
			filepath.Join(home, ".jdks"),
			"/opt/jdk",
		}
	case "windows":
		candidates = []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Java"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Java"),
			filepath.Join(home, ".jdks"),
		}
	default:
		candidates = []string{filepath.Join(home, ".jdks")}
	}

	// Also try PATH lookup.
	if p, err := exec.LookPath("java"); err == nil {
		if h := homeFromBin(p); h != "" {
			candidates = append([]string{h}, candidates...)
		}
	}

	for _, c := range candidates {
		if h, err := findNewestUnder(c); err == nil {
			return h, nil
		}
	}
	return "", fmt.Errorf("no JDK found in any of: %v", candidates)
}

// findNewestUnder finds the newest JDK home under a directory.
// On macOS, JDKs live in Contents/Home subdirs.
func findNewestUnder(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	var homes []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		// macOS: JDK dirs have Contents/Home
		macHome := filepath.Join(p, "Contents", "Home")
		if _, err := os.Stat(filepath.Join(macHome, "bin")); err == nil {
			p = macHome
		}
		if _, err := os.Stat(filepath.Join(p, "bin")); err == nil {
			homes = append(homes, p)
		}
	}

	if len(homes) == 0 {
		return "", fmt.Errorf("no JDK under %s", dir)
	}

	// Sort by version, newest first.
	sort.Slice(homes, func(i, j int) bool {
		vi, _ := DetectVersion(homes[i])
		vj, _ := DetectVersion(homes[j])
		return vi > vj
	})

	return homes[0], nil
}

// homeFromBin derives JAVA_HOME from a java binary path.
func homeFromBin(binPath string) string {
	dir := filepath.Dir(binPath) // bin
	parent := filepath.Dir(dir)  // JDK home
	if _, err := os.Stat(filepath.Join(parent, "bin")); err == nil {
		return parent
	}
	return ""
}

// Validate checks that a directory looks like a JDK home (has bin/).
func Validate(home string) error {
	binDir := filepath.Join(home, "bin")
	info, err := os.Stat(binDir)
	if err != nil {
		return fmt.Errorf("not a valid JDK home: %s (no bin/ directory)\nSet --jdk or JAVA_HOME to a JDK installation", home)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a valid JDK home: %s/bin is not a directory", home)
	}
	return nil
}

// DetectVersion runs `java -version` and parses the major version number.
func DetectVersion(home string) (int, error) {
	javaBin := filepath.Join(home, "bin", "java")
	if runtime.GOOS == "windows" {
		javaBin += ".exe"
	}
	if _, err := os.Stat(javaBin); err != nil {
		return 0, fmt.Errorf("java binary not found: %s", javaBin)
	}

	out, err := exec.Command(javaBin, "-version").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("java -version failed: %w", err)
	}

	return ParseJavaVersion(string(out))
}

// ParseJavaVersion extracts the major version from java -version output.
// Handles: 'version "17.0.9"', 'version "1.8.0_382"', 'version "25"'.
func ParseJavaVersion(output string) (int, error) {
	// Find version "X.Y.Z" pattern.
	idx := strings.Index(output, "version \"")
	if idx < 0 {
		return 0, fmt.Errorf("no version string in output: %s", output)
	}
	rest := output[idx+len("version \""):]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return 0, fmt.Errorf("unterminated version string")
	}
	ver := rest[:end]

	parts := strings.Split(ver, ".")
	if len(parts) == 0 {
		return 0, fmt.Errorf("empty version: %s", ver)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("parse major version %q: %w", parts[0], err)
	}

	// Java 8 and earlier use 1.X format.
	if major == 1 && len(parts) > 1 {
		minor, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, fmt.Errorf("parse minor version %q: %w", parts[1], err)
		}
		return minor, nil
	}

	return major, nil
}

// javaVersionString runs java -version and returns the full version line.
func javaVersionString(home string) string {
	javaBin := filepath.Join(home, "bin", "java")
	if runtime.GOOS == "windows" {
		javaBin += ".exe"
	}
	out, err := exec.Command(javaBin, "-version").CombinedOutput()
	if err != nil {
		return "unknown"
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return "unknown"
}

// CompressArg returns the jlink --compress argument value for the JDK version.
// JDK 20+: supports compress=2 (DEFLATE).
// JDK 9-19: supports compress=2 but some versions have bugs; use 1 (CONSTANT).
// Returns 0 if compression is not supported.
func CompressArg(version int) int {
	if version >= 20 {
		return 2 // DEFLATE
	}
	if version >= 9 {
		return 1 // CONSTANT_POOL_STRING
	}
	return 0 // not supported (legacy JDK)
}
