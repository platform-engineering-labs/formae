// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns the theme for a config name, applying aliases, user-dir
// overrides, built-ins, and a warn-and-fallback-to-quiet for unknown names.
func Resolve(name string) *Theme {
	return resolveWithDir(name, userThemeDir(), func(m string) {
		fmt.Fprintln(os.Stderr, m)
	})
}

// userThemeDir is ~/.config/formae/themes (empty if HOME is unset).
func userThemeDir() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cfg, "formae", "themes")
}

func resolveWithDir(name, userDir string, warn func(string)) *Theme {
	// Step 0: alias rewrite.
	switch name {
	case "", "formae":
		name = "quiet"
	}

	// Step 2: user-dir override (may shadow a built-in name).
	if userDir != "" {
		if th, ok := loadUserTheme(userDir, name, warn); ok {
			return th
		}
	}

	// Step 3: embedded built-in.
	if th, ok := loadBuiltin(name); ok {
		return th
	}

	// Step 4: warn + fall back to quiet.
	warn(fmt.Sprintf("formae: unknown cli.theme %q, falling back to quiet (available: %s)",
		name, strings.Join(builtinNames(), ", ")))
	th, _ := loadBuiltin("quiet")
	return th
}

// loadUserTheme loads ~/.config/formae/themes/<name>.toml, resolving one level
// of extends against a built-in base. A missing file returns (nil, false)
// silently (the resolver falls through to built-ins); a file that exists but
// fails to parse or has an invalid extends is reported via warn before
// returning (nil, false), so a user debugging their own theme sees why.
func loadUserTheme(dir, name string, warn func(string)) (*Theme, bool) {
	path := filepath.Join(dir, name+".toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	f, err := parseThemeFile(data)
	if err != nil {
		warn(fmt.Sprintf("formae: theme file %s failed to parse: %v", path, err))
		return nil, false
	}
	merged, err := resolveExtends(f)
	if err != nil {
		warn(fmt.Sprintf("formae: theme file %s: %v", path, err))
		return nil, false
	}
	return merged.toTheme(), true
}
