// Package runner generates a standalone Go project that embeds the payload.zip
// and launches the JRE with the application + user arguments. The generated
// runner is compiled with `go build` into the final native executable.
//
// runner/shared.go is embedded into the generated runner via //go:embed so
// that zip-slip, cache, and manifest logic is shared between build-time and
// runtime without a separate module dependency.
package runner

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// Config holds the data injected into the generated runner.
type Config struct {
	PayloadHash string   // SHA-256 of payload.zip (cache key)
	JarName     string   // Application artifact name (e.g. app.jar)
	JVMArgs     []string // Extra JVM arguments
	OS          string   // Target GOOS
	Arch        string   // Target GOARCH
}

// Build generates a Go project in runnerDir, embeds payload.zip, and compiles
// the final executable. Returns the path to the compiled binary.
func Build(runnerDir string, cfg Config, appName, targetOS string) (string, error) {
	if err := generateProject(runnerDir, cfg, appName); err != nil {
		return "", fmt.Errorf("generate runner project: %w", err)
	}

	binaryName := appName
	if targetOS == "windows" {
		binaryName += ".exe"
	}

	args := []string{"build"}
	if targetOS != "" {
		args = append(args, "-ldflags", "-s -w")
	}
	args = append(args, "-o", binaryName, ".")

	cmd := exec.Command("go", args...)
	cmd.Dir = runnerDir
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+targetOS,
		"GOARCH="+cfg.Arch,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}

	return filepath.Join(runnerDir, binaryName), nil
}

// generateProject writes main.go, shared.go, go.mod, and copies payload.zip
// into runnerDir.
func generateProject(dir string, cfg Config, appName string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Write go.mod (no external dependencies).
	if err := writeIfMissing(filepath.Join(dir, "go.mod"),
		"module runner\n\ngo 1.23\n"); err != nil {
		return err
	}

	// Write shared.go (copied from embed source, package renamed to main).
	sharedSrc := sharedSource
	sharedSrc = strings.Replace(sharedSrc, "package runner", "package main", 1)
	if err := os.WriteFile(filepath.Join(dir, "shared.go"), []byte(sharedSrc), 0644); err != nil {
		return err
	}

	// Pre-render JVM args as a Go []string literal.
	var jvmArgsLiteral string
	if len(cfg.JVMArgs) > 0 {
		var parts []string
		for _, a := range cfg.JVMArgs {
			parts = append(parts, `"`+strings.ReplaceAll(strings.ReplaceAll(a, `\`, `\\`), `"`, `\"`)+`"`)
		}
		jvmArgsLiteral = "[]string{" + strings.Join(parts, ", ") + "}"
	} else {
		jvmArgsLiteral = "[]string{}"
	}

	// Write main.go from template.
	var buf strings.Builder
	if err := runnerTemplate.Execute(&buf, map[string]interface{}{
		"Config":         cfg,
		"AppName":        appName,
		"JVMArgsLiteral": jvmArgsLiteral,
	}); err != nil {
		return fmt.Errorf("execute runner template: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(buf.String()), 0644); err != nil {
		return err
	}

	return nil
}

func writeIfMissing(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// runnerTemplate is the generated runner's main.go. It uses backtick-delimited
// raw string so no backticks appear inside. The generated runner:
//   - embeds payload.zip via //go:embed
//   - extracts to cache dir on first run
//   - launches the JRE with the application + forwarded args
//   - forwards signals and propagates exit codes
var runnerTemplate = template.Must(template.New("runner").Parse(
	`package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
)

//go:embed payload.zip
var payloadZip []byte

func main() {
	appName := "{{.AppName}}"
	payloadHash := "{{.Config.PayloadHash}}"
	jarName := "{{.Config.JarName}}"
	jvmArgs := {{.JVMArgsLiteral}}

	cacheDir, err := EnsureCache(payloadZip, appName, payloadHash)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to extract runtime:", err)
		os.Exit(1)
	}

	jreBin := filepath.Join(cacheDir, "jre", "bin")
	execName := "java"
	if runtime.GOOS == "windows" {
		execName = "java.exe"
		jreBin = filepath.Join(cacheDir, "jre", "bin")
	}
	javaExe := filepath.Join(jreBin, execName)

	args := make([]string, 0, len(jvmArgs)+3+len(os.Args[1:]))
	args = append(args, jvmArgs...)
	args = append(args, "-jar", filepath.Join(cacheDir, jarName))
	args = append(args, os.Args[1:]...)

	cmd := exec.Command(javaExe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Forward signals to the child process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				cmd.Process.Signal(sig)
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to start JVM:", err)
		os.Exit(1)
	}
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "JVM wait error:", err)
		os.Exit(1)
	}
	os.Exit(0)
}
`))

//go:embed shared.go
var sharedSourceFile []byte

var sharedSource = string(sharedSourceFile)
