package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// JREBuilder builds the runtime directory to be bundled.
//
//   - full mode (Java 9+): jlink with ALL-MODULE-PATH over the JDK's own
//     jmods — the complete runtime of the current JDK, no jdeps involved.
//   - full mode (Java 8):  the JDK's self-contained jre/ directory is copied.
//   - deps mode (Java 9+): jlink with only the jdeps-analyzed module set.
type JREBuilder struct {
	jdk *JDKManager
}

// NewJREBuilder creates a new JREBuilder.
func NewJREBuilder(jdk *JDKManager) *JREBuilder {
	return &JREBuilder{jdk: jdk}
}

// BuildFull creates a full runtime at outputDir (complete JRE).
func (b *JREBuilder) BuildFull(outputDir string) error {
	if b.jdk.IsLegacy() {
		return b.copyLegacyJRE(outputDir)
	}
	return b.jlink(outputDir, "ALL-MODULE-PATH", true)
}

// BuildDeps creates a minimal runtime at outputDir from the analyzed module set.
func (b *JREBuilder) BuildDeps(outputDir, modules string) error {
	if b.jdk.IsLegacy() {
		return fmt.Errorf("internal error: --deps must be rejected before reaching JREBuilder for Java %d", b.jdk.Version)
	}
	return b.jlink(outputDir, modules, false)
}

// jlink runs jlink against the JDK's jmods directory.
func (b *JREBuilder) jlink(outputDir, modules string, full bool) error {
	modeDesc := "minimal"
	if full {
		modeDesc = "full"
	}
	logStep("Building %s runtime with jlink (Java %d, modules: %s)", modeDesc, b.jdk.Version, modules)

	jmodsPath := filepath.Join(b.jdk.JDKDir, "jmods")
	args := []string{
		"--module-path", jmodsPath,
		"--add-modules", modules,
		"--output", outputDir,
		"--no-header-files",
		"--no-man-pages",
		"--compress", "zip-6",
	}

	cmd := exec.Command(b.jdk.Bin("jlink"), args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Older JDKs (< 21) only support the numeric --compress level; retry
		// once without --compress, then with a numeric level.
		if strings.Contains(strings.ToLower(string(out)), "invalid compression") {
			for _, level := range []string{"6", ""} {
				retry := []string{
					"--module-path", jmodsPath,
					"--add-modules", modules,
					"--output", outputDir,
					"--no-header-files",
					"--no-man-pages",
				}
				if level != "" {
					retry = append(retry, "--compress", level)
				}
				cmd = exec.Command(b.jdk.Bin("jlink"), retry...)
				cmd.Env = append(os.Environ(), "LC_ALL=C")
				out, err = cmd.CombinedOutput()
				if err == nil {
					break
				}
			}
		}
		if err != nil {
			return fmt.Errorf("jlink failed: %w\n%s\n\nPlease make sure %s is a complete JDK with a populated jmods/ directory.", err, strings.TrimSpace(string(out)), b.jdk.JDKDir)
		}
	}

	return b.verifyRuntime(outputDir)
}

// verifyRuntime checks that the built runtime can execute java -version.
func (b *JREBuilder) verifyRuntime(outputDir string) error {
	javaExe := "java"
	if runtime.GOOS == "windows" {
		javaExe = "java.exe"
	}
	javaBin := filepath.Join(outputDir, "bin", javaExe)
	if _, err := os.Stat(javaBin); err != nil {
		return fmt.Errorf("runtime validation failed: %s is missing after jlink", javaBin)
	}
	if out, err := exec.Command(javaBin, "-version").CombinedOutput(); err != nil {
		return fmt.Errorf("runtime validation failed: `%s -version` exited with error: %w\n%s", javaBin, err, strings.TrimSpace(string(out)))
	}
	logOK("Runtime built and verified (%s -version OK)", javaBin)
	return nil
}

// copyLegacyJRE copies the JRE directory for Java 8 and below, preserving
// executable bits, then verifies it.
func (b *JREBuilder) copyLegacyJRE(outputDir string) error {
	srcDir := b.jdk.JREDir()
	logStep("Copying full JRE for Java %d: %s → %s", b.jdk.Version, srcDir, outputDir)

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		dest := filepath.Join(outputDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dest, 0755)
		}

		// Resolve symlinks so the copy contains real files.
		realPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			realPath = path
		}

		info, err := os.Stat(realPath)
		if err != nil {
			return err
		}

		if err := copyFile(realPath, dest, info.Mode()); err != nil {
			return fmt.Errorf("copy %s: %w", rel, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("copy JRE: %w", err)
	}

	return b.verifyRuntime(outputDir)
}

// copyFile copies src to dst preserving the file mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, mode.Perm())
}
