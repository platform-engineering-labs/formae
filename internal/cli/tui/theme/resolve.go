// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"fmt"
	"os"
	"path/filepath"
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
		if th, ok := loadUserTheme(userDir, name); ok {
			return th
		}
	}

	// Step 3: embedded built-in.
	if th, ok := loadBuiltin(name); ok {
		return th
	}

	// Step 4: warn + fall back to quiet.
	warn(fmt.Sprintf("formae: unknown cli.theme %q, falling back to quiet", name))
	th, _ := loadBuiltin("quiet")
	return th
}

// loadUserTheme loads ~/.config/formae/themes/<name>.toml, resolving one level
// of extends against a built-in base. Returns (nil, false) if absent/broken.
func loadUserTheme(dir, name string) (*Theme, bool) {
	data, err := os.ReadFile(filepath.Join(dir, name+".toml"))
	if err != nil {
		return nil, false
	}
	f, err := parseThemeFile(data)
	if err != nil {
		return nil, false
	}
	merged, err := resolveExtends(f)
	if err != nil {
		return nil, false
	}
	return merged.toTheme(), true
}
