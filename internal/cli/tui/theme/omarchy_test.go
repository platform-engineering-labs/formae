//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"os"
	"path/filepath"
	"reflect"
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
	// Derived from the background and accent (bandMix), not selection_background
	// — see TestMapOmarchyPalette_SelectionBandIsDerivedFromBackground.
	if got := p.Selection; got == nil || got.Dark != "#2f3954" {
		t.Errorf("Selection = %+v, want #2f3954", got)
	}
}

// TestMapOmarchyPalette_SelectionBandIsDerivedFromBackground covers a
// palette where selection_background carries the same color as foreground —
// a real Omarchy theme shape. formae keeps each cell's own foreground on the
// cursor row, so mapping selection_background straight onto the band would
// put the band directly behind text of the identical color: 1:1 contrast,
// unreadable. The band must be derived from the background instead.
func TestMapOmarchyPalette_SelectionBandIsDerivedFromBackground(t *testing.T) {
	const reportedForeground = "#FAFCFB"
	oc := omarchyColors{
		Background:          "#1d2021",
		Foreground:          reportedForeground,
		Accent:              "#fe8019",
		SelectionForeground: "#1d2021",
		SelectionBackground: reportedForeground,
	}
	p := mapOmarchyPalette(oc)

	if p.Selection == nil {
		t.Fatal("Selection is nil")
	}
	if p.Selection.Dark == reportedForeground {
		t.Errorf("Selection.Dark = %q, must not be the selection_background/foreground value", p.Selection.Dark)
	}
	if got := contrast(p.Selection.Dark, p.TextPrimary.Dark); got < minBandText {
		t.Errorf("contrast(band, foreground) = %v, want >= %v", got, minBandText)
	}
}

// quattroColors is the semantic palette shape Omarchy 4 publishes: no ANSI
// colorN keys at all, colors named by role instead. Omarchy 4's own "selection"
// key is not carried; see the Selection comment in mapOmarchyPalette.
func quattroColors() omarchyColors {
	return omarchyColors{
		Mode:            "dark",
		Accent:          "#7aa2f7",
		Muted:           "#414868",
		Background:      "#1a1b26",
		Foreground:      "#a9b1d6",
		LightForeground: "#b4bee6",
		Red:             "#f7768e",
		Yellow:          "#e0af68",
		Green:           "#9ece6a",
		Blue:            "#7aa2f7",
		Magenta:         "#ad8ee6",
	}
}

// nilPaletteSlots names every paletteFile field left unset. Reflection rather
// than a hand-listed set, so a slot added to paletteFile later is covered here
// without anyone remembering to extend this test.
func nilPaletteSlots(p paletteFile) []string {
	var nils []string
	v := reflect.ValueOf(p)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsNil() {
			nils = append(nils, v.Type().Field(i).Name)
		}
	}
	return nils
}

// A semantic-only palette must fill every slot. Omarchy 4 dropped colorN
// entirely, so a mapper that reads only ANSI keys leaves most of the palette
// empty and the theme silently inherits quiet's colors.
func TestMapOmarchyPalette_SemanticPaletteFillsEverySlot(t *testing.T) {
	p := mapOmarchyPalette(quattroColors())

	if nils := nilPaletteSlots(p); len(nils) > 0 {
		t.Errorf("unset palette slots from a semantic-only palette: %v", nils)
	}
}

