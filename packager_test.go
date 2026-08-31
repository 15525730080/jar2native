package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── manifest parsing ─────────────────────────────────────────────────────────

func TestParseManifestBasic(t *testing.T) {
	mf := "Manifest-Version: 1.0\nMain-Class: com.example.Main\n"
	attrs := parseManifest([]byte(mf))
	if got := manifestAttr(attrs, "Main-Class"); got != "com.example.Main" {
		t.Fatalf("Main-Class = %q, want com.example.Main", got)
	}
	if got := manifestAttr(attrs, "Manifest-Version"); got != "1.0" {
		t.Fatalf("Manifest-Version = %q, want 1.0", got)
	}
}

func TestParseManifestContinuationLines(t *testing.T) {
	// Long values are continued with a single leading space on the next
	// physical line (72-byte limit in the JAR spec).
	mf := "Main-Class: com.example.\n VeryLongMainClassName\n"
	attrs := parseManifest([]byte(mf))
	if got := manifestAttr(attrs, "Main-Class"); got != "com.example.VeryLongMainClassName" {
		t.Fatalf("Main-Class = %q, want com.example.VeryLongMainClassName", got)
	}
}

func TestParseManifestCaseInsensitiveAndNoFalsePositive(t *testing.T) {
	// "X-Main-Class" must NOT be detected as Main-Class; a body containing
	// the word "Main-Class" must not either. Attribute names are
	// case-insensitive.
	mf := "X-Main-Class: nope\nSome-Note: this mentions Main-Class in text\nmain-class: yes\n"
	attrs := parseManifest([]byte(mf))
	if got := manifestAttr(attrs, "Main-Class"); got != "yes" {
		t.Fatalf("Main-Class = %q, want \"yes\" (case-insensitive match)", got)
	}
}

func TestParseManifestStopsAtMainSectionEnd(t *testing.T) {
	mf := "Main-Class: com.example.Main\n\nName: some/entry\nMain-Class: com.other.Wrong\n"
	attrs := parseManifest([]byte(mf))
	if got := manifestAttr(attrs, "Main-Class"); got != "com.example.Main" {
		t.Fatalf("Main-Class = %q, want com.example.Main (individual section must be ignored)", got)
	}
}

func TestParseManifestEmpty(t *testing.T) {
	attrs := parseManifest(nil)
	if len(attrs) != 0 {
		t.Fatalf("expected empty attrs, got %v", attrs)
	}
}

// ── artifact inspection ──────────────────────────────────────────────────────

// buildTestZip writes a zip file with the given entries (name → content).
func buildTestZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInspectAppExecutableJAR(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.jar")
	buildTestZip(t, p, map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nMain-Class: com.example.Main\n",
		"com/example/Main.class": "bytes",
		"some/other.txt":        "text mentioning Main-Class in a resource",
	})
	info, err := inspectApp(p)
	if err != nil {
		t.Fatalf("inspectApp: %v", err)
	}
	if info.MainClass != "com.example.Main" {
		t.Fatalf("MainClass = %q", info.MainClass)
	}
	if info.Kind != AppExecutableJAR {
		t.Fatalf("Kind = %v, want AppExecutableJAR", info.Kind)
	}
}

func TestInspectAppSpringBootJAR(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.jar")
	buildTestZip(t, p, map[string]string{
		"META-INF/MANIFEST.MF": "Main-Class: org.springframework.boot.loader.JarLauncher\n",
		"BOOT-INF/classes/com/App.class":      "bytes",
		"org/springframework/boot/loader/Launcher.class": "bytes",
	})
	info, err := inspectApp(p)
	if err != nil {
		t.Fatalf("inspectApp: %v", err)
	}
	if info.Kind != AppSpringBootJAR || !info.SpringBoot {
		t.Fatalf("Kind = %v SpringBoot = %v, want Spring Boot JAR", info.Kind, info.SpringBoot)
	}
}

func TestInspectAppJARWithoutMainClass(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lib.jar")
	buildTestZip(t, p, map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\n",
	})
	_, err := inspectApp(p)
	if err == nil {
		t.Fatal("expected error for JAR without Main-Class")
	}
	if !strings.Contains(err.Error(), "Main-Class") {
		t.Fatalf("error should mention Main-Class, got: %v", err)
	}
}

func TestInspectAppPlainWARRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.war")
	buildTestZip(t, p, map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\n",
		"WEB-INF/classes/com/App.class": "bytes",
		"WEB-INF/lib/some.jar":          "bytes",
	})
	_, err := inspectApp(p)
	if err == nil {
		t.Fatal("plain WAR must be rejected")
	}
	if !strings.Contains(err.Error(), "not an executable WAR") {
		t.Fatalf("error should explain the WAR limitation, got: %v", err)
	}
}

func TestInspectAppExecutableWARAccepted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.war")
	buildTestZip(t, p, map[string]string{
		"META-INF/MANIFEST.MF": "Main-Class: org.springframework.boot.loader.WarLauncher\n",
		"WEB-INF/classes/com/App.class": "bytes",
	})
	info, err := inspectApp(p)
	if err != nil {
		t.Fatalf("executable WAR should be accepted: %v", err)
	}
	if info.Kind != AppExecutableWAR || !info.IsWAR {
		t.Fatalf("Kind = %v IsWAR = %v", info.Kind, info.IsWAR)
	}
}

