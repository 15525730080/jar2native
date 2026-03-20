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

const defaultModules = "java.base,java.instrument,java.logging"

// ModuleAnalyzer analyzes Java module dependencies for JAR/WAR files.
type ModuleAnalyzer struct {
	jdk *JDKManager
}

// NewModuleAnalyzer creates a new ModuleAnalyzer.
func NewModuleAnalyzer(jdk *JDKManager) *ModuleAnalyzer {
	return &ModuleAnalyzer{jdk: jdk}
}

// Analyze returns the required modules for the given JAR/WAR file.
// If useAll is true, returns all available JDK modules.
func (a *ModuleAnalyzer) Analyze(jarPath string, useAll bool) (string, error) {
	if useAll {
		logInfo("Using all JDK modules")
		return a.jdk.ListAllModules()
	}
	return a.analyzeFile(jarPath)
}

// analyzeFile dispatches to WAR or JAR analysis.
func (a *ModuleAnalyzer) analyzeFile(path string) (string, error) {
	if strings.ToLower(filepath.Ext(path)) == ".war" {
		return a.analyzeWAR(path)
	}
	return a.runJdeps([]string{path})
}

// analyzeWAR extracts a WAR file to a temp dir and analyzes its contents.
func (a *ModuleAnalyzer) analyzeWAR(warPath string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "jar2native-war-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	logInfo("Extracting WAR file to temporary directory")
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
		logWarn("No analysis targets found in WAR, using default modules")
		return defaultModules, nil
	}
	return a.runJdeps(targets)
}

// runJdeps runs jdeps on the given targets and returns the module list.
func (a *ModuleAnalyzer) runJdeps(targets []string) (string, error) {
	args := append([]string{"--print-module-deps"}, targets...)
	out, err := exec.Command(a.jdk.Bin("jdeps"), args...).Output()
	if err == nil {
		result := strings.TrimSpace(string(out))
		if result != "" {
			return result, nil
		}
	}

	// Fallback: --list-deps mode
	logInfo("Fallback to --list-deps mode")
	args2 := append([]string{"--list-deps"}, targets...)
	out2, err2 := exec.Command(a.jdk.Bin("jdeps"), args2...).Output()
	if err2 != nil {
		logWarn("jdeps execution failed, using default modules")
		return defaultModules, nil
	}
	return parseListDeps(string(out2)), nil
}

// parseListDeps extracts module names from --list-deps output.
// Lines look like: "   java.base [JDK internal API]" or "   [java.base]"
func parseListDeps(output string) string {
	modules := map[string]struct{}{"java.base": {}}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if start := strings.Index(line, "["); start != -1 {
			if end := strings.Index(line, "]"); end > start {
				mod := line[start+1 : end]
				if mod != "" && !strings.Contains(mod, " ") {
					modules[mod] = struct{}{}
				}
			}
		}
	}
	result := make([]string, 0, len(modules))
	for m := range modules {
		result = append(result, m)
	}
	sort.Strings(result)
	return strings.Join(result, ",")
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

// extractZip extracts a zip/jar/war archive to destDir.
func extractZip(src, destDir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		target := filepath.Join(destDir, filepath.FromSlash(f.Name))

		// Guard against zip-slip
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
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
	}
	return nil
}