// The semantic names each drive the slot the corresponding ANSI key used to.
func TestMapOmarchyPalette_SemanticNamesDriveSlots(t *testing.T) {
	p := mapOmarchyPalette(quattroColors())

	for _, tc := range []struct {
		slot string
		got  *colorValue
		want string
	}{
		{"Error (red)", p.Error, "#f7768e"},
		{"Warning (yellow)", p.Warning, "#e0af68"},
		{"Done (green)", p.Done, "#9ece6a"},
		{"OpCreate (green)", p.OpCreate, "#9ece6a"},
		{"OpUpdate (yellow)", p.OpUpdate, "#e0af68"},
		{"OpDelete (red)", p.OpDelete, "#f7768e"},
		{"SecondaryAccent (magenta)", p.SecondaryAccent, "#ad8ee6"},
		{"OpReplace (magenta)", p.OpReplace, "#ad8ee6"},
		{"TextSubtle (muted)", p.TextSubtle, "#414868"},
		{"Border (muted)", p.Border, "#414868"},
		{"Pending (muted)", p.Pending, "#414868"},
		{"TextSecondary (light_foreground)", p.TextSecondary, "#b4bee6"},
	} {
		if tc.got == nil || tc.got.Dark != tc.want {
			t.Errorf("%s = %+v, want %s", tc.slot, tc.got, tc.want)
		}
	}
}

// An accent-less semantic palette falls back to blue, the way it used to fall
// back to color4.
func TestMapOmarchyPalette_SemanticAccentFallsBackToBlue(t *testing.T) {
	oc := quattroColors()
	oc.Accent = ""
	p := mapOmarchyPalette(oc)

	if got := p.PrimaryAccent; got == nil || got.Dark != "#7aa2f7" {
		t.Errorf("PrimaryAccent = %+v, want fallback to blue #7aa2f7", got)
	}
}

