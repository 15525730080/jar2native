package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Zip helpers (build-time, streaming)
// ─────────────────────────────────────────────────────────────────────────────

// addFileToZip adds a single file to the zip archive under nameInZip.
func addFileToZip(zw *zip.Writer, srcPath, nameInZip string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = nameInZip
	header.Method = zip.Deflate

	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

// addBytesToZip adds an in-memory blob to the zip archive.
func addBytesToZip(zw *zip.Writer, data []byte, nameInZip string) error {
	w, err := zw.Create(nameInZip)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// addDirToZip recursively adds srcDir into the zip under prefix/.
// Symlinks are resolved to their targets (required for JRE bin/).
func addDirToZip(zw *zip.Writer, srcDir, prefix string) error {
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		nameInZip := prefix + "/" + filepath.ToSlash(rel)

		if d.IsDir() {
			if !strings.HasSuffix(nameInZip, "/") {
				nameInZip += "/"
			}
			_, err = zw.Create(nameInZip)
			return err
		}

		// Resolve symlinks so the zip contains real files.
		realPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			realPath = path
		}

		info, err := os.Stat(realPath)
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = nameInZip
		header.Method = zip.Deflate
		header.SetMode(info.Mode())

		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}

		f, err := os.Open(realPath)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
}

// payloadManifest is embedded into every payload as manifest.json and read
// back by the generated runner for integrity checks.
type payloadManifest struct {
	Version          int    `json:"version"`
	Runtime          string `json:"runtime"`
	OS               string `json:"os"`
	Arch             string `json:"arch"`
	JREMode          string `json:"jreMode"`
	JavaVersion      string `json:"javaVersion"`
	Jar2nativeVersion string `json:"jar2nativeVersion"`
	Application      string `json:"application"`
	ApplicationHash  string `json:"applicationHash"`
	PayloadHash      string `json:"payloadHash"`
}

// buildPayloadZip streams app + JRE + manifest.json into the zip file at
// zipPath while hashing the bytes on the fly. The JAR/JRE/zip are never held
// in memory at the same time.
//
// It returns the SHA-256 of the final zip bytes. manifest.json carries the
// SHA-256 of the payload content (application + runtime, i.e. everything
// except the manifest itself) so builds are reproducible and debuggable;
// the returned full-zip hash is what the generated runner embeds and uses as
// its cache key (different payload bytes ⇒ different cache directory).
func buildPayloadZip(zipPath, appPath, jreDir string, mf payloadManifest) (string, error) {
	f, err := os.Create(zipPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fullHasher := sha256.New()
	contentHasher := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(f, fullHasher, contentHasher))

	appName := filepath.Base(appPath)
	if err := addFileToZip(zw, appPath, appName); err != nil {
		return "", fmt.Errorf("embed application: %w", err)
	}
	if err := addDirToZip(zw, jreDir, "jre"); err != nil {
		return "", fmt.Errorf("embed runtime: %w", err)
	}

	// Snapshot the content hash before the manifest entry is written.
	mf.PayloadHash = hex.EncodeToString(contentHasher.Sum(nil))
	mfData, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return "", err
	}
	if err := addBytesToZip(zw, mfData, "manifest.json"); err != nil {
		return "", fmt.Errorf("embed manifest.json: %w", err)
	}

	if err := zw.Close(); err != nil {
		return "", fmt.Errorf("finalize payload zip: %w", err)
	}
	return hex.EncodeToString(fullHasher.Sum(nil)), nil
}

// hashFile streams a file through SHA-256.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Runner template
// ─────────────────────────────────────────────────────────────────────────────

