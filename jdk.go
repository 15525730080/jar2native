package main

import (
		"fmt"
		"os"
		"os/exec"
		"path/filepath"
		"sort"
		"strconv"
		"strings"
	)

// JDKManager handles local JDK detection and strict validation.
//
// jar2native never downloads or installs a JDK: it either uses the JDK given
// via --jdk-path or one detected on the local machine.
type JDKManager struct {
		JDKDir  string
		Version int // major version: 8, 11, 17, 21, 25 …
}

// NewJDKManager creates a JDKManager. If customPath is empty, a JDK is
// auto-detected on this machine (JAVA_HOME, PATH, well-known locations).
// The JDK is strictly validated for the requested JRE mode.
func NewJDKManager(customPath string, mode JREMode) (*JDKManager, error) {
		var jdkDir string
		var err error
		if customPath != "" {
					jdkDir = customPath
					if info, statErr := os.Stat(jdkDir); statErr != nil || !info.IsDir() {
									return nil, fmt.Errorf("--jdk-path %s is not a directory", customPath)
								}
				} else {
					jdkDir, err = detectJDKDir()
					if err != nil {
									return nil, fmt.Errorf(`no local JDK found: %w
									
									jar2native does not download or install JDKs. Install a JDK yourself
									(e.g. from https://adoptium.net/) and make sure it is on PATH/JAVA_HOME,
									or pass it explicitly with --jdk-path /path/to/jdk.`, err)
								}
				}
	
		ver, err := detectVersion(filepath.Join(jdkDir, "bin", "java"))
		if err != nil {
					return nil, fmt.Errorf("JDK validation failed: %s does not contain a usable bin/java (%w)", jdkDir, err)
				}
	
		m := &JDKManager{JDKDir: jdkDir, Version: ver}
		if err := m.Validate(mode); err != nil {
					return nil, err
				}
	
		logInfo("JDK: %s (Java %d, mode: %s)", jdkDir, ver, mode)
		return m, nil
}

// IsLegacy returns true for Java 8 and below (no jlink/jdeps).
func (j *JDKManager) IsLegacy() bool {
		return j.Version <= 8
}

// Bin returns the absolute path to a JDK tool (e.g. "jlink", "jdeps").
func (j *JDKManager) Bin(tool string) string {
		return filepath.Join(j.JDKDir, "bin", tool)
}

// Validate performs strict JDK validation for the requested mode.
//
//	full mode: java + jlink + jmods
//	deps mode: java + jdeps + jlink + jmods
//
// Java 8 has no jlink/jdeps at all; in full mode only java is required
// (the JRE is copied), while --deps is rejected before validation.
func (j *JDKManager) Validate(mode JREMode) error {
		var missing []string
	
		requireBin := func(tool string) {
					if _, err := os.Stat(j.Bin(tool)); err != nil {
									missing = append(missing, j.Bin(tool))
								}
				}
	
		requireBin("java")
	
		if j.IsLegacy() {
					// Java 8: no jlink/jmods exist; the full JRE is copied from the JDK.
					if mode == JREDeps {
									return fmt.Errorf(`Dependency-based JRE optimization is not supported for Java %d.
									
									Java 8 has no jlink/jdeps toolchain. Please build without --deps to
									package the full JRE.`, j.Version)
								}
					if len(missing) > 0 {
									return j.jdkValidationErr(missing)
								}
					return nil
				}
	
		if mode == JREDeps {
					requireBin("jdeps")
				}
		requireBin("jlink")
		if _, err := os.Stat(filepath.Join(j.JDKDir, "jmods")); err != nil {
					missing = append(missing, filepath.Join(j.JDKDir, "jmods"))
				}
	
		if len(missing) > 0 {
					return j.jdkValidationErr(missing)
				}
		return nil
}

