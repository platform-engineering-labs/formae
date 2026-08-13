// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// omarchyColors is the flat colors.toml an Omarchy theme publishes in its
// current-theme directory. Absent keys stay empty; the mapper
// (mapOmarchyPalette) applies per-key fallbacks.
//
// Two generations of the format are read from the same struct. Omarchy 3 named
// colors by ANSI slot (color0..color15) and declared light mode with a
// light.mode marker file. Omarchy 4 ("Quattro") replaced both: colors carry
// semantic role names and mode is a key in this file. The two vocabularies are
// disjoint in practice, so every field simply stays empty for the generation
// that does not use it and the mapper's fallback chains pick whichever is
// populated. Canonical (semantic) names are tried first, matching the
// precedence omarchy-theme-color applies.
type omarchyColors struct {
	// Mode is Omarchy 4's declared appearance, "light" or "dark".
	Mode string `toml:"mode"`

	Background          string `toml:"background"`
	Foreground          string `toml:"foreground"`
	Accent              string `toml:"accent"`
	Cursor              string `toml:"cursor"`
	SelectionForeground string `toml:"selection_foreground"`
	SelectionBackground string `toml:"selection_background"`

	// Omarchy 4 semantic names. Only the roles formae's palette consumes are
	// declared; the rest of the 24-color set is deliberately not carried.
	Muted           string `toml:"muted"`
	LightForeground string `toml:"light_foreground"`
	Red             string `toml:"red"`
	Green           string `toml:"green"`
	Yellow          string `toml:"yellow"`
	Blue            string `toml:"blue"`
	Magenta         string `toml:"magenta"`

	Color0  string `toml:"color0"`
	Color1  string `toml:"color1"`
	Color2  string `toml:"color2"`
	Color3  string `toml:"color3"`
	Color4  string `toml:"color4"`
	Color5  string `toml:"color5"`
	Color6  string `toml:"color6"`
	Color7  string `toml:"color7"`
	Color8  string `toml:"color8"`
	Color9  string `toml:"color9"`
	Color10 string `toml:"color10"`
	Color11 string `toml:"color11"`
	Color12 string `toml:"color12"`
	Color13 string `toml:"color13"`
	Color14 string `toml:"color14"`
	Color15 string `toml:"color15"`
}

// parseOmarchyColors decodes an Omarchy colors.toml document.
func parseOmarchyColors(data []byte) (omarchyColors, error) {
	var oc omarchyColors
	if err := toml.Unmarshal(data, &oc); err != nil {
		return omarchyColors{}, fmt.Errorf("parse omarchy colors: %w", err)
	}
	return oc, nil
}

// mapOmarchyPalette translates an Omarchy colors.toml into formae's semantic
// palette (design §6.1). Each color is mirrored onto both adaptive sides: the
// Omarchy palette is the terminal's actual colors, so it renders faithfully
// regardless of background detection. Per-key fallbacks keep every semantic
// slot populated even from a sparse colors.toml.
func mapOmarchyPalette(oc omarchyColors) paletteFile {
	pick := func(vals ...string) string {
		for _, v := range vals {
			if v != "" {
				return v
			}
		}
		return ""
	}
	mirror := func(hex string) *colorValue {
		if hex == "" {
			return nil
		}
		return &colorValue{Light: hex, Dark: hex}
	}

	// Each chain reads the Omarchy 4 semantic name first, then the Omarchy 3
	// ANSI slot it replaced. The pairings are omarchy's own (see the ansi_alias
	// table in omarchy-theme-color): muted==color8, red==color1, green==color2,
	// yellow==color3, blue==color4, magenta==color5, light_foreground==color7.
	textSecondary := pick(oc.LightForeground, oc.Color7, oc.Foreground)
	textSubtle := pick(oc.Muted, oc.Color8, oc.Color7, oc.Foreground)
	border := pick(oc.Muted, oc.Color8, oc.Color7)
	primary := pick(oc.Accent, oc.Blue, oc.Color4)
	secondary := pick(oc.Magenta, oc.Color5, oc.Accent)
	muted := pick(oc.Muted, oc.Color8)
	red := pick(oc.Red, oc.Color1)
	green := pick(oc.Green, oc.Color2)
	yellow := pick(oc.Yellow, oc.Color3)

	return paletteFile{
		Base:          mirror(oc.Background),
		Surface:       mirror(oc.Background),
		TextPrimary:   mirror(oc.Foreground),
		TextSecondary: mirror(textSecondary),
		TextSubtle:    mirror(textSubtle),
		Border:        mirror(border),
		// The cursor row keeps each cell's own foreground and draws this band behind
		// it, so the band must sit next to the background, not next to the text.
		// selection_background is a terminal text-selection color, meaningful only
		// when paired with selection_foreground (which replaces the text color too),
		// so it is deliberately not mapped here. Omarchy 4's "selection" key is the
		// same thing under a new name (omarchy derives selection_background from it
		// and pairs it with bright_foreground), so it is not mapped either.
		//
		// Derivation needs a parseable background. A palette whose background is
		// not hex (termenv also accepts a bare ANSI index) leaves this unset, and
		// merge inherits the base theme's hand-authored band instead.
		Selection:       mirror(selectionBand(oc.Background, oc.Foreground, primary)),
		PrimaryAccent:   mirror(primary),
		SecondaryAccent: mirror(secondary),
		Error:           mirror(red),
		ErrorSubtle:     mirror(red),
		ErrorBright:     mirror(red),
		Warning:         mirror(yellow),
		// Unmanaged mirrors Error (both red), matching quiet's own
		// unmanaged==error relationship, so the inventory marker follows the
		// palette instead of a fixed red.
		Unmanaged:  mirror(red),
		Done:       mirror(green),
		InProgress: mirror(pick(oc.Muted, oc.Color8, oc.Color7)),
		Pending:    mirror(muted),
		OpCreate:   mirror(green),
		OpUpdate:   mirror(yellow),
		OpDelete:   mirror(red),
		OpReplace:  mirror(secondary),
		OpDetach:   mirror(muted),
		OpKeep:     mirror(muted),
		// Color the logo wordmark with the OS accent so the printed logo follows
		// the Omarchy theme, mirroring how rich tints its wordmark. The propeller
		// stays brand orange.
		LogoWordmark: mirror(primary),
	}
}

