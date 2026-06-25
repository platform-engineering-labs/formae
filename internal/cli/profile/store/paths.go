// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// warnTo is where ResolveConfigDir emits its one-line both-populated warning.
// Overridable in tests (internal) to capture and assert on the warning.
var warnTo io.Writer = os.Stderr

// ResolveConfigDir returns the formae config directory using a
// compatibility-safe precedence (see design RFC-27):
//  1. FORMAE_CONFIG_DIR (expanded + absolutized) — always wins.
//  2. A populated $HOME/.config/formae — even if $XDG_CONFIG_HOME is also
//     populated (legacy wins; warn on the both-populated tie-break).
//  3. A populated $XDG_CONFIG_HOME/formae.
//  4. Fresh machine: $XDG_CONFIG_HOME/formae if set, else $HOME/.config/formae.
func ResolveConfigDir() (string, error) {
	if d := os.Getenv("FORMAE_CONFIG_DIR"); d != "" {
		return absolutize(d)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	legacy := filepath.Join(home, ".config", "formae")

	var xdg string
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		xdg = filepath.Join(x, "formae")
	}

	legacyPop := hasUserConfig(legacy)
	xdgPop := xdg != "" && hasUserConfig(xdg)

	if legacyPop {
		if xdgPop {
			_, _ = fmt.Fprintf(warnTo,
				"warning: both %s and %s contain formae profiles; using %s. "+
					"Set FORMAE_CONFIG_DIR to choose explicitly.\n", legacy, xdg, legacy)
		}
		return legacy, nil
	}
	if xdgPop {
		return xdg, nil
	}
	if xdg != "" {
		return xdg, nil
	}
	return legacy, nil
}

// absolutize expands a leading ~ and makes the path absolute.
func absolutize(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("absolutize %q: %w", p, err)
	}
	return abs, nil
}

// hasUserConfig reports whether dir holds something worth preserving: a valid
// active pointer (naming an existing profile), at least one profiles/*.pkl, or
// a formae.conf.pkl. A bare empty profiles/ dir or a stale active-only file
// does NOT count.
func hasUserConfig(dir string) bool {
	s := New(dir)
	// Valid active pointer.
	if data, err := os.ReadFile(s.activePath()); err == nil {
		name := strings.TrimSpace(string(data))
		if ValidateName(name) == nil {
			if _, err := os.Stat(s.ProfilePath(name)); err == nil {
				return true
			}
		}
	}
	// Any real profile file.
	if entries, err := os.ReadDir(s.profilesDir()); err == nil {
		for _, e := range entries {
			if e.Type().IsRegular() && strings.HasSuffix(e.Name(), profileExt) {
				return true
			}
		}
	}
	// Legacy config (symlink or regular).
	if _, err := os.Lstat(s.ConfigPath()); err == nil {
		return true
	}
	return false
}
