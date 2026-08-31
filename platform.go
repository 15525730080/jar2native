package main

import (
	"fmt"
	"runtime"
	"strings"
)

// RuntimePlatform identifies an OS/architecture pair, e.g. linux/amd64.
type RuntimePlatform struct {
	OS   string
	Arch string
}

func currentPlatform() RuntimePlatform {
	return RuntimePlatform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func (p RuntimePlatform) String() string {
	return p.OS + "/" + p.Arch
}

// resolveTargetPlatform resolves the requested target platform.
//
// Empty OS/arch default to the current platform. Because the embedded JRE is
// taken from the local machine, cross-platform packaging is impossible: any
// requested target that differs from the build platform is rejected with a
// clear error instead of producing a binary that embeds a foreign JRE.
func resolveTargetPlatform(targetOS, targetArch string) (RuntimePlatform, error) {
	host := currentPlatform()

	if targetOS == "" {
		targetOS = host.OS
	}
	if targetArch == "" {
		targetArch = host.Arch
	}
	targetOS = strings.ToLower(strings.TrimSpace(targetOS))
	targetArch = strings.ToLower(strings.TrimSpace(targetArch))

	// Validate GOOS/GOARCH spelling so typos fail early with a good message.
	validOS := map[string]bool{"linux": true, "darwin": true, "windows": true, "freebsd": true}
	validArch := map[string]bool{"amd64": true, "arm64": true, "386": true}
	if !validOS[targetOS] {
		return RuntimePlatform{}, fmt.Errorf("unknown target OS %q (supported: linux, darwin, windows, freebsd)", targetOS)
	}
	if !validArch[targetArch] {
		return RuntimePlatform{}, fmt.Errorf("unknown target arch %q (supported: amd64, arm64, 386)", targetArch)
	}

	target := RuntimePlatform{OS: targetOS, Arch: targetArch}
	if target != host {
		return target, fmt.Errorf(`Cross-platform JRE packaging is not supported.

Current build environment: %s
Requested target:         %s

The embedded JRE comes from the build machine, so an executable built for %s
would contain a %s JRE and could not run. Please run jar2native on %s
(or omit --os/--arch to build for the current platform).`,
			host, target, target, host, target)
	}
	return target, nil
}

// assertPlatformMatch double-checks that a runtime directory really belongs to
// the target platform. The runtime built by jlink/copy on this machine always
// does; this guard exists so that a future JDK provider cannot silently
// introduce a mismatch between the executable target and the embedded JRE.
func assertPlatformMatch(target RuntimePlatform, buildOS, buildArch string) error {
	if buildOS != target.OS || buildArch != target.Arch {
		return fmt.Errorf(`Embedded JRE platform does not match target executable.

Executable target: %s
Embedded JRE:      %s

Cross-platform JRE packaging is not supported.
Please build on the target platform.`, target, RuntimePlatform{OS: buildOS, Arch: buildArch})
	}
	return nil
}
