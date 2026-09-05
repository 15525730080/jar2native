// Package runtime builds the embedded JRE using jlink (JDK 9+) or copies
// the legacy JRE directory (Java 8).
package runtime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fanbozhou/jar2native/jdk"
)

// Builder creates JRE runtimes via jlink or legacy copy.
type Builder struct {
	jdk *jdk.JDK
}

// New creates a Builder for the given JDK.
func New(j *jdk.JDK) *Builder {
	return &Builder{jdk: j}
}

// BuildFull creates a full runtime image (no module trimming) via jlink.
// Used when jdeps analysis is skipped or unavailable.
func (b *Builder) BuildFull(jreDir string) error {
	return b.runJLink(jreDir, nil)
}

// BuildDeps creates a trimmed runtime image containing only the specified
// modules plus java.base.se service extras.
func (b *Builder) BuildDeps(jreDir string, modules []string) error {
	return b.runJLink(jreDir, modules)
}

// runJLink executes jlink to produce a custom JRE at jreDir.
func (b *Builder) runJLink(jreDir string, modules []string) error {
	if b.jdk.IsLegacy() {
		return b.copyLegacyJRE(jreDir)
	}

	jlink := b.jdk.Bin("jlink")
	if jlink == "" {
		return fmt.Errorf("jlink not found in %s", b.jdk.Dir)
	}

	// Find the jmods directory (JDK 9+: $JAVA_HOME/jmods).
	jmodsDir := filepath.Join(b.jdk.Dir, "jmods")
	if _, err := os.Stat(jmodsDir); err != nil {
		// macOS: sometimes under Contents/Home/jmods
		alt := filepath.Join(b.jdk.Dir, "..", "..", "jmods")
		if _, err2 := os.Stat(alt); err2 == nil {
			jmodsDir = alt
		}
	}

	args := []string{
		"--output", jreDir,
		"--module-path", jmodsDir,
		"--no-header-files",
		"--no-man-pages",
		"--strip-debug",
	}

	if c := jdk.CompressArg(b.jdk.Version); c > 0 {
		args = append(args, "--compress="+fmt.Sprint(c))
	}

	if len(modules) == 0 {
		// Full runtime: add all available modules.
		args = append(args, "--add-modules", "ALL-MODULE-PATH")
	} else {
		args = append(args, "--add-modules", strings.Join(modules, ","))
	}

	cmd := exec.Command(jlink, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("jlink failed: %w", err)
	}

	return b.verify(jreDir)
}

// copyLegacyJRE copies the JRE directory from a Java 8 JDK.
func (b *Builder) copyLegacyJRE(jreDir string) error {
	src := b.jdk.JREDir()
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("legacy JRE not found at %s: %w", src, err)
	}
	if err := os.RemoveAll(jreDir); err != nil {
		return fmt.Errorf("clean jre dir: %w", err)
	}
	if err := copyDir(src, jreDir); err != nil {
		return fmt.Errorf("copy JRE: %w", err)
	}
	return b.verify(jreDir)
}

// verify checks that the built JRE has a working java binary.
func (b *Builder) verify(jreDir string) error {
	javaName := "java"
	if runtime.GOOS == "windows" {
		javaName = "java.exe"
	}
	javaPath := filepath.Join(jreDir, "bin", javaName)
	if _, err := os.Stat(javaPath); err != nil {
		return fmt.Errorf("verify: java binary missing at %s", javaPath)
	}
	return nil
}

// copyDir recursively copies src to dst preserving permissions.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		// Resolve symlinks to copy real files (common in macOS JDKs).
		realPath, err := filepath.EvalSymlinks(p)
		if err != nil {
			realPath = p
		}
		realInfo, err := os.Stat(realPath)
		if err != nil {
			return err
		}

		if realInfo.IsDir() {
			return os.MkdirAll(target, realInfo.Mode())
		}
		return copyFile(realPath, target, realInfo.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