func (j *JDKManager) jdkValidationErr(missing []string) error {
		return fmt.Errorf(`JDK validation failed.
		
		JDK home: %s
		
		Missing components:
		%s
		
		This looks like a JRE or an incomplete JDK rather than a full JDK. jar2native
		requires a full JDK containing bin/java, bin/jlink%s and the jmods/ directory.
		Install a full JDK locally or point --jdk-path at one.`,
						  		j.JDKDir, missingList(missing), depsSuffix(missing))
}

func depsSuffix(missing []string) string {
		for _, m := range missing {
					if strings.HasSuffix(filepath.ToSlash(m), "/jdeps") || strings.HasSuffix(m, "jdeps.exe") {
									return ", bin/jdeps"
								}
				}
		return ""
}

// missingList renders the missing components for an error message.
func missingList(missing []string) string {
		lines := make([]string, 0, len(missing))
		for _, m := range missing {
					lines = append(lines, "  - "+m)
				}
		return strings.Join(lines, "\n")
}

// JREDir returns the directory that should be bundled as the runtime for
// Java 8 (symlink-resolved). For Java 9+ the runtime is built with jlink.
func (j *JDKManager) JREDir() string {
		if !j.IsLegacy() {
					return ""
				}
		jreSubdir := filepath.Join(j.JDKDir, "jre")
		if info, err := os.Stat(jreSubdir); err == nil && info.IsDir() {
					if real, err := filepath.EvalSymlinks(jreSubdir); err == nil {
									return real
								}
					return jreSubdir
				}
		// JRE-only install or non-standard layout — use the root.
		if real, err := filepath.EvalSymlinks(j.JDKDir); err == nil {
					return real
				}
		return j.JDKDir
}

// ListAllModules returns a comma-separated list of all modules of this JDK
// (Java 9+ only).
func (j *JDKManager) ListAllModules() (string, error) {
		out, err := exec.Command(j.Bin("java"), "--list-modules").Output()
		if err != nil {
					return "", fmt.Errorf("failed to list JDK modules: %w", err)
				}
		seen := map[string]bool{}
		var modules []string
		for _, line := range strings.Split(string(out), "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
									continue
								}
					if idx := strings.Index(line, "@"); idx != -1 {
									line = line[:idx]
								}
					if line != "" && !seen[line] {
									seen[line] = true
									modules = append(modules, line)
								}
				}
		if len(modules) == 0 {
					return "", fmt.Errorf("`%s --list-modules` returned no modules", j.Bin("java"))
				}
		sort.Strings(modules)
		return strings.Join(modules, ","), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// detectJDKDir finds a local JDK home: JAVA_HOME first, then the java binary
// on PATH, then well-known install locations.
func detectJDKDir() (string, error) {
		// 1. JAVA_HOME
		if jh := os.Getenv("JAVA_HOME"); jh != "" {
					if isUsableJDK(jh) {
									return jh, nil
								}
					// macOS layout: JAVA_HOME points at <jdk>/ (with Contents/Home inside)
					inner := filepath.Join(jh, "Contents", "Home")
					if isUsableJDK(inner) {
									return inner, nil
								}
				}
	
		// 2. java on PATH (may be a JRE — callers validate strictly afterwards)
		if javaPath, err := exec.LookPath("java"); err == nil {
					resolved, err := filepath.EvalSymlinks(javaPath)
					if err != nil {
									resolved = javaPath
								}
					return filepath.Dir(filepath.Dir(resolved)), nil
				}
	
		// 3. Well-known locations
		candidates := []string{
					"/Library/Java/JavaVirtualMachines",
					filepath.Join(os.Getenv("HOME"), "Library/Java/JavaVirtualMachines"),
					filepath.Join(os.Getenv("HOME"), ".jdks"),
					"/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home",
					"/opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home",
					"/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home",
					"/usr/lib/jvm",
				}
		for _, c := range candidates {
					if jh, err := findJDKUnder(c); err == nil {
									return jh, nil
								}
				}
	
		return "", fmt.Errorf("no java binary found in JAVA_HOME, PATH or common install locations")
}