func TestInspectAppUnsupportedExtension(t *testing.T) {
	if _, err := inspectApp("/tmp/app.txt"); err == nil {
		t.Fatal("expected error for non-jar/war input")
	}
}

// ── java version parsing ─────────────────────────────────────────────────────

func TestParseJavaVersion(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`java version "1.8.0_492"`, 8},
		{`openjdk version "17.0.18" 2026-01-20`, 17},
		{`openjdk version "21.0.1" 2023-10-17`, 21},
		{`openjdk version "25.0.2" 2026-01-20`, 25},
		{`openjdk version "11" 2018-09-25`, 11},
	}
	for _, c := range cases {
		got, err := parseJavaVersion(c.in)
		if err != nil {
			t.Fatalf("parseJavaVersion(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("parseJavaVersion(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseJavaVersionInvalid(t *testing.T) {
	if _, err := parseJavaVersion("no version here"); err == nil {
		t.Fatal("expected error for unparseable version output")
	}
}

// ── platform validation ──────────────────────────────────────────────────────

func TestResolveTargetPlatformDefaultsToHost(t *testing.T) {
	got, err := resolveTargetPlatform("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != currentPlatform() {
		t.Fatalf("default target = %v, want host %v", got, currentPlatform())
	}
}

func TestResolveTargetPlatformRejectsCrossPlatform(t *testing.T) {
	host := currentPlatform()
	// Pick a target guaranteed to differ from the host.
	foreignOS := "linux"
	if host.OS == "linux" {
		foreignOS = "darwin"
	}
	_, err := resolveTargetPlatform(foreignOS, host.Arch)
	if err == nil {
		t.Fatal("cross-platform target must be rejected")
	}
	msg := err.Error()
	for _, want := range []string{"Cross-platform", "not supported", foreignOS + "/" + host.Arch} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error should contain %q, got: %s", want, msg)
		}
	}
}

func TestResolveTargetPlatformRejectsUnknownValues(t *testing.T) {
	if _, err := resolveTargetPlatform("plan9", ""); err == nil {
		t.Fatal("unknown OS must be rejected")
	}
	if _, err := resolveTargetPlatform("", "riscv64"); err == nil {
		t.Fatal("unknown arch must be rejected")
	}
}

func TestResolveTargetPlatformAcceptsHostExplicitly(t *testing.T) {
	host := currentPlatform()
	if _, err := resolveTargetPlatform(host.OS, host.Arch); err != nil {
		t.Fatalf("explicit host platform must be accepted: %v", err)
	}
}

func TestAssertPlatformMatch(t *testing.T) {
	if err := assertPlatformMatch(RuntimePlatform{OS: "linux", Arch: "amd64"}, "linux", "amd64"); err != nil {
		t.Fatalf("matching platform should pass: %v", err)
	}
	err := assertPlatformMatch(RuntimePlatform{OS: "linux", Arch: "amd64"}, "darwin", "arm64")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched platform should fail with clear error, got: %v", err)
	}
}

// ── module merging ───────────────────────────────────────────────────────────

func TestMergeModules(t *testing.T) {
	got := MergeModules("java.base,java.logging", []string{"java.sql", " java.base "})
	want := "java.base,java.logging,java.sql"
	if got != want {
		t.Fatalf("MergeModules = %q, want %q", got, want)
	}
}

// ── zip-slip protection (build-time extraction) ──────────────────────────────

func TestExtractZipZipSlipProtection(t *testing.T) {
	src := filepath.Join(t.TempDir(), "evil.zip")
	buildTestZip(t, src, map[string]string{
		"ok.txt":             "fine",
		"../evil.txt":        "escape",
		"a/../../evil2.txt":  "escape",
	})
	dest := t.TempDir()
	err := extractZip(src, dest)
	if err == nil {
		t.Fatal("zip with escaping entries must be rejected")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("error should mention escape, got: %v", err)
	}
	// The safe entry may or may not have been written before the error; the
	// evil files must never exist outside dest.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil.txt")); err == nil {
		t.Fatal("evil.txt must not be created outside dest")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil2.txt")); err == nil {
		t.Fatal("evil2.txt must not be created outside dest")
	}
}

func TestExtractZipNormal(t *testing.T) {
	src := filepath.Join(t.TempDir(), "ok.zip")
	buildTestZip(t, src, map[string]string{
		"a.txt":       "A",
		"dir/b.txt":   "B",
		"dir/c/d.txt": "C",
	})
	dest := t.TempDir()
	if err := extractZip(src, dest); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	for name, want := range map[string]string{"a.txt": "A", "dir/b.txt": "B", "dir/c/d.txt": "C"} {
		data, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q, want %q", name, data, want)
		}
	}
}

func TestSafeJoin(t *testing.T) {
	if _, err := safeJoin("/tmp/dest", "normal/file.txt"); err != nil {
		t.Fatalf("normal path should pass: %v", err)
	}
	if _, err := safeJoin("/tmp/dest", "../escape.txt"); err == nil {
		t.Fatal("../escape.txt must be rejected")
	}
	if _, err := safeJoin("/tmp/dest", "a/../../escape.txt"); err == nil {
		t.Fatal("nested escape must be rejected")
	}
	// Absolute paths inside the zip are treated as relative names by
	// filepath.Join; clean+prefix check must still contain them.
	if p, err := safeJoin("/tmp/dest", "/abs.txt"); err != nil || !strings.HasPrefix(p, "/tmp/dest") {
		t.Fatalf("absolute entry should be contained, got %q err %v", p, err)
	}
}