// omarchyThemeDirs lists the candidate OS theme directories, current generation
// first. Omarchy 4 keeps the active theme under ~/.local/state and deletes the
// ~/.config location on upgrade without leaving a symlink behind, so both have
// to be probed: a Quattro machine only has the first, an Omarchy 3 machine only
// the second. Either entry is omitted when the environment cannot name it.
func omarchyThemeDirs() []string {
	var dirs []string
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		dirs = append(dirs, filepath.Join(state, "omarchy", "current", "theme"))
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "state", "omarchy", "current", "theme"))
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(cfg, "omarchy", "current", "theme"))
	}
	return dirs
}

// omarchyThemeDir is the OS theme directory the "omarchy" theme reads: the
// first candidate that actually holds a colors.toml. With no Omarchy install at
// all it names the current generation's path, so the resolver's warning points
// at where a theme is expected rather than at a location Omarchy has retired.
// Empty when the environment names no candidate (no HOME).
func omarchyThemeDir() string {
	dirs := omarchyThemeDirs()
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, "colors.toml")); err == nil {
			return dir
		}
	}
	if len(dirs) == 0 {
		return ""
	}
	return dirs[0]
}

// resolveOmarchy builds the "omarchy" theme by mapping dir/colors.toml onto
// quiet's resolved themeFile (glyphs/progress/spinner/behavior inherited). Any
// failure — no dir, unreadable or malformed colors.toml — warns once and falls
// back to quiet, exactly like an unknown theme name.
func resolveOmarchy(dir string, warn func(string)) *Theme {
	fallback := func(msg string) *Theme {
		warn(msg)
		th, _ := loadBuiltin("quiet")
		return th
	}
	if dir == "" {
		return fallback("formae: cli.theme \"omarchy\" but no Omarchy theme dir; falling back to quiet")
	}
	path := filepath.Join(dir, "colors.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback(fmt.Sprintf("formae: cli.theme \"omarchy\": cannot read %s: %v; falling back to quiet", path, err))
	}
	oc, err := parseOmarchyColors(data)
	if err != nil {
		return fallback(fmt.Sprintf("formae: cli.theme \"omarchy\": %v; falling back to quiet", err))
	}

	base := quietRequiredFields() // quiet's complete resolved themeFile
	overlay := &themeFile{Name: "omarchy", Palette: mapOmarchyPalette(oc)}
	merged := mergeThemeFiles(base, overlay)
	return merged.toTheme()
}

// omarchyAutoAppearance reports the appearance ("light"/"dark") the Omarchy
// theme at dir declares: the Omarchy 4 mode key in colors.toml first, then the
// Omarchy 3 light.mode marker file, defaulting to dark. Returns "" when dir has
// no colors.toml (no Omarchy theme to follow).
//
// omarchy-theme-color also accepts a legacy theme_type key and, failing
// everything, guesses from background luminance. Neither is mirrored here: no
// Omarchy theme has ever shipped theme_type, and the luminance guess only
// applies to hand-written user themes that declare no mode at all, which land
// on the same dark default either way.
func omarchyAutoAppearance(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "colors.toml"))
	if err != nil {
		return ""
	}
	// Omarchy 4 declares the appearance in colors.toml. Only the two values it
	// defines are a verdict; anything else falls through to the marker, so a
	// theme with an unrecognised mode is treated as not having declared one.
	if oc, err := parseOmarchyColors(data); err == nil {
		switch oc.Mode {
		case "light", "dark":
			return oc.Mode
		}
	}
	// Omarchy 3 marked light themes with a file beside colors.toml.
	if _, err := os.Stat(filepath.Join(dir, "light.mode")); err == nil {
		return "light"
	}
	return "dark"
}

// OmarchyAutoAppearance reports the OS Omarchy theme's declared appearance
// ("light"/"dark"), or "" when no Omarchy theme is active. Used to let
// cli.appearance="auto" follow the OS theme under cli.theme="omarchy".
func OmarchyAutoAppearance() string {
	return omarchyAutoAppearance(omarchyThemeDir())
}