// runnerTemplate is the Go source compiled into the final binary. It embeds
// the payload zip and extracts it atomically (with a lock) on first run.
//
// NOTE: this string is backtick-delimited; the generated code must therefore
// not contain backticks.
const runnerTemplate = `// Code generated by jar2native. DO NOT EDIT.
package main

import (
	_ "embed"
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

//go:embed payload.zip
var payloadZip []byte

const payloadHash = "{{.Hash}}"
const jarName = "{{.JarName}}"

// JVM arguments baked in at build time via --jvm-arg.
var bakedJVMArgs = []string{ {{range .JVMArgs}}{{goquote .}},{{end}} }

func main() {
	os.Exit(run())
}

func run() int {
	// Internal post-build validation hook used by jar2native's smoke test:
	// extract the runtime and run the bundled "java -version".
	if os.Getenv("JAR2NATIVE_SMOKE_TEST") != "" {
		return smokeTest()
	}

	dir, err := extractRuntime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[jar2native] runtime setup failed: %v\n", err)
		return 1
	}

	javaExe := "java"
	if runtime.GOOS == "windows" {
		javaExe = "java.exe"
	}
	javaBin := filepath.Join(dir, "jre", "bin", javaExe)
	jarFile := filepath.Join(dir, jarName)

	// Argument layering:
	//   1. JVM args baked at build time (--jvm-arg)
	//   2. JVM args from JAR2NATIVE_JVM_OPTS (space-separated, no shell syntax)
	//   3. -jar <app>
	//   4. application args (everything after the executable; a leading "--" is
	//      stripped so "./app -- foo bar" passes "foo bar" to the application)
	args := make([]string, 0, len(bakedJVMArgs)+len(os.Args)+3)
	args = append(args, bakedJVMArgs...)
	args = append(args, strings.Fields(os.Getenv("JAR2NATIVE_JVM_OPTS"))...)
	args = append(args, "-jar", jarFile)
	appArgs := os.Args[1:]
	if len(appArgs) > 0 && appArgs[0] == "--" {
		appArgs = appArgs[1:]
	}
	args = append(args, appArgs...)

	cmd := exec.Command(javaBin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "[jar2native] failed to start bundled java (%s): %v\n", javaBin, err)
		return 1
	}

	// Forward termination signals to the JVM child process so that
	// Docker/Kubernetes/systemd stop requests reach the application
	// (e.g. Spring graceful shutdown). Without this the JVM would be
	// orphaned or killed abruptly.
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	forwardDone := make(chan struct{})
	go func() {
		defer close(forwardDone)
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	waitErr := cmd.Wait()
	// signal.Stop stops delivery but does NOT close the channel; closing it
	// here ends the forwarding goroutine's range loop (otherwise <-forwardDone
	// would deadlock after a normal application exit).
	signal.Stop(sigCh)
	close(sigCh)
	<-forwardDone

	if waitErr == nil {
		return 0
	}
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		// Propagate the application exit code verbatim (exit 42 stays 42).
		return exitErr.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "[jar2native] java process failed: %v\n", waitErr)
	return 1
}

// smokeTest extracts the runtime and verifies the bundled java binary runs.
func smokeTest() int {
	dir, err := extractRuntime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "smoke-test: extraction failed: %v\n", err)
		return 1
	}
	javaExe := "java"
	if runtime.GOOS == "windows" {
		javaExe = "java.exe"
	}
	javaBin := filepath.Join(dir, "jre", "bin", javaExe)
	out, err := exec.Command(javaBin, "-version").CombinedOutput()
	fmt.Printf("jar2native smoke test: runtime extracted, bundled java OK\n%s", out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "smoke-test: bundled java -version failed: %v\n", err)
		return 1
	}
	return 0
}

// payloadManifest mirrors the build-time manifest.json.
type payloadManifest struct {
	Version     int    "json:\"version\""
	Runtime     string "json:\"runtime\""
	OS          string "json:\"os\""
	Arch        string "json:\"arch\""
	JREMode     string "json:\"jreMode\""
	PayloadHash string "json:\"payloadHash\""
	Application string "json:\"application\""
}

// extractRuntime returns the cached runtime directory for this payload,
// extracting it atomically on first use. Concurrent processes coordinate via
// a lock file; extraction happens in a temp directory and is made visible
// with a single rename, so no process can ever observe a partial runtime.
func extractRuntime() (string, error) {
	base := os.Getenv("JAR2NATIVE_CACHE_DIR")
	if base == "" {
		var err error
		base, err = os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
	}
	root := filepath.Join(base, "jar2native")
	final := filepath.Join(root, payloadHash)
	lock := final + ".lock"

	if runtimeValid(final) {
		return final, nil
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}

	// Acquire the extraction lock.
	deadline := time.Now().Add(10 * time.Minute)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			f.Close()
			break
		}
		// The lock exists: another process may be extracting, or it is stale.
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > 15*time.Minute {
			// Stale lock left behind by a crashed extractor — steal it.
			_ = os.Remove(lock)
			continue
		}
		// While waiting, the holder may finish the atomic rename.
		if runtimeValid(final) {
			return final, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for another process to extract the runtime (lock: %s)", lock)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer os.Remove(lock)

	// Double-check now that we hold the lock.
	if runtimeValid(final) {
		return final, nil
	}

	tmp, err := os.MkdirTemp(root, payloadHash+".tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	fmt.Fprintln(os.Stderr, "[jar2native] First run: extracting runtime...")
	if err := extractZipTo(tmp); err != nil {
		return "", err
	}
	if err := verifyExtraction(tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	return final, nil
}

// runtimeValid reports whether dir holds a complete runtime whose payload
// was built for the current platform.
func runtimeValid(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return false
	}
	var mf payloadManifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return false
	}
	if mf.Version != 1 || mf.OS != runtime.GOOS || mf.Arch != runtime.GOARCH {
		return false
	}
	return verifyLayout(dir) == nil
}

// verifyExtraction checks the extracted payload in dir.
func verifyExtraction(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("manifest.json missing from payload: %w", err)
	}
	var mf payloadManifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return fmt.Errorf("corrupt manifest.json: %w", err)
	}
	if mf.Version != 1 {
		return fmt.Errorf("unsupported payload manifest version %d", mf.Version)
	}
	if mf.OS != runtime.GOOS || mf.Arch != runtime.GOARCH {
		return fmt.Errorf("payload was built for %s/%s but is running on %s/%s", mf.OS, mf.Arch, runtime.GOOS, runtime.GOARCH)
	}
	return verifyLayout(dir)
}

// verifyLayout checks that the essential runtime files exist.
func verifyLayout(dir string) error {
	javaExe := "java"
	if runtime.GOOS == "windows" {
		javaExe = "java.exe"
	}
	javaBin := filepath.Join(dir, "jre", "bin", javaExe)
	if _, err := os.Stat(javaBin); err != nil {
		return fmt.Errorf("runtime incomplete: %s is missing", javaBin)
	}
	if _, err := os.Stat(filepath.Join(dir, jarName)); err != nil {
		return fmt.Errorf("runtime incomplete: %s is missing", jarName)
	}
	return nil
}

// extractZipTo extracts the embedded payload zip into dir with zip-slip
// protection: every entry is cleaned and verified to stay inside dir.
func extractZipTo(dir string) error {
	r, err := zip.NewReader(bytes.NewReader(payloadZip), int64(len(payloadZip)))
	if err != nil {
		return err
	}
	cleanDest := filepath.Clean(dir)
	for _, f := range r.File {
		cleaned := filepath.Clean(filepath.FromSlash(f.Name))
		target := filepath.Join(cleanDest, cleaned)
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry %q escapes the extraction directory", f.Name)
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
		// Preserve the recorded mode (executable bits matter for jre/bin/java).
		if err := os.Chmod(target, f.Mode()); err != nil {
			return err
		}
	}
	return nil
}
`

