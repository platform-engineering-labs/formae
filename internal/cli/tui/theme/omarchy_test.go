//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOmarchyColors(t *testing.T) {
	data := []byte(`
background = "#1a1b26"
foreground = "#c0caf5"
accent = "#7aa2f7"
cursor = "#c0caf5"
selection_foreground = "#1a1b26"
selection_background = "#33467c"
color0 = "#15161e"
color1 = "#f7768e"
color2 = "#9ece6a"
color3 = "#e0af68"
color4 = "#7aa2f7"
color5 = "#bb9af7"
color7 = "#a9b1d6"
color8 = "#414868"
`)
	oc, err := parseOmarchyColors(data)
	if err != nil {
		t.Fatalf("parseOmarchyColors: %v", err)
	}
	if oc.Background != "#1a1b26" {
		t.Errorf("Background = %q, want #1a1b26", oc.Background)
	}
	if oc.Accent != "#7aa2f7" {
		t.Errorf("Accent = %q, want #7aa2f7", oc.Accent)
	}
	if oc.Color1 != "#f7768e" {
		t.Errorf("Color1 = %q, want #f7768e", oc.Color1)
	}
	if oc.SelectionBackground != "#33467c" {
		t.Errorf("SelectionBackground = %q, want #33467c", oc.SelectionBackground)
	}
	// color6 absent → empty (mapper handles fallback)
	if oc.Color6 != "" {
		t.Errorf("Color6 = %q, want empty", oc.Color6)
	}
}

func TestParseOmarchyColors_Malformed(t *testing.T) {
	if _, err := parseOmarchyColors([]byte("this is = = not toml")); err == nil {
		t.Error("expected a parse error for malformed TOML")
	}
}

func TestMapOmarchyPalette(t *testing.T) {
	oc := omarchyColors{
		Background: "#1a1b26", Foreground: "#c0caf5", Accent: "#7aa2f7",
		SelectionBackground: "#33467c",
		Color1:              "#f7768e", Color2: "#9ece6a", Color3: "#e0af68",
		Color4: "#7aa2f7", Color5: "#bb9af7", Color7: "#a9b1d6", Color8: "#414868",
	}
	p := mapOmarchyPalette(oc)

	// A representative spread of the table, and the mirror invariant.
	if got := p.Base; got == nil || got.Light != "#1a1b26" || got.Dark != "#1a1b26" {
		t.Errorf("Base = %+v, want both sides #1a1b26", got)
	}
	if got := p.PrimaryAccent; got == nil || got.Dark != "#7aa2f7" {
		t.Errorf("PrimaryAccent = %+v, want #7aa2f7", got)
	}
	if got := p.Error; got == nil || got.Dark != "#f7768e" {
		t.Errorf("Error = %+v, want #f7768e (color1)", got)
	}
	if got := p.OpCreate; got == nil || got.Dark != "#9ece6a" {
		t.Errorf("OpCreate = %+v, want #9ece6a (color2)", got)
	}
	if got := p.Warning; got == nil || got.Dark != "#e0af68" {
		t.Errorf("Warning = %+v, want #e0af68 (color3)", got)
	}
	if got := p.Selection; got == nil || got.Dark != "#33467c" {
		t.Errorf("Selection = %+v, want #33467c", got)
	}
}

func TestMapOmarchyPalette_Fallbacks(t *testing.T) {
	// Only background + foreground + accent + the ANSI reds/greens present;
	// text_secondary/border/etc. must fall back, never end up empty.
	oc := omarchyColors{
		Background: "#000000", Foreground: "#ffffff", Accent: "#0000ff",
		Color1: "#ff0000", Color2: "#00ff00", Color3: "#ffff00",
	}
	p := mapOmarchyPalette(oc)
	// TextSecondary has no color7 → falls back to foreground.
	if got := p.TextSecondary; got == nil || got.Dark != "#ffffff" {
		t.Errorf("TextSecondary = %+v, want fallback to foreground #ffffff", got)
	}
	// SecondaryAccent has no color5 → falls back to accent.
	if got := p.SecondaryAccent; got == nil || got.Dark != "#0000ff" {
		t.Errorf("SecondaryAccent = %+v, want fallback to accent #0000ff", got)
	}
}

func writeOmarchyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := `background = "#1a1b26"
foreground = "#c0caf5"
accent = "#7aa2f7"
color1 = "#f7768e"
color2 = "#9ece6a"
color3 = "#e0af68"
color5 = "#bb9af7"
color8 = "#414868"
`
	if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolveOmarchy_Success(t *testing.T) {
	dir := writeOmarchyFixture(t)
	var warned []string
	th := resolveOmarchy(dir, func(m string) { warned = append(warned, m) })

	if th.Name != "omarchy" {
		t.Errorf("Name = %q, want omarchy", th.Name)
	}
	// Palette is Omarchy-derived...
	if th.Palette.PrimaryAccent.Dark != "#7aa2f7" {
		t.Errorf("PrimaryAccent.Dark = %q, want #7aa2f7", th.Palette.PrimaryAccent.Dark)
	}
	// ...but non-palette planes are inherited from quiet (self-contained root).
	quiet, _ := loadBuiltin("quiet")
	if th.Glyphs.OpCreate != quiet.Glyphs.OpCreate {
		t.Errorf("OpCreate glyph = %q, want quiet's %q (inherited)", th.Glyphs.OpCreate, quiet.Glyphs.OpCreate)
	}
	if len(th.Spinner.Frames) == 0 {
		t.Error("spinner frames should be inherited from quiet, got none")
	}
	if len(warned) != 0 {
		t.Errorf("no warning expected on success, got %v", warned)
	}
}

func TestResolveOmarchy_MissingFile(t *testing.T) {
	var warned []string
	th := resolveOmarchy(t.TempDir(), func(m string) { warned = append(warned, m) })
	if th.Name != "quiet" {
		t.Errorf("missing colors.toml → Name = %q, want quiet fallback", th.Name)
	}
	if len(warned) == 0 {
		t.Error("expected a one-line warning on missing colors.toml")
	}
}

func TestResolveOmarchy_Malformed(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "colors.toml"), []byte("= not toml ="), 0o644)
	var warned []string
	th := resolveOmarchy(dir, func(m string) { warned = append(warned, m) })
	if th.Name != "quiet" {
		t.Errorf("malformed colors.toml → Name = %q, want quiet", th.Name)
	}
	if len(warned) == 0 {
		t.Error("expected a warning on malformed colors.toml")
	}
}

func TestResolve_OmarchyRoutesToOmarchyResolver(t *testing.T) {
	t.Run("success path proves routing", func(t *testing.T) {
		// Install a fixture Omarchy theme at the real omarchyThemeDir path
		// (driven by XDG_CONFIG_HOME) and confirm resolveWithDir("omarchy", ...)
		// produces the Omarchy-derived theme. The unknown-name→quiet fallback
		// can never set Name="omarchy" or pick up this accent, so this only
		// passes if the "omarchy" name is actually routed to resolveOmarchy.
		tmp := t.TempDir()
		t.Setenv("HOME", tmp)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))

		themeDir := filepath.Join(tmp, ".config", "omarchy", "current", "theme")
		if err := os.MkdirAll(themeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		const fixtureAccent = "#7aa2f7"
		body := `background = "#1a1b26"
foreground = "#c0caf5"
accent = "` + fixtureAccent + `"
color1 = "#f7768e"
color2 = "#9ece6a"
color3 = "#e0af68"
`
		if err := os.WriteFile(filepath.Join(themeDir, "colors.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}

		var warned []string
		th := resolveWithDir("omarchy", "", func(m string) { warned = append(warned, m) })
		if th.Name != "omarchy" {
			t.Errorf("Name = %q, want omarchy", th.Name)
		}
		if th.Palette.PrimaryAccent.Dark != fixtureAccent {
			t.Errorf("PrimaryAccent.Dark = %+v, want %q (from Omarchy fixture, unreachable via the quiet fallback)",
				th.Palette.PrimaryAccent, fixtureAccent)
		}
		if len(warned) != 0 {
			t.Errorf("no warning expected on a successful Omarchy resolve, got %v", warned)
		}
	})

	t.Run("missing install still warns and falls back to quiet", func(t *testing.T) {
		// With no Omarchy install, Resolve("omarchy") must warn + fall back to
		// quiet rather than silently succeeding.
		t.Setenv("HOME", t.TempDir()) // empty config dir → no colors.toml
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		var warned []string
		th := resolveWithDir("omarchy", "", func(m string) { warned = append(warned, m) })
		if th.Name != "quiet" {
			t.Errorf("Name = %q, want quiet (omarchy fallback)", th.Name)
		}
		if len(warned) == 0 || !containsAny(warned, "omarchy") {
			t.Errorf("expected an omarchy-specific warning, got %v", warned)
		}
	})
}

// containsAny reports whether any string in xs contains sub.
func containsAny(xs []string, sub string) bool {
	for _, x := range xs {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

func TestOmarchyAutoAppearance(t *testing.T) {
	// Dark theme: colors.toml present, no light.mode marker.
	dark := writeOmarchyFixture(t)
	// Light theme: add a light.mode marker.
	light := writeOmarchyFixture(t)
	if err := os.WriteFile(filepath.Join(light, "light.mode"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := omarchyAutoAppearance(dark); got != "dark" {
		t.Errorf("no marker → %q, want dark", got)
	}
	if got := omarchyAutoAppearance(light); got != "light" {
		t.Errorf("light.mode marker → %q, want light", got)
	}
	if got := omarchyAutoAppearance(filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Errorf("no omarchy theme → %q, want empty", got)
	}
}
