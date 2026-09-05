// Command jar2native packages a Java JAR/WAR into a self-contained native
// executable with an embedded JRE. It runs jdeps to determine required
// modules, builds a minimal runtime via jlink (or copies a legacy JRE),
// assembles a deterministic payload.zip, generates a Go runner project, and
// compiles it into the final binary.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fanbozhou/jar2native/analyzer"
	"github.com/fanbozhou/jar2native/jdk"
	"github.com/fanbozhou/jar2native/payload"
	"github.com/fanbozhou/jar2native/runner"
	rtpkg "github.com/fanbozhou/jar2native/runtime"
)

// ── Platform ────────────────────────────────────────────────────────────────

// Platform describes a target OS/arch pair.
type Platform struct {
	OS   string
	Arch string
}

func (p Platform) String() string { return p.OS + "/" + p.Arch }

// hostPlatform returns the current GOOS/GOARCH.
func hostPlatform() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// parsePlatform parses "os/arch" or returns host platform on empty input.
func parsePlatform(s string) (Platform, error) {
	if s == "" {
		return hostPlatform(), nil
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return Platform{}, fmt.Errorf("invalid platform %q: expected os/arch (e.g. linux/amd64)", s)
	}
	return Platform{OS: parts[0], Arch: parts[1]}, nil
}

// ── Config ──────────────────────────────────────────────────────────────────

// Config holds all CLI-provided configuration.
type Config struct {
	AppPath  string
	JDKPath  string
	Output   string
	Platform Platform
	JREMode  jdk.Mode
	Analyze  bool   // run jdeps to trim modules (opt-in)
	Modules  string // comma-separated extra modules
	JVMArgs  string // space-separated JVM args
	Verbose  bool
}

// ── Logging ─────────────────────────────────────────────────────────────────

func logInfo(format string, args ...any) { fmt.Printf("[INFO] "+format+"\n", args...) }
func logStep(format string, args ...any) { fmt.Printf("[STEP] "+format+"\n", args...) }
func logOK(format string, args ...any)   { fmt.Printf("[OK]   "+format+"\n", args...) }
func logWarn(format string, args ...any) { fmt.Printf("[WARN] "+format+"\n", args...) }
func logFail(format string, args ...any) { fmt.Fprintf(os.Stderr, "[FAIL] "+format+"\n", args...) }

// ── Main ────────────────────────────────────────────────────────────────────

