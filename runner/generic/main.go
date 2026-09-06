package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/fanbozhou/jar2native/runner"
)

func main() {
	executable, err := os.Executable()
	if err != nil {
		fail("locate runner binary", err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		fail("read runner binary", err)
	}
	payloadZip, cfg, err := runner.ParseTrailer(binary)
	if err != nil {
		fail("read runner configuration", err)
	}

	cacheDir, err := runner.EnsureCache(payloadZip, cfg.AppName, cfg.PayloadHash)
	if err != nil {
		fail("extract runtime", err)
	}

	execName := "java"
	if runtime.GOOS == "windows" {
		execName = "java.exe"
	}
	javaExe := filepath.Join(cacheDir, "jre", "bin", execName)
	args := make([]string, 0, len(cfg.JVMArgs)+3+len(os.Args[1:]))
	args = append(args, cfg.JVMArgs...)
	args = append(args, "-jar", filepath.Join(cacheDir, cfg.JarName))
	args = append(args, os.Args[1:]...)

	cmd := exec.Command(javaExe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		fail("start JVM", err)
	}
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fail("wait for JVM", err)
	}
}

func fail(action string, err error) {
	fmt.Fprintln(os.Stderr, "Failed to "+action+":", err)
	os.Exit(1)
}