// A theme carrying both spellings is Omarchy 3 output read by an Omarchy 4
// resolver: the canonical semantic name wins, matching omarchy-theme-color.
func TestMapOmarchyPalette_CanonicalNameWinsOverANSI(t *testing.T) {
	oc := quattroColors()
	oc.Color1 = "#ff0000" // legacy red, must lose to the canonical Red
	oc.Color8 = "#00ff00" // legacy bright-black, must lose to the canonical Muted
	p := mapOmarchyPalette(oc)

	if got := p.Error; got == nil || got.Dark != "#f7768e" {
		t.Errorf("Error = %+v, want canonical red #f7768e to win over color1", got)
	}
	if got := p.TextSubtle; got == nil || got.Dark != "#414868" {
		t.Errorf("TextSubtle = %+v, want canonical muted #414868 to win over color8", got)
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

// installOmarchyTheme writes a minimal colors.toml under root's
// omarchy/current/theme and returns that theme dir.
func installOmarchyTheme(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "omarchy", "current", "theme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte("background = \"#1a1b26\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// omarchyHome points HOME and both XDG roots at fresh temp dirs and returns the
// state and config roots, so a test can install a theme under either.
func omarchyHome(t *testing.T) (stateRoot, configRoot string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	stateRoot = filepath.Join(home, ".local", "state")
	configRoot = filepath.Join(home, ".config")
	t.Setenv("XDG_STATE_HOME", stateRoot)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	return stateRoot, configRoot
}

// Omarchy 4 moved the active theme out of ~/.config into ~/.local/state and
// removed the old directory outright, leaving no compatibility symlink. Looking
// only in the config dir finds nothing on a Quattro machine.
func TestOmarchyThemeDir_FindsStateDir(t *testing.T) {
	stateRoot, _ := omarchyHome(t)
	want := installOmarchyTheme(t, stateRoot)

	if got := omarchyThemeDir(); got != want {
		t.Errorf("omarchyThemeDir() = %q, want the state dir %q", got, want)
	}
}

// Omarchy 3 installs keep the theme in the config dir and must keep working.
func TestOmarchyThemeDir_FallsBackToConfigDir(t *testing.T) {
	_, configRoot := omarchyHome(t)
	want := installOmarchyTheme(t, configRoot)

	if got := omarchyThemeDir(); got != want {
		t.Errorf("omarchyThemeDir() = %q, want the legacy config dir %q", got, want)
	}
}

// A machine upgraded in place can have a stale config-dir theme left over; the
// state dir is the live one.
func TestOmarchyThemeDir_StateDirWinsOverConfigDir(t *testing.T) {
	stateRoot, configRoot := omarchyHome(t)
	want := installOmarchyTheme(t, stateRoot)
	installOmarchyTheme(t, configRoot)

	if got := omarchyThemeDir(); got != want {
		t.Errorf("omarchyThemeDir() = %q, want the state dir %q to win", got, want)
	}
}

// XDG_STATE_HOME is frequently unset; the state dir still resolves under HOME.
func TestOmarchyThemeDir_StateDirDefaultsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	want := installOmarchyTheme(t, filepath.Join(home, ".local", "state"))

	if got := omarchyThemeDir(); got != want {
		t.Errorf("omarchyThemeDir() = %q, want %q", got, want)
	}
}

// The XDG spec calls a relative base directory invalid and says to ignore it,
// which is what os.UserConfigDir does for XDG_CONFIG_HOME. The state dir has to
// agree, or a relative value silently probes (and then names in the warning) a
// working-directory-relative path.
func TestOmarchyThemeDir_RelativeStateHomeIsIgnored(t *testing.T) {
	for _, stateHome := range []string{"relative/path", "   "} {
		t.Run(stateHome, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", stateHome)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			want := installOmarchyTheme(t, filepath.Join(home, ".local", "state"))

			if got := omarchyThemeDir(); got != want {
				t.Errorf("omarchyThemeDir() = %q, want the HOME default %q", got, want)
			}
		})
	}
}

// With no Omarchy install at all the resolver still needs a path to name in its
// warning, and the current generation is the one to name.
func TestOmarchyThemeDir_NoInstallNamesStateDir(t *testing.T) {
	stateRoot, _ := omarchyHome(t)
	want := filepath.Join(stateRoot, "omarchy", "current", "theme")

	if got := omarchyThemeDir(); got != want {
		t.Errorf("omarchyThemeDir() = %q, want %q", got, want)
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

// writeOmarchyColors writes a colors.toml with the given body into a new dir.
func writeOmarchyColors(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Omarchy 4 declares appearance with a mode key inside colors.toml. No Quattro
// theme ships the light.mode marker, so a marker-only check reports every light
// theme as dark.
func TestOmarchyAutoAppearance_ModeKey(t *testing.T) {
	light := writeOmarchyColors(t, "mode = \"light\"\nbackground = \"#ffffff\"\n")
	if got := omarchyAutoAppearance(light); got != "light" {
		t.Errorf("mode = \"light\" → %q, want light", got)
	}

	dark := writeOmarchyColors(t, "mode = \"dark\"\nbackground = \"#1a1b26\"\n")
	if got := omarchyAutoAppearance(dark); got != "dark" {
		t.Errorf("mode = \"dark\" → %q, want dark", got)
	}
}

// The mode key is canonical, so it wins over a stale marker left behind by an
// upgrade.
func TestOmarchyAutoAppearance_ModeKeyWinsOverMarker(t *testing.T) {
	dir := writeOmarchyColors(t, "mode = \"dark\"\nbackground = \"#1a1b26\"\n")
	if err := os.WriteFile(filepath.Join(dir, "light.mode"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := omarchyAutoAppearance(dir); got != "dark" {
		t.Errorf("mode = \"dark\" alongside a light.mode marker → %q, want dark", got)
	}
}

// A mode value outside light/dark is not a verdict; the marker still decides.
func TestOmarchyAutoAppearance_UnknownModeFallsThrough(t *testing.T) {
	dir := writeOmarchyColors(t, "mode = \"sepia\"\nbackground = \"#ffffff\"\n")
	if err := os.WriteFile(filepath.Join(dir, "light.mode"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := omarchyAutoAppearance(dir); got != "light" {
		t.Errorf("unrecognised mode with a light.mode marker → %q, want light", got)
	}
}

// A colors.toml that cannot be parsed must not claim an appearance it does not
// know; the marker still decides.
func TestOmarchyAutoAppearance_MalformedFallsBackToMarker(t *testing.T) {
	dir := writeOmarchyColors(t, "= not toml =")
	if err := os.WriteFile(filepath.Join(dir, "light.mode"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := omarchyAutoAppearance(dir); got != "light" {
		t.Errorf("malformed colors.toml with a light.mode marker → %q, want light", got)
	}
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