// isUsableJDK reports whether dir looks like a JDK/JRE home (has bin/java).
func isUsableJDK(dir string) bool {
		javaBin := filepath.Join(dir, "bin", "java")
		if _, err := os.Stat(javaBin); err == nil {
					return true
				}
		// macOS layout: <home>/Contents/Home/bin/java
		if _, err := os.Stat(filepath.Join(dir, "Contents", "Home", "bin", "java")); err == nil {
					return true
				}
		return false
}

// findJDKUnder scans a directory of JDK installations (e.g.
// /Library/Java/JavaVirtualMachines) and returns the newest usable home.
func findJDKUnder(root string) (string, error) {
		entries, err := os.ReadDir(root)
		if err != nil {
					return "", err
				}
		type candidate struct {
					home    string
					version int
				}
		var found []candidate
		for _, e := range entries {
					if !e.IsDir() {
									continue
								}
					// Standard layout: <root>/<name>/Contents/Home (macOS) or <root>/<name>
					homes := []string{
									filepath.Join(root, e.Name(), "Contents", "Home"),
									filepath.Join(root, e.Name()),
								}
					for _, h := range homes {
									if !isUsableJDK(h) {
														continue
													}
									ver, err := detectVersion(filepath.Join(h, "bin", "java"))
									if err != nil {
														continue
													}
									found = append(found, candidate{home: h, version: ver})
									break
								}
				}
		if len(found) == 0 {
					return "", fmt.Errorf("no JDK under %s", root)
				}
		// Prefer the highest version.
		best := found[0]
		for _, c := range found[1:] {
					if c.version > best.version {
									best = c
								}
				}
		return best.home, nil
}

// detectVersion runs `java -version` and parses the major version number.
func detectVersion(javaBin string) (int, error) {
		if _, err := os.Stat(javaBin); err != nil {
					return 0, fmt.Errorf("java binary not found: %s", javaBin)
				}
		out, err := exec.Command(javaBin, "-version").CombinedOutput()
		if err != nil {
					return 0, fmt.Errorf("failed to run %s -version: %w", javaBin, err)
				}
		return parseJavaVersion(string(out))
}

// parseJavaVersion extracts the major version from java -version output.
//
//	Java 8:  java version "1.8.0_392"
//	Java 9+: java version "17.0.9" / openjdk version "21.0.1"
func parseJavaVersion(output string) (int, error) {
		start := strings.Index(output, `"`)
		end := strings.LastIndex(output, `"`)
		if start == -1 || end <= start {
					return 0, fmt.Errorf("cannot parse java version from: %s", strings.TrimSpace(output))
				}
		ver := output[start+1 : end]
	
		// Legacy format: "1.major.minor_update"
		if strings.HasPrefix(ver, "1.") {
					parts := strings.SplitN(ver, ".", 3)
					if len(parts) >= 2 {
									major, err := strconv.Atoi(parts[1])
									if err == nil {
														return major, nil
													}
								}
				}
	
		// Modern format: "major.minor.patch"
		parts := strings.SplitN(ver, ".", 2)
		major, err := strconv.Atoi(parts[0])
		if err != nil {
					return 0, fmt.Errorf("cannot parse major version from %q", ver)
				}
		return major, nil
}
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// JDKManager handles JDK detection and tool path resolution.
type JDKManager struct {
	JDKDir  string
	Version int // major version: 8, 11, 17, 21, 25 …
}

// NewJDKManager creates a JDKManager. If customPath is empty, auto-detects the JDK.
func NewJDKManager(customPath string) (*JDKManager, error) {
	var jdkDir string
	if customPath != "" {
		jdkDir = customPath
	} else {
		var err error
		jdkDir, err = detectJDKDir()
		if err != nil {
			return nil, err
		}
	}

	ver, err := detectVersion(filepath.Join(jdkDir, "bin", "java"))
	if err != nil {
		return nil, err
	}

	logInfo("JDK detected: %s (version %d)", jdkDir, ver)
	return &JDKManager{JDKDir: jdkDir, Version: ver}, nil
}

