// Package payload inspects JAR/WAR artifacts, parses manifests, and assembles
// the payload.zip (application + runtime + manifest.json) with deterministic
// ZIP writing and content hashing.
//
// This file is also embedded into generated runners via runner/shared.go,
// providing the runtime-side extraction and cache logic.
package payload

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ── Manifest ────────────────────────────────────────────────────────────────

const ManifestVersion = 2

// Manifest is embedded into every payload as manifest.json. Written at build
// time, read back by generated runners for integrity checks.
type Manifest struct {
	Version            int    `json:"version"`
	Runtime            string `json:"runtime"`
	OS                 string `json:"os"`
	Arch               string `json:"arch"`
	JREMode            string `json:"jreMode"`
	JavaVersion        string `json:"javaVersion"`
	Application        string `json:"application"`
	ApplicationHash    string `json:"applicationHash"`
	RuntimeHash        string `json:"runtimeHash"`
	PayloadContentHash string `json:"payloadContentHash"`
}

// ── Payload ─────────────────────────────────────────────────────────────────

// Payload describes a built payload.zip.
type Payload struct {
	Path        string
	ArchiveHash string // SHA-256 of zip bytes (runner cache key)
	Size        int64
}

// Build writes payload.zip (app + runtime + manifest.json) to zipPath.
// The ZIP is deterministic: fixed timestamps, normalized permissions,
// deflate compression. Hashes are computed on the fly.
func Build(zipPath, appPath, runtimeDir string, mf Manifest) (*Payload, error) {
	appHash, err := hashFile(appPath)
	if err != nil {
		return nil, fmt.Errorf("hash application: %w", err)
	}
	mf.ApplicationHash = appHash

	f, err := os.Create(zipPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	archiveHasher := sha256.New()
	contentHasher := newContentHasher()
	zw := zip.NewWriter(io.MultiWriter(f, archiveHasher))

	appName := filepath.Base(appPath)
	if err := addFile(zw, appPath, appName, contentHasher); err != nil {
		zw.Close()
		return nil, fmt.Errorf("embed application: %w", err)
	}

	runtimeHasher := newContentHasher()
	if err := addDir(zw, runtimeDir, "jre", contentHasher, runtimeHasher); err != nil {
		zw.Close()
		return nil, fmt.Errorf("embed runtime: %w", err)
	}

	mf.PayloadContentHash = contentHasher.sum()
	mf.RuntimeHash = runtimeHasher.sum()
	mfData, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		zw.Close()
		return nil, err
	}
	if err := addBytes(zw, mfData, "manifest.json"); err != nil {
		zw.Close()
		return nil, fmt.Errorf("embed manifest.json: %w", err)
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalize payload zip: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return &Payload{
		Path:        zipPath,
		ArchiveHash: hex.EncodeToString(archiveHasher.Sum(nil)),
		Size:        info.Size(),
	}, nil
}

// ── Artifact inspection ──────────────────────────────────────────────────────

// Kind classifies the input artifact.
type Kind int

const (
	KindExecutableJAR Kind = iota
	KindSpringBootJAR
	KindExecutableWAR
)

func (k Kind) String() string {
	switch k {
	case KindSpringBootJAR:
		return "Spring Boot executable JAR"
	case KindExecutableWAR:
		return "executable WAR"
	default:
		return "executable JAR"
	}
}

// Info describes the inspected input artifact.
type Info struct {
	Kind       Kind
	MainClass  string
	IsWAR      bool
	SpringBoot bool
}

// Inspect opens a JAR/WAR, parses MANIFEST.MF and classifies the artifact.
func Inspect(appPath string) (*Info, error) {
	ext := strings.ToLower(path.Ext(appPath))
	if ext != ".jar" && ext != ".war" {
		return nil, fmt.Errorf("unsupported input %q: only .jar and .war supported", appPath)
	}

	r, err := zip.OpenReader(appPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path.Base(appPath), err)
	}
	defer r.Close()

	var manifestData []byte
	hasBootInf, hasSpringLoader := false, false
	for _, f := range r.File {
		switch {
		case strings.EqualFold(f.Name, "META-INF/MANIFEST.MF"):
			manifestData, _ = readEntry(f)
		case f.Name == "BOOT-INF/" || strings.HasPrefix(f.Name, "BOOT-INF/"):
			hasBootInf = true
		case strings.HasPrefix(f.Name, "org/springframework/boot/loader/"):
			hasSpringLoader = true
		}
	}

	attrs := parseManifest(manifestData)
	mainClass := strings.TrimSpace(manifestAttr(attrs, "Main-Class"))
	springBoot := hasBootInf && hasSpringLoader

	if ext == ".war" {
		if mainClass == "" {
			return nil, fmt.Errorf(`This WAR is not an executable WAR.

%s has no Main-Class in MANIFEST.MF, so java -jar cannot run it.
jar2native does not bundle a Servlet container. Deploy to Tomcat/Jetty.`, path.Base(appPath))
		}
		return &Info{Kind: KindExecutableWAR, MainClass: mainClass, IsWAR: true, SpringBoot: springBoot}, nil
	}

	if mainClass == "" {
		return nil, fmt.Errorf(`JAR file is not executable: %s has no Main-Class in MANIFEST.MF.

jar2native requires a JAR startable with java -jar. Rebuild with Main-Class
(e.g. jar cfe app.jar MainClass).`, path.Base(appPath))
	}
	kind := KindExecutableJAR
	if springBoot {
		kind = KindSpringBootJAR
	}
	return &Info{Kind: kind, MainClass: mainClass, IsWAR: false, SpringBoot: springBoot}, nil
}

