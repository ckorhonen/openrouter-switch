// version_probe.go shares version-fetching and binary-resolution
// helpers between the status verbs (lifecycle.go) and doctor.go so both
// surfaces report the same per-component version, executable path, and
// menubar-binary skew signal. Stdlib only; no external dependencies.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// processExePath returns the executable path of the running pid via
// `ps -o comm= -p <pid>` on darwin. Empty on failure or non-darwin: a
// graceful blank rather than a hard error, matching the status rows'
// best-effort posture. The comm= format strips the column header so the
// output is just the path (ps may truncate it; that is acceptable for
// the skew smell, which is about the path prefix, not the full argv).
func processExePath(pid int) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// menubarBinaryCandidates returns the paths the menubar app would
// search for the openrouter-switch binary, in documented lookup order
// (the menubar integration contract, OpenRouterSwitchState.swift locateOpenRouterSwitchBinary):
// $OPENROUTER_SWITCH_GATEWAY_BIN, ~/.local/bin/openrouter-switch, then the two brew opt
// symlinks. $HOME-derived so tests can point ~/.local/bin at a temp
// dir; the brew paths are only probed when the caller passes them
// (tests constrain the list via the env seams so no real host binary
// is ever exec'd).
func menubarBinaryCandidates() []string {
	var out []string
	if v := os.Getenv("OPENROUTER_SWITCH_GATEWAY_BIN"); v != "" {
		out = append(out, v)
	}
	if p := menubarLocalBin(); p != "" {
		out = append(out, p)
	}
	return out
}

// menubarLocalBin returns the ~/.local/bin/openrouter-switch candidate.
// Package var, like menubarBrewPaths, so the doctor fixture can swap
// it for a temp path and never probe (or exec) a real host binary.
var menubarLocalBin = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "bin", "openrouter-switch")
}

// menubarBrewPaths are the stable Homebrew opt symlink paths for the
// binary. Package var so tests can swap them for temp dirs and never
// probe a real host brew install.
var menubarBrewPaths = []string{
	"/opt/homebrew/opt/openrouter-switch/bin/openrouter-switch",
	"/usr/local/opt/openrouter-switch/bin/openrouter-switch",
}

// resolveMenubarBinary walks the documented lookup order and returns
// the first existing executable, or "" when nothing resolves. The
// brew paths are appended after the env/home candidates.
func resolveMenubarBinary() string {
	for _, p := range append(menubarBinaryCandidates(), menubarBrewPaths...) {
		if isExecutable(p) {
			return p
		}
	}
	return ""
}

// isExecutable reports whether path exists and is executable by the
// current user. Mirrors FileManager.isExecutableFile in the Swift
// menubar (the documented lookup contract).
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// menubarVersionTimeout bounds the "<resolved> --version" probe.
var menubarVersionTimeout = 2 * time.Second

// menubarBinaryVersion runs "<resolved> --version" with a short timeout
// and returns the version string it prints. The --version output is a
// single line "openrouter-switch vX.Y.Z" (or "openrouter-switch dev"); we return the
// last whitespace token, which is the version itself. Empty on any
// failure (binary missing, timeout, non-zero exit, no parseable token).
func menubarBinaryVersion(resolved string) string {
	if resolved == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), menubarVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, resolved, "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return ""
	}
	// "openrouter-switch v0.2.0" -> "v0.2.0"; "openrouter-switch dev" -> "dev".
	fields := strings.Fields(line)
	return fields[len(fields)-1]
}
