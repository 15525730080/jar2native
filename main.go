package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// JREMode selects how the embedded Java runtime is produced.
//
//	JREFull — default. Bundle the complete runtime of the current JDK.
//	          Maximum compatibility, no dependency analysis.
//	JREDeps — opt-in via --deps. Analyze the application with jdeps and
//	          build a minimal runtime with jlink. Smaller, but applications
//	          using reflection / dynamic loading may fail at runtime.
type JREMode string

const (
	JREFull JREMode = "full"
	JREDeps JREMode = "deps"
)

// Config holds all CLI configuration.
type Config struct {
	AppFile       string
	JDKPath       string
	JREMode       JREMode
	ExtraModules  []string
	AllModules    bool // legacy flag kept for backward compatibility (full JRE is now the default)
	TargetOS      string
	TargetArch    string
	JVMArgs       []string
	SkipSmokeTest bool
}

// stringSliceFlag is a custom flag type for repeatable string flags.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

const version = "3.0.0"

// splitArgs separates CLI tokens into flag tokens and positional arguments,
// allowing options to appear before or after the file argument
// (Go's flag package alone stops parsing at the first non-flag token).
// A "--" terminator makes everything after it positional.
func splitArgs(args []string) (flags, positionals []string) {
	boolFlags := map[string]bool{
		"deps": true, "all-modules": true, "version": true,
		"skip-smoke-test": true, "help": true, "h": true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if strings.Contains(a, "=") {
				continue // value attached: --flag=value
			}
			if boolFlags[strings.TrimLeft(a, "-")] {
				continue // boolean flag: no value to consume
			}
			// String/slice flag: the next token is its value (even if it starts
			// with '-', matching Go flag semantics, e.g. --jvm-arg -Xmx2g).
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return flags, positionals
}

func main() {
	var (
		jdkPath       string
		deps          bool
		allModules    bool
		showVer       bool
		targetOS      string
		targetArch    string
		extraMods     stringSliceFlag
		jvmArgs       stringSliceFlag
		skipSmokeTest bool
		legacyJar     string
	)

	flag.StringVar(&jdkPath, "jdk-path", "", "Use the JDK at this path (validated; default: auto-detect local JDK)")
	flag.BoolVar(&deps, "deps", false, "Opt-in: analyze dependencies with jdeps and build a minimal JRE with jlink (smaller, less compatible)")
	flag.BoolVar(&allModules, "all-modules", false, "(deprecated) Include all JDK modules — this is now the default behavior")
	flag.BoolVar(&showVer, "version", false, "Print version and exit")
	flag.StringVar(&targetOS, "os", "", "Target OS (default: current OS). Must match the current platform: the embedded JRE cannot be cross-built")
	flag.StringVar(&targetArch, "arch", "", "Target arch (default: current arch). Must match the current platform: the embedded JRE cannot be cross-built")
	flag.Var(&extraMods, "extra-module", "Additional Java module to include in --deps mode (repeatable)")
	flag.Var(&jvmArgs, "jvm-arg", "JVM argument baked into the executable, e.g. -Xmx2g or -Dfoo=bar (repeatable)")
	flag.BoolVar(&skipSmokeTest, "skip-smoke-test", false, "Skip the post-build smoke test of the generated executable")
	// Legacy compatibility: the old CLI required "-jar app.jar"; the file
	// argument is positional now, but "-jar app.jar" still works.
	flag.StringVar(&legacyJar, "jar", "", "(legacy alias) the JAR/WAR to package; prefer passing the file directly")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `jar2native %s — Package a JAR/WAR into a single self-contained executable

Usage:
  jar2native [options] <file.jar|file.war>

By default jar2native bundles the FULL Java runtime of a JDK found on this
machine (no jdeps, no module analysis) for maximum compatibility.

Options:
`, version)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  # Default: full JRE, current platform, maximum compatibility
  jar2native myapp.jar

  # Opt-in dependency analysis: smaller runtime, compatibility trade-off
  jar2native myapp.jar --deps

  # Bake JVM arguments into the executable
  jar2native myapp.jar --jvm-arg "-Xmx2g" --jvm-arg "-Dfoo=bar"

  # Extra modules for --deps mode
  jar2native myapp.jar --deps --extra-module java.sql

  # Use a specific local JDK
  jar2native myapp.jar --jdk-path /path/to/jdk

Platform policy:
  The embedded JRE always matches the build machine. jar2native does NOT
  support cross-platform JRE packaging (e.g. building a Linux executable
  with a JRE from macOS). Build on the platform you intend to run on.

How it works:
  1. Inspect  — read MANIFEST.MF, verify the JAR/WAR is executable
  2. JDK      — detect (or take --jdk-path) and strictly validate a local JDK
  3. Runtime  — full JRE by default; jdeps + jlink minimal runtime with --deps
  4. Package  — stream JAR + runtime into payload.zip, embed into a Go binary
  5. Validate — smoke test: run the binary, extract, check bundled java -version

At runtime the executable atomically extracts itself to a cache directory
(override with JAR2NATIVE_CACHE_DIR), forwards signals to the JVM, and
propagates the application exit code.
`)
	}

	// First parse: consumes flags before the file argument (Go's flag
	// package stops at the first non-flag token).
	_ = flag.CommandLine.Parse(os.Args[1:])

	// Allow flags before or after the file argument.
	flagTokens, positionals := splitArgs(flag.Args())
	_ = flag.CommandLine.Parse(flagTokens)

	if showVer {
		fmt.Printf("jar2native %s\n", version)
		os.Exit(0)
	}

	if len(positionals) < 1 {
		if legacyJar == "" {
			fmt.Fprintln(os.Stderr, "[ERROR] Missing required argument: <file.jar|file.war>")
			flag.Usage()
			os.Exit(1)
		}
		positionals = []string{legacyJar}
	}
	if len(positionals) > 1 {
		fmt.Fprintf(os.Stderr, "[ERROR] Unexpected extra arguments: %s\n", strings.Join(positionals[1:], " "))
		flag.Usage()
		os.Exit(1)
	}

	cfg := &Config{
		AppFile:       positionals[0],
		JDKPath:       jdkPath,
		JREMode:       JREFull,
		ExtraModules:  []string(extraMods),
		AllModules:    allModules,
		TargetOS:      targetOS,
		TargetArch:    targetArch,
		JVMArgs:       []string(jvmArgs),
		SkipSmokeTest: skipSmokeTest,
	}
	if deps {
		cfg.JREMode = JREDeps
	}
	if allModules && !deps {
		// Backward compatibility: --all-modules used to mean "full JRE".
		logWarn("--all-modules is deprecated: the full JRE is now the default behavior")
	}

	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  jar2native %s\n", version)
	fmt.Println(strings.Repeat("─", 60))

	packager, err := NewJavaPackager(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}

	if err := packager.Package(); err != nil {
		fmt.Fprintf(os.Stderr, "\n[ERROR] %v\n", err)
		os.Exit(1)
	}
}

// runtimePlatform returns the build machine platform.
func runtimePlatform() (os, arch string) {
	return runtime.GOOS, runtime.GOARCH
}
