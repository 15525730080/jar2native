package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// JREBuilder builds the runtime directory to be bundled.
//
// Java 9+: uses jlink to create a minimal JRE containing only the required modules.
// Java 8-: copies the existing JRE directory as-is (jlink is not available).
type JREBuilder struct {
	jdk *JDKManager
}

// NewJREBuilder creates a new JREBuilder.
func NewJREBuilder(jdk *JDKManager) *JREBuilder {
	return &JREBuilder{jdk: jdk}
}

// Build prepares the JRE at outputDir.
//
// For Java 9+, modules must be a comma-separated list of module names.
// For Java 8-, modules is ignored and the existing JRE is copied instead.
func (b *JREBuilder) Build(outputDir, modules string) error {
	if b.jdk.IsLegacy() {
		return b.copyLegacyJRE(outputDir)
	}
	return b.buildWithJlink(outputDir, modules)
}

// buildWithJlink creates a minimal JRE using jlink (Java 9+).
func (b *JREBuilder) buildWithJlink(outputDir, modules string) error {
	logStep("Building minimal JRE with jlink (Java %d)", b.jdk.Version)

	jmodsPath := filepath.Join(b.jdk.JDKDir, "jmods")
	cmd := exec.Command(
		b.jdk.Bin("jlink"),
		"--module-path", jmodsPath,
		"--add-modules", modules,
		"--output", outputDir,
		"--no-header-files",
		"--no-man-pages",
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("jlink failed: %w\n%s", err, string(out))
	}

	logOK("Minimal JRE built successfully")
	return nil
}

// copyLegacyJRE copies the JRE directory for Java 8 and below.
func (b *JREBuilder) copyLegacyJRE(outputDir string) error {
	srcDir := b.jdk.JREDir()
	logStep("Copying JRE for Java %d: %s → %s", b.jdk.Version, srcDir, outputDir)

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

		// Skip the root itself
		if rel == "." {
			return nil
		}

		dest := filepath.Join(outputDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dest, 0755)
		}

		// Resolve symlinks so the copy contains real files
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

	logOK("JRE copied successfully")
	return nil
}

// copyFile copies src to dst preserving the file mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
