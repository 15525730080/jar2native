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
