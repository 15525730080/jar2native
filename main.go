package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// Config holds all CLI configuration.
type Config struct {
	JarFile      string
	JDKPath      string
	ExtraModules []string
	AllModules   bool
	TargetOS     string // GOOS for the output binary (empty = current)
	TargetArch   string // GOARCH for the output binary (empty = current)
}

// extraModulesFlag is a custom flag type for repeated --extra-module flags.
type extraModulesFlag []string

func (e *extraModulesFlag) String() string { return strings.Join(*e, ",") }
func (e *extraModulesFlag) Set(v string) error {
	*e = append(*e, v)
	return nil
}

const version = "2.0.0"

func main() {
	var (
		jdkPath    string
		allModules bool
		showVer    bool
		targetOS   string
		targetArch string
		extraMods  extraModulesFlag
	)

	flag.StringVar(&jdkPath, "jdk-path", "", "Custom JDK installation path (optional)")
	flag.BoolVar(&allModules, "all-modules", false, "Include all JDK modules (maximum compatibility, larger output)")
	flag.BoolVar(&showVer, "version", false, "Print version and exit")
	flag.StringVar(&targetOS, "os", "", "Target OS: linux, darwin, windows (default: current OS)")
	flag.StringVar(&targetArch, "arch", "", "Target arch: amd64, arm64 (default: current arch)")
	flag.Var(&extraMods, "extra-module", "Additional Java module to include (repeatable)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `jar2native %s - Package JAR/WAR into a single self-contained binary

Usage:
  jar2native [options] <file.jar|file.war>

Options:
`, version)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  # Basic — produces dist/myapp (or dist/myapp.exe on Windows)
  jar2native myapp.jar

  # Cross-compile for Linux amd64
  jar2native myapp.jar --os linux --arch amd64

  # Extra Java modules
  jar2native myapp.war --extra-module java.sql --extra-module java.naming

  # Custom JDK path
  jar2native myapp.jar --jdk-path /usr/lib/jvm/java-17-openjdk

How it works:
  1. jdeps  — detect required Java modules
  2. jlink  — build a minimal JRE (only needed modules)
  3. embed  — pack JAR + JRE into a zip, embed into a Go source file
  4. go build — compile a single self-contained binary
  5. Run    — on first launch the binary extracts itself to a cache dir,
              then executes: jre/bin/java -jar app.jar
`)
	}

	flag.Parse()

	if showVer {
		fmt.Printf("jar2native %s\n", version)
		os.Exit(0)
	}

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "[ERROR] Missing required argument: <file.jar|file.war>")
		flag.Usage()
		os.Exit(1)
	}

	cfg := &Config{
		JarFile:      flag.Arg(0),
		JDKPath:      jdkPath,
		ExtraModules: []string(extraMods),
		AllModules:   allModules,
		TargetOS:     targetOS,
		TargetArch:   targetArch,
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
		fmt.Fprintf(os.Stderr, "[ERROR] Packaging failed: %v\n", err)
		os.Exit(1)
	}
}
