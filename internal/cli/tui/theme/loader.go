// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed themes/*.toml
var builtinFS embed.FS

// loadBuiltin loads an embedded theme by name, resolving one level of extends
// against another embedded theme. Returns (nil, false) if no built-in matches.
func loadBuiltin(name string) (*Theme, bool) {
	f, err := readBuiltin(name)
	if err != nil {
		return nil, false
	}
	merged, err := resolveExtends(f)
	if err != nil {
		// A malformed built-in is a programming error; surface it loudly rather
		// than silently shipping a broken theme.
		panic(fmt.Sprintf("theme: built-in %q: %v", name, err))
	}
	return merged.toTheme(), true
}

func readBuiltin(name string) (*themeFile, error) {
	data, err := builtinFS.ReadFile("themes/" + name + ".toml")
	if err != nil {
		return nil, err
	}
	return parseThemeFile(data)
}

// resolveExtends merges a themeFile onto its (built-in) base, one level deep.
func resolveExtends(f *themeFile) (*themeFile, error) {
	if f.Extends == "" {
		return f, nil
	}
	base, err := readBuiltin(f.Extends)
	if err != nil {
		return nil, fmt.Errorf("extends %q: %w", f.Extends, err)
	}
	if base.Extends != "" {
		return nil, fmt.Errorf("extends is one level only, but %q also extends %q", f.Extends, base.Extends)
	}
	return mergeThemeFiles(base, f), nil
}

// quietRequiredFields returns quiet's resolved themeFile, used as the
// required-field template for user-theme completeness checks (a candidate
// theme is complete iff it has every field quiet has set). Panics on failure
// like loadBuiltin: a broken embedded quiet.toml is a programming error.
func quietRequiredFields() *themeFile {
	f, err := readBuiltin("quiet")
	if err != nil {
		panic(fmt.Sprintf("theme: built-in quiet: %v", err))
	}
	merged, err := resolveExtends(f)
	if err != nil {
		panic(fmt.Sprintf("theme: built-in quiet: %v", err))
	}
	return merged
}

// builtinNames lists the embedded theme names (no .toml suffix).
func builtinNames() []string {
	entries, _ := builtinFS.ReadDir("themes")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
	}
	sort.Strings(names)
	return names
}
