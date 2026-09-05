// Package analyzer runs jdeps against a JAR/WAR to determine the minimal set
// of JDK modules required by the application.
package analyzer

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Analyzer wraps a jdeps binary path.
type Analyzer struct {
	jdeps   string
	jdkHome string
}

// New creates an Analyzer for the given JDK home.
func New(jdkHome string) *Analyzer {
	return &Analyzer{
		jdeps:   jdkBin(jdkHome, "jdeps"),
		jdkHome: jdkHome,
	}
}

// Analyze runs jdeps --print-module-deps against the application. Returns a
// sorted, de-duplicated module list.
func (a *Analyzer) Analyze(appPath string, isWAR bool) ([]string, error) {
	if a.jdeps == "" {
		return nil, fmt.Errorf("jdeps not found; ensure a JDK (not JRE) is installed")
	}

	// jdeps --print-module-deps is the cleanest API (JDK 14+).
	args := []string{"--print-module-deps", "--multi-release", "base", appPath}

	cmd := exec.Command(a.jdeps, args...)
	cmd.Env = append(os.Environ(), "JDK_HOME="+a.jdkHome)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		// Fallback: parse -verbose:module output if --print-module-deps unavailable.
		mods, fallbackErr := a.analyzeFallback(appPath)
		if fallbackErr != nil {
			return nil, fmt.Errorf("jdeps failed: %s\n%s", err, errOut.String())
		}
		return mods, nil
	}

	raw := strings.TrimSpace(out.String())
	if raw == "" {
		return nil, fmt.Errorf("jdeps returned no modules")
	}
	mods := strings.Split(raw, ",")
	return dedupSort(mods), nil
}

// analyzeFallback runs jdeps -verbose:module and parses the output.
func (a *Analyzer) analyzeFallback(appPath string) ([]string, error) {
	cmd := exec.Command(a.jdeps, "-verbose:module", appPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Text()
		for _, field := range strings.Fields(line) {
			if !strings.HasPrefix(field, "java.") && !strings.HasPrefix(field, "jdk.") &&
				!strings.HasPrefix(field, "javafx.") {
				continue
			}
			m := strings.TrimPrefix(strings.TrimPrefix(field, "module "), "java.base/")
			m = strings.TrimSuffix(m, "/")
			if strings.Contains(m, "/") {
				m = strings.Split(m, "/")[0]
			}
			if m != "" {
				seen[m] = true
			}
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("jdeps fallback found no modules")
	}
	return dedupSort(seenToSlice(seen)), nil
}

// MergeModules merges analyzed and user-provided module lists, de-duplicated
// and sorted. Always includes java.base.
func MergeModules(analyzed, extra []string) []string {
	seen := map[string]bool{"java.base": true}
	for _, m := range analyzed {
		seen[m] = true
	}
	for _, m := range extra {
		m = strings.TrimSpace(m)
		if m != "" {
			seen[m] = true
		}
	}
	return dedupSort(seenToSlice(seen))
}

// ── helpers ──────────────────────────────────────────────────────────────────

func jdkBin(jdkHome, tool string) string {
	if jdkHome == "" {
		if p, err := exec.LookPath(tool); err == nil {
			return p
		}
		return ""
	}
	binDir := jdkHome + "/bin/" + tool
	if _, err := os.Stat(binDir); err == nil {
		return binDir
	}
	if _, err := os.Stat(binDir + ".exe"); err == nil {
		return binDir + ".exe"
	}
	if p, err := exec.LookPath(tool); err == nil {
		return p
	}
	return ""
}

func dedupSort(s []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range s {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func seenToSlice(seen map[string]bool) []string {
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}