func main() {
	if err := run(); err != nil {
		logFail("%s", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseFlags()
	if err != nil {
		return err
	}

	return packageApp(cfg)
}

func parseFlags() (*Config, error) {
	cfg := &Config{}

	flag.StringVar(&cfg.AppPath, "jar", "", "Path to the JAR or WAR file (required)")
	flag.StringVar(&cfg.JDKPath, "jdk", "", "Path to JDK home (default: JAVA_HOME or auto-detect)")
	flag.StringVar(&cfg.Output, "output", "", "Output binary path (default: same name as input without extension)")
	flag.StringVar(&cfg.Output, "o", "", "Shorthand for -output")
	platformFlag := flag.String("platform", "", "Target platform os/arch (default: host)")
	modeFlag := flag.String("jre-mode", "auto", "JRE build mode: auto, jlink, copy")
	flag.BoolVar(&cfg.Analyze, "analyze", false, "Run jdeps to trim unused modules (default: full JRE)")
	flag.StringVar(&cfg.Modules, "modules", "", "Extra JDK modules (comma-separated, use with -analyze)")
	flag.StringVar(&cfg.JVMArgs, "jvm-args", "", "JVM arguments passed to the runner (e.g. \"-Xmx2g -Dfile.encoding=UTF-8\")")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Verbose output")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `jar2native — package Java JAR/WAR into a self-contained native executable.

Usage: jar2native -jar <app.jar|app.war> [options]

Options:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  jar2native -jar app.jar\n")
		fmt.Fprintf(os.Stderr, "  jar2native -jar app.jar -o myapp -platform linux/amd64\n")
		fmt.Fprintf(os.Stderr, "  jar2native -jar app.war --jdk /usr/lib/jvm/jdk-21 --jvm-args \"-Xmx2g\"\n")
	}

	flag.Parse()

	if cfg.AppPath == "" {
		flag.Usage()
		return nil, fmt.Errorf("input file is required (use -jar)")
	}

	absPath, err := filepath.Abs(cfg.AppPath)
	if err != nil {
		return nil, fmt.Errorf("resolve input path: %w", err)
	}
	cfg.AppPath = absPath

	if cfg.Output == "" {
		ext := filepath.Ext(cfg.AppPath)
		cfg.Output = strings.TrimSuffix(filepath.Base(cfg.AppPath), ext)
	}

	cfg.Platform, err = parsePlatform(*platformFlag)
	if err != nil {
		return nil, err
	}

	cfg.JREMode = jdk.Mode(*modeFlag)
	if cfg.JREMode != jdk.ModeAuto && cfg.JREMode != jdk.ModeJLink && cfg.JREMode != jdk.ModeCopy {
		return nil, fmt.Errorf("invalid jre-mode %q: must be auto, jlink, or copy", *modeFlag)
	}

	return cfg, nil
}

// packageApp orchestrates the full packaging pipeline.
func packageApp(cfg *Config) error {
	logStep("Inspecting artifact: %s", filepath.Base(cfg.AppPath))
	info, err := payload.Inspect(cfg.AppPath)
	if err != nil {
		return err
	}
	logOK("Detected %s (Main-Class: %s)", info.Kind, info.MainClass)

	// Resolve JDK.
	logStep("Resolving JDK")
	j, err := jdk.Resolve(cfg.JDKPath, cfg.JREMode)
	if err != nil {
		return err
	}
	logOK("JDK %s at %s (mode: %s)", j.JavaVersionString(), j.Dir, j.Mode)

	// Analyze modules only when explicitly requested.
	var modules []string
	if cfg.Analyze && j.Mode != jdk.ModeCopy {
		logStep("Analyzing dependencies with jdeps")
		a := analyzer.New(j.Dir)
		analyzed, err := a.Analyze(cfg.AppPath, info.IsWAR)
		if err != nil {
			logWarn("jdeps analysis failed: %v — building full runtime", err)
		} else {
			extra := splitCSV(cfg.Modules)
			modules = analyzer.MergeModules(analyzed, extra)
			logOK("Required modules: %s", strings.Join(modules, ", "))
		}
	}

	// Build runtime.
	jreDir := filepath.Join(os.TempDir(), "jar2native-jre-*")
	tmpDir, err := os.MkdirTemp("", "jar2native-build-*")
	if err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	jreDir = filepath.Join(tmpDir, "jre")
	logStep("Building runtime image")
	b := rtpkg.New(j)
	if len(modules) > 0 {
		if err := b.BuildDeps(jreDir, modules); err != nil {
			return fmt.Errorf("build runtime (deps): %w", err)
		}
	} else {
		if err := b.BuildFull(jreDir); err != nil {
			return fmt.Errorf("build runtime (full): %w", err)
		}
	}
	logOK("Runtime image built (%d modules)", len(modules))

	// Assemble payload.
	zipPath := filepath.Join(tmpDir, "payload.zip")
	logStep("Assembling payload")
	mf := payload.Manifest{
		Version:     payload.ManifestVersion,
		Runtime:     "jlink",
		OS:          cfg.Platform.OS,
		Arch:        cfg.Platform.Arch,
		JREMode:     string(j.Mode),
		JavaVersion: j.JavaVersionString(),
		Application: filepath.Base(cfg.AppPath),
	}
	if j.IsLegacy() {
		mf.Runtime = "legacy-jre"
	}
	pl, err := payload.Build(zipPath, cfg.AppPath, jreDir, mf)
	if err != nil {
		return fmt.Errorf("build payload: %w", err)
	}
	logOK("Payload: %d bytes (hash: %s)", pl.Size, pl.ArchiveHash[:16])

	// Generate and compile runner.
	runnerDir := filepath.Join(tmpDir, "runner")
	logStep("Generating runner project")
	jvmArgs := tokenizeArgs(cfg.JVMArgs)
	rcfg := runner.Config{
		PayloadHash: pl.ArchiveHash,
		JarName:     filepath.Base(cfg.AppPath),
		JVMArgs:     jvmArgs,
		OS:          cfg.Platform.OS,
		Arch:        cfg.Platform.Arch,
	}

	// Copy payload.zip into runner dir for embedding.
	if err := os.MkdirAll(runnerDir, 0o755); err != nil {
		return fmt.Errorf("create runner dir: %w", err)
	}
	if err := copyFile(zipPath, filepath.Join(runnerDir, "payload.zip")); err != nil {
		return fmt.Errorf("copy payload to runner dir: %w", err)
	}

	appName := strings.TrimSuffix(filepath.Base(cfg.AppPath), filepath.Ext(cfg.AppPath))
	logStep("Compiling native binary: %s", cfg.Output)
	binaryPath, err := runner.Build(runnerDir, rcfg, appName, cfg.Platform.OS)
	if err != nil {
		return fmt.Errorf("build runner: %w", err)
	}

	// Move binary to final output path.
	finalPath := cfg.Output
	if !filepath.IsAbs(finalPath) {
		finalPath, _ = filepath.Abs(finalPath)
	}
	if err := os.Rename(binaryPath, finalPath); err != nil {
		if err := copyFile(binaryPath, finalPath); err != nil {
			return fmt.Errorf("move binary to output: %w", err)
		}
		os.Remove(binaryPath)
	}
	logOK("Created: %s", finalPath)
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// tokenizeArgs splits a JVM args string respecting double-quoted segments.
// e.g. `-Xmx2g "-Dspring.profiles.active=prod"` → ["-Xmx2g", "-Dspring.profiles.active=prod"]
func tokenizeArgs(s string) []string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	var args []string
	var current strings.Builder
	inQuote := false
	for _, ch := range s {
		switch {
		case ch == '"':
			inQuote = !inQuote
		case ch == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
