package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ModuleAnalyzer analyzes Java module dependencies for JAR/WAR files.
//
// It is only used in --deps mode. Analysis failures are fatal: jar2native
// never silently falls back to a guessed module set, because that produced
// binaries that built fine but crashed at runtime.
type ModuleAnalyzer struct {
	jdk *JDKManager
}

// NewModuleAnalyzer creates a new ModuleAnalyzer.
func NewModuleAnalyzer(jdk *JDKManager) *ModuleAnalyzer {
	return &ModuleAnalyzer{jdk: jdk}
}

// Analyze returns the comma-separated module list required by the application.
func (a *ModuleAnalyzer) Analyze(appPath string, isWAR bool) (string, error) {
	if isWAR {
		return a.analyzeWAR(appPath)
	}
	return a.runJdeps([]string{appPath})
}

// analyzeWAR extracts an executable WAR to a temp dir and analyzes
// WEB-INF/classes plus WEB-INF/lib/*.jar.
func (a *ModuleAnalyzer) analyzeWAR(warPath string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "jar2native-war-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	logInfo("Extracting WAR file to temporary directory for analysis")
	if err := extractZip(warPath, tmpDir); err != nil {
		return "", fmt.Errorf("failed to extract WAR: %w", err)
	}

	var targets []string
	classesDir := filepath.Join(tmpDir, "WEB-INF", "classes")
	if info, err := os.Stat(classesDir); err == nil && info.IsDir() {
		targets = append(targets, classesDir)
	}

	libDir := filepath.Join(tmpDir, "WEB-INF", "lib")
	if entries, err := os.ReadDir(libDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.ToLower(filepath.Ext(e.Name())) == ".jar" {
				targets = append(targets, filepath.Join(libDir, e.Name()))
			}
		}
	}

	if len(targets) == 0 {
		return "", fmt.Errorf("no analysis targets found in WAR (expected WEB-INF/classes or WEB-INF/lib/*.jar)")
	}
	return a.runJdeps(targets)
}

// runJdeps runs `jdeps --print-module-deps` and returns the module list.
// Any failure is a hard error — no fallback to a default module set.
func (a *ModuleAnalyzer) runJdeps(targets []string) (string, error) {
	args := append([]string{"--print-module-deps"}, targets...)
	cmd := exec.Command(a.jdk.Bin("jdeps"), args...)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return "", jdepsFailureError(targets, stderr)
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		return "", jdepsFailureError(targets, "jdeps printed an empty module list")
	}
	return result, nil
}

// jdepsFailureError renders the mandated error for failed dependency analysis.
func jdepsFailureError(targets []string, detail string) error {
	msg := fmt.Sprintf(`Failed to analyze Java module dependencies.

Analyzed: %s`, strings.Join(targets, ", "))
	if detail != "" {
		msg += "\n\njdeps output:\n  " + strings.ReplaceAll(detail, "\n", "\n  ")
	}
	msg += `

Possible causes:
- dynamic class loading
- reflection
- ServiceLoader
- unsupported fat JAR layout
- missing dependencies

You can:
1. fix the dependency problem
2. run without --deps to use the full JRE`
	return fmt.Errorf("%s", msg)
}

// MergeModules merges analyzed modules with extra user-specified modules.
func MergeModules(analyzed string, extra []string) string {
	modules := map[string]struct{}{}
	for _, m := range strings.Split(analyzed, ",") {
		m = strings.TrimSpace(m)
		if m != "" {
			modules[m] = struct{}{}
		}
	}
	for _, m := range extra {
		m = strings.TrimSpace(m)
		if m != "" {
			modules[m] = struct{}{}
		}
	}
	result := make([]string, 0, len(modules))
	for m := range modules {
		result = append(result, m)
	}
	sort.Strings(result)
	return strings.Join(result, ",")
}

// extractZip extracts a zip/jar/war archive to destDir with zip-slip
// protection: every entry target is cleaned and verified to stay inside
// destDir; path-escaping entries abort the extraction.
func extractZip(src, destDir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	cleanDest := filepath.Clean(destDir)

	for _, f := range r.File {
		target, err := safeJoin(cleanDest, f.Name)
		if err != nil {
			return fmt.Errorf("zip entry %q rejected: %w", f.Name, err)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
		// Ensure the recorded mode survives the umask (executable bits matter
		// for jre/bin/java).
		if err := os.Chmod(target, f.Mode()); err != nil {
			return err
		}
	}
	return nil
}

// safeJoin joins destDir and name after cleaning name, and verifies the
// result stays inside destDir (zip-slip protection).
func safeJoin(destDir, name string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(name))
	target := filepath.Join(destDir, cleaned)
	if target != destDir && !strings.HasPrefix(target, destDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the destination directory", name)
	}
	return target, nil
}
