// Package runner contains shared source that is embedded into generated runner
// executables. This file (shared.go) is compiled normally by the build side
// AND written verbatim into generated runner projects (with package renamed
// to "main").
//
// It provides: zip-slip-safe extraction, cache management with PID-based lock
// recovery, and the Manifest struct for integrity verification.
//
// IMPORTANT: This file must be self-contained — no imports of
// github.com/fanbozhou/jar2native/* — because it runs inside generated runner
// projects that have no access to the parent module.
package runner

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ManifestVersion identifies the payload format. Bumped on breaking changes.
const ManifestVersion = 2

// Manifest is written into every payload as manifest.json. The runner reads it
// to verify integrity and locate the JRE and application.
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

// ── ZIP extraction with zip-slip protection ───────────────────────────────

// SafeJoin joins destDir and name after cleaning, verifying the result stays
// inside destDir. This is THE single zip-slip guard for runtime extraction.
func SafeJoin(destDir, name string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(name))
	target := filepath.Join(destDir, cleaned)
	if target != destDir && !strings.HasPrefix(target, destDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the destination directory", name)
	}
	return target, nil
}

// ExtractReader extracts every entry of r into destDir with zip-slip protection.
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
	return out.Close()
}

// ── Cache: atomic extract + lock + integrity ─────────────────────────────

// CacheDir returns the platform cache directory for this application.
func CacheDir(appName, payloadHash string) string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "jar2native", appName+"-"+payloadHash[:12])
}

// EnsureCache extracts the embedded payload into the cache directory if not
// already present and valid. Returns the cache dir path.
func EnsureCache(payloadZip []byte, appName, payloadHash string) (string, error) {
	cacheDir := CacheDir(appName, payloadHash)
	manifestPath := filepath.Join(cacheDir, "manifest.json")

	// Fast path: cache exists and content hash matches.
	if _, err := os.Stat(manifestPath); err == nil {
		if cached, err := readManifest(manifestPath); err == nil &&
			cached.PayloadContentHash == hashContent(payloadZip) {
			return cacheDir, nil
		}
	}

	// Acquire lock with PID-based stale recovery.
	lockPath := filepath.Join(cacheDir, ".lock")
	if err := acquireLock(lockPath); err != nil {
		return "", fmt.Errorf("cache lock: %w", err)
	}
	defer os.Remove(lockPath)

	// Atomic extract: extract to temp dir, then rename.
	tmpDir, err := os.MkdirTemp(filepath.Dir(cacheDir), "."+appName+"-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	zr, err := zip.NewReader(strings.NewReader(string(payloadZip)), int64(len(payloadZip)))
	if err != nil {
		return "", fmt.Errorf("open payload: %w", err)
	}
	if err := ExtractReader(zr, tmpDir); err != nil {
		return "", fmt.Errorf("extract payload: %w", err)
	}

	if err := os.RemoveAll(cacheDir); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("clear old cache: %w", err)
	}
	if err := os.Rename(tmpDir, cacheDir); err != nil {
		// Fallback: cross-device move.
		if err := moveAll(tmpDir, cacheDir); err != nil {
			return "", fmt.Errorf("finalize cache: %w", err)
		}
	}
	return cacheDir, nil
}

// ── Lock with PID-based stale recovery ────────────────────────────────────

func acquireLock(lockPath string) error {
	for i := 0; i < 3; i++ {
		err := writeLock(lockPath)
		if err == nil {
			return nil
		}
		if isStaleLock(lockPath) {
			os.Remove(lockPath)
			continue
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("could not acquire lock %s", lockPath)
}

func writeLock(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	pid := []byte(strconv.Itoa(os.Getpid()))
	return os.WriteFile(path, pid, 0o600)
}

func isStaleLock(lockPath string) bool {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return true
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return true
	}
	return !processAlive(pid)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// ── Helpers ───────────────────────────────────────────────────────────────

func readManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	return &m, json.Unmarshal(data, &m)
}

// hashContent is a fast non-crypto hash for cache validation fast-path.
// The authoritative hash is in manifest.json from build time.
func hashContent(data []byte) string {
	h := uint32(5381)
	for _, b := range data {
		h = (h << 5) + h + uint32(b)
	}
	return strconv.FormatUint(uint64(h), 16)
}

func moveAll(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
