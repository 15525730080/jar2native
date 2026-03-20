package main

import (
	"fmt"
	"os/exec"
)

// logInfo prints an informational message.
func logInfo(format string, args ...any) {
	fmt.Printf("[INFO] "+format+"\n", args...)
}

// logStep prints a step message.
func logStep(format string, args ...any) {
	fmt.Printf("[STEP] "+format+"\n", args...)
}

// logOK prints a success message.
func logOK(format string, args ...any) {
	fmt.Printf("[OK]   "+format+"\n", args...)
}

// logWarn prints a warning message.
func logWarn(format string, args ...any) {
	fmt.Printf("[WARN] "+format+"\n", args...)
}

// newCmd is a helper to create an exec.Cmd with variadic args.
func newCmd(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