// ── Manifest parsing (JAR spec: 72-byte lines, continuation, case-insensitive) ─

func parseManifest(data []byte) map[string]string {
	attrs := map[string]string{}
	var lastName, value string
	flush := func() {
		if lastName != "" {
			attrs[strings.ToLower(lastName)] = value
		}
		lastName, value = "", ""
	}
	for _, rawLine := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		if line == "" {
			flush()
			break
		}
		if strings.HasPrefix(line, " ") {
			if lastName == "" {
				continue
			}
			value += line[1:]
			continue
		}
		flush()
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		lastName = line[:idx]
		value = strings.TrimLeft(line[idx+1:], " ")
	}
	flush()
	return attrs
}

func manifestAttr(attrs map[string]string, name string) string {
	return attrs[strings.ToLower(name)]
}

// ── Deterministic ZIP writing ───────────────────────────────────────────────

// zipEpoch = 1980-01-01T00:00:00Z (minimum ZIP timestamp for reproducibility).
const zipEpoch = 315532800

func fixedTime() time.Time { return time.Unix(zipEpoch, 0).UTC() }

func normalizeMode(m fs.FileMode) fs.FileMode {
	if m.Perm()&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func createEntry(zw *zip.Writer, name string, mode fs.FileMode) (io.Writer, error) {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: fixedTime()}
	h.SetMode(mode)
	return zw.CreateHeader(h)
}

func addFile(zw *zip.Writer, srcPath, name string, hashers ...*contentHasher) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	w, err := createEntry(zw, name, normalizeMode(info.Mode()))
	if err != nil {
		return err
	}
	return copyTo(w, src, name, hashers)
}

func addBytes(zw *zip.Writer, data []byte, name string) error {
	w, err := createEntry(zw, name, 0o644)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func addDir(zw *zip.Writer, srcDir, prefix string, hashers ...*contentHasher) error {
	return filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		nameInZip := prefix + "/" + filepath.ToSlash(rel)
		if d.IsDir() {
			if !strings.HasSuffix(nameInZip, "/") {
				nameInZip += "/"
			}
			_, err := createEntry(zw, nameInZip, 0o755)
			return err
		}
		realPath, err := filepath.EvalSymlinks(p)
		if err != nil {
			realPath = p
		}
		info, err := os.Stat(realPath)
		if err != nil {
			return err
		}
		f, err := os.Open(realPath)
		if err != nil {
			return err
		}
		defer f.Close()
		w, err := createEntry(zw, nameInZip, normalizeMode(info.Mode()))
		if err != nil {
			return err
		}
		return copyTo(w, f, nameInZip, hashers)
	})
}

func copyTo(w io.Writer, r io.Reader, name string, hashers []*contentHasher) error {
	if len(hashers) == 0 {
		_, err := io.Copy(w, r)
		return err
	}
	writers := make([]io.Writer, 0, len(hashers))
	for _, h := range hashers {
		writers = append(writers, h.beginEntry(name))
	}
	_, err := io.Copy(w, io.TeeReader(r, io.MultiWriter(writers...)))
	return err
}

// ── Content hasher (deterministic SHA-256 over entry names + raw content) ───

type contentHasher struct {
	h interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
}

func newContentHasher() *contentHasher { return &contentHasher{h: sha256.New()} }

func (c *contentHasher) beginEntry(name string) io.Writer {
	c.h.Write([]byte(name))
	c.h.Write([]byte{0})
	return c.h
}

func (c *contentHasher) sum() string { return hex.EncodeToString(c.h.Sum(nil)) }

// ── Shared extraction (embedded into generated runners via runner/shared.go) ─

// SafeJoin joins destDir and name after cleaning, verifying the result stays
// inside destDir (zip-slip protection).
//
// This is THE single zip-slip implementation shared by build-time extraction
// and runtime extraction inside generated runners.
func SafeJoin(destDir, name string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(name))
	target := filepath.Join(destDir, cleaned)
	if target != destDir && !strings.HasPrefix(target, destDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the destination directory", name)
	}
	return target, nil
}

// ExtractReader extracts every entry of r into destDir with zip-slip protection.
// Shared by build-time and runtime extraction.
func ExtractReader(r *zip.Reader, destDir string) error {
	cleanDest := filepath.Clean(destDir)
	for _, f := range r.File {
		target, err := SafeJoin(cleanDest, f.Name)
		if err != nil {
			return fmt.Errorf("zip entry %q rejected: %w", f.Name, err)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractFile(f, target); err != nil {
			return fmt.Errorf("zip entry %q: %w", f.Name, err)
		}
	}
	return nil
}

// ExtractFile opens a zip archive at src and extracts it into destDir.
func ExtractFile(src, destDir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	return ExtractReader(&r.Reader, destDir)
}

func extractFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, f.Mode())
}

// ── helpers ──────────────────────────────────────────────────────────────────

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

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