// IsLegacy returns true for Java 8 and below (no jlink/jdeps).
func (j *JDKManager) IsLegacy() bool {
	return j.Version <= 8
}

// Bin returns the absolute path to a JDK tool (e.g. "jlink", "jdeps", "jar").
func (j *JDKManager) Bin(tool string) string {
	return filepath.Join(j.JDKDir, "bin", tool)
}

// JREDir returns the real (symlink-resolved) directory that should be bundled
// as the runtime.
//
// Java 9+: jlink will create this directory.
// Java 8-: the JDK ships with a self-contained jre/ subdirectory; if that
// doesn't exist (JRE-only install or non-standard layout) we fall back to
// the JDK root itself.
func (j *JDKManager) JREDir() string {
	if !j.IsLegacy() {
		// caller will build this with jlink
		return ""
	}
	// Standard JDK 8 layout: $JAVA_HOME/jre/
	// Note: on macOS Zulu/Temurin, jre/ is often a symlink — resolve it so
	// WalkDir sees a real directory and not a symlink entry.
	jreSubdir := filepath.Join(j.JDKDir, "jre")
	if info, err := os.Stat(jreSubdir); err == nil && info.IsDir() {
		if real, err := filepath.EvalSymlinks(jreSubdir); err == nil {
			return real
		}
		return jreSubdir
	}
	// JRE-only install or non-standard layout — use the root
	if real, err := filepath.EvalSymlinks(j.JDKDir); err == nil {
		return real
	}
	return j.JDKDir
}

// ListAllModules returns a comma-separated list of all modules (Java 9+ only).
func (j *JDKManager) ListAllModules() (string, error) {
	out, err := exec.Command(j.Bin("java"), "--list-modules").Output()
	if err != nil {
		return "", fmt.Errorf("failed to list JDK modules: %w", err)
	}
	var modules []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "@"); idx != -1 {
			line = line[:idx]
		}
		modules = append(modules, line)
	}
	return strings.Join(modules, ","), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// detectJDKDir finds the JDK home directory via the java binary in PATH.
func detectJDKDir() (string, error) {
	javaPath, err := exec.LookPath("java")
	if err != nil {
		return "", fmt.Errorf("java binary not found in PATH; install a JDK or use --jdk-path")
	}
	resolved, err := filepath.EvalSymlinks(javaPath)
	if err != nil {
		resolved = javaPath
	}
	// java → <jdk>/bin/java  →  parent of bin  →  jdk root
	return filepath.Dir(filepath.Dir(resolved)), nil
}

// detectVersion runs `java -version` and parses the major version number.
//
// Version string formats:
//
//	Java 8:  java version "1.8.0_392"
//	Java 9+: java version "17.0.9" / openjdk version "21.0.1"
func detectVersion(javaBin string) (int, error) {
	out, err := exec.Command(javaBin, "-version").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to run java -version: %w", err)
	}
	return parseJavaVersion(string(out))
}

// parseJavaVersion extracts the major version from java -version output.
func parseJavaVersion(output string) (int, error) {
	// Find the quoted version string, e.g. "1.8.0_392" or "17.0.9"
	start := strings.Index(output, `"`)
	end := strings.LastIndex(output, `"`)
	if start == -1 || end <= start {
		return 0, fmt.Errorf("cannot parse java version from: %s", output)
	}
	ver := output[start+1 : end] // e.g. "1.8.0_392" or "17.0.9"

	// Legacy format: "1.major.minor_update"
	if strings.HasPrefix(ver, "1.") {
		parts := strings.SplitN(ver, ".", 3)
		if len(parts) >= 2 {
			major, err := strconv.Atoi(parts[1])
			if err == nil {
				return major, nil
			}
		}
	}

	// Modern format: "major.minor.patch"
	parts := strings.SplitN(ver, ".", 2)
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("cannot parse major version from %q", ver)
	}
	return major, nil
}