// ─────────────────────────────────────────────────────────────────────────────
// BinaryBuilder: generates a temp Go project and compiles it
// ─────────────────────────────────────────────────────────────────────────────

type runnerData struct {
	Hash    string
	JarName string
	JVMArgs []string
}

// BinaryBuilder compiles the final self-contained binary.
type BinaryBuilder struct {
	outputDir string
}

func NewBinaryBuilder(outputDir string) *BinaryBuilder {
	return &BinaryBuilder{outputDir: outputDir}
}

// Build compiles the runner project in runnerDir (which must already contain
// payload.zip and go.mod) into outputName inside b.outputDir.
func (b *BinaryBuilder) Build(runnerDir, payloadHash, jarName string, jvmArgs []string, outputName string, targetOS, targetArch string) (string, error) {
	tmpl, err := template.New("runner").Funcs(template.FuncMap{
		"goquote": func(s string) string { return fmt.Sprintf("%q", s) },
	}).Parse(runnerTemplate)
	if err != nil {
		return "", fmt.Errorf("parse runner template: %w", err)
	}
	var mainBuf bytes.Buffer
	if err := tmpl.Execute(&mainBuf, runnerData{
		Hash:    payloadHash,
		JarName: jarName,
		JVMArgs: jvmArgs,
	}); err != nil {
		return "", fmt.Errorf("render runner template: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runnerDir, "main.go"), mainBuf.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("write runner main.go: %w", err)
	}

	absOutputDir, err := filepath.Abs(b.outputDir)
	if err != nil {
		return "", fmt.Errorf("resolve output dir: %w", err)
	}
	if err := os.MkdirAll(absOutputDir, 0755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	outFile := filepath.Join(absOutputDir, outputName)
	if targetOS == "windows" {
		outFile += ".exe"
	}

	logStep("Compiling binary: %s", outFile)
	goBin, err := findGo()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(goBin, "build", "-ldflags", "-s -w", "-o", outFile, ".")
	cmd.Dir = runnerDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build failed: %w\n%s", err, string(out))
	}

	logOK("Binary compiled: %s", outFile)
	return outFile, nil
}

// findGo locates the go binary.
func findGo() (string, error) {
	if p, err := exec.LookPath("go"); err == nil {
		return p, nil
	}
	candidates := []string{
		"/opt/homebrew/bin/go",
		"/usr/local/go/bin/go",
		"/usr/bin/go",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("go binary not found; jar2native compiles the final executable with Go — please install Go from https://go.dev/dl/")
}

// ─────────────────────────────────────────────────────────────────────────────
// JavaPackager: orchestrates the full workflow
// ─────────────────────────────────────────────────────────────────────────────

// JavaPackager orchestrates the full packaging workflow.
type JavaPackager struct {
	cfg *Config
	jdk *JDKManager
}

// NewJavaPackager creates a new JavaPackager from the given config.
func NewJavaPackager(cfg *Config) (*JavaPackager, error) {
	jdk, err := NewJDKManager(cfg.JDKPath, cfg.JREMode)
	if err != nil {
		return nil, err
	}
	return &JavaPackager{cfg: cfg, jdk: jdk}, nil
}

// Package runs the full packaging workflow.
func (p *JavaPackager) Package() error {
	start := time.Now()

	appPath, err := filepath.Abs(p.cfg.AppFile)
	if err != nil {
		return fmt.Errorf("invalid input path: %w", err)
	}

	// ── Inspect the application ─────────────────────────────────────────────
	logStep("Inspecting application")
	appInfo, err := inspectApp(appPath)
	if err != nil {
		return err
	}
	kindDesc := "executable JAR"
	switch appInfo.Kind {
	case AppSpringBootJAR:
		kindDesc = "Spring Boot executable JAR"
	case AppExecutableWAR:
		kindDesc = "executable WAR"
	}
	logOK("Application: %s (%s, Main-Class: %s)", filepath.Base(appPath), kindDesc, appInfo.MainClass)

	// ── Resolve and validate the target platform ────────────────────────────
	target, err := resolveTargetPlatform(p.cfg.TargetOS, p.cfg.TargetArch)
	if err != nil {
		return err
	}

	// The embedded JRE always comes from this machine: build platform,
	// JRE platform and executable platform must be identical.
	buildOS, buildArch := runtimePlatform()
	if err := assertPlatformMatch(target, buildOS, buildArch); err != nil {
		return err
	}

	// ── Show the build plan ─────────────────────────────────────────────────
	javaVerStr := fmt.Sprintf("Java %d", p.jdk.Version)
	runtimeDesc := "full JRE"
	if p.cfg.JREMode == JREDeps {
		runtimeDesc = "jdeps + jlink minimal runtime"
	}
	fmt.Println()
	fmt.Printf("Application: %s\n", filepath.Base(appPath))
	fmt.Printf("Platform:    %s\n", target)
	fmt.Printf("JRE mode:    %s\n", p.cfg.JREMode)
	fmt.Printf("JDK:         %s (%s)\n", javaVerStr, p.jdk.JDKDir)
	fmt.Printf("Runtime:     %s\n", runtimeDesc)
	fmt.Println()

	// ── Prepare temp workspaces ─────────────────────────────────────────────
	tmpDir, err := os.MkdirTemp("", "jar2native-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	jreDir := filepath.Join(tmpDir, "jre")
	runnerDir := filepath.Join(tmpDir, "runner")

	// ── Build the runtime ───────────────────────────────────────────────────
	logStep("Building runtime (%s mode)", p.cfg.JREMode)
	builder := NewJREBuilder(p.jdk)

	switch p.cfg.JREMode {
	case JREFull:
		if p.jdk.IsLegacy() {
			logInfo("Java %d detected — copying the full JRE (jlink is not available)", p.jdk.Version)
		} else {
			logInfo("Using the complete runtime of the detected JDK (no dependency analysis)")
		}
		if err := builder.BuildFull(jreDir); err != nil {
			return err
		}
	case JREDeps:
		logStep("Analyzing Java dependencies")
		analyzer := NewModuleAnalyzer(p.jdk)
		analyzed, err := analyzer.Analyze(appPath, appInfo.IsWAR)
		if err != nil {
			return err
		}
		finalModules := MergeModules(analyzed, p.cfg.ExtraModules)
		fmt.Println("Detected modules:")
		for _, m := range strings.Split(finalModules, ",") {
			if m != "" {
				fmt.Printf("  %s\n", m)
			}
		}
		if len(p.cfg.ExtraModules) > 0 {
			logInfo("Extra modules merged in: %s", strings.Join(p.cfg.ExtraModules, ", "))
		}
		if err := builder.BuildDeps(jreDir, finalModules); err != nil {
			return err
		}
	}

	// ── Application hash (streamed, for the build manifest) ─────────────────
	appHash, err := hashFile(appPath)
	if err != nil {
		return fmt.Errorf("hash application: %w", err)
	}

	// ── Build the payload (streamed straight to disk) ───────────────────────
	logStep("Packaging payload (application + runtime)")
	if err := os.MkdirAll(runnerDir, 0755); err != nil {
		return fmt.Errorf("create runner dir: %w", err)
	}
	payloadPath := filepath.Join(runnerDir, "payload.zip")
	payloadHash, err := buildPayloadZip(payloadPath, appPath, jreDir, payloadManifest{
		Version:           1,
		Runtime:           "java",
		OS:                target.OS,
		Arch:              target.Arch,
		JREMode:           string(p.cfg.JREMode),
		JavaVersion:       javaVerStr,
		Jar2nativeVersion: version,
		Application:       filepath.Base(appPath),
		ApplicationHash:   appHash,
	})
	if err != nil {
		return fmt.Errorf("build payload: %w", err)
	}
	logInfo("Payload size: %.1f MB  hash: %s…", float64(fileSize(payloadPath))/1e6, payloadHash[:12])

	// ── Write the runner Go project ─────────────────────────────────────────
	goMod := "module runner\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(runnerDir, "go.mod"), []byte(goMod), 0644); err != nil {
		return fmt.Errorf("write runner go.mod: %w", err)
	}

	// ── Compile the final binary ────────────────────────────────────────────
	appName := strings.TrimSuffix(filepath.Base(appPath), filepath.Ext(appPath))
	bb := NewBinaryBuilder("dist")
	outFile, err := bb.Build(runnerDir, payloadHash, filepath.Base(appPath), p.cfg.JVMArgs, appName, target.OS, target.Arch)
	if err != nil {
		return err
	}

	// ── Smoke test ──────────────────────────────────────────────────────────
	if !p.cfg.SkipSmokeTest {
		logStep("Validating executable (smoke test)")
		if err := smokeTest(outFile); err != nil {
			return fmt.Errorf("smoke test failed: %w", err)
		}
	} else {
		logWarn("Smoke test skipped (--skip-smoke-test)")
	}

	// ── Summary ─────────────────────────────────────────────────────────────
	elapsed := time.Since(start)
	fmt.Println()
	fmt.Println("Success:")
	fmt.Printf("  %s\n", outFile)
	fmt.Printf("  (%.1f MB, built in %.1fs, JRE mode: %s)\n", float64(fileSize(outFile))/1e6, elapsed.Seconds(), p.cfg.JREMode)
	fmt.Println()
	fmt.Println("The binary is fully self-contained — just run it:")
	if target.OS == "windows" {
		fmt.Printf("  %s.exe [app-args]\n", appName)
	} else {
		fmt.Printf("  chmod +x dist/%s && dist/%s [app-args]\n", appName, appName)
	}
	fmt.Println()
	fmt.Println("Environment variables honored at runtime:")
	fmt.Println("  JAR2NATIVE_JVM_OPTS   extra JVM args (space-separated, e.g. \"-Xmx1g -Dfoo=bar\")")
	fmt.Println("  JAR2NATIVE_CACHE_DIR  override the runtime extraction cache directory")
	return nil
}

// smokeTest runs the generated executable with JAR2NATIVE_SMOKE_TEST=1:
// it extracts the embedded payload and runs the bundled java -version.
func smokeTest(exePath string) error {
	cmd := exec.Command(exePath)
	cmd.Env = append(os.Environ(), "JAR2NATIVE_SMOKE_TEST=1")
	out, err := cmd.CombinedOutput()
	fmt.Printf("%s", out)
	if err != nil {
		return fmt.Errorf("generated executable failed self-validation: %w", err)
	}
	return nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
