// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

// Theme holds the active color palette, derived lipgloss styles, and the
// glyph/progress/spinner/behavior planes loaded from a theme file.
type Theme struct {
	Name            string
	Palette         Palette
	Styles          Styles
	Glyphs          Glyphs
	Progress        Progress
	Spinner         Spinner
	ConfirmationBar ConfirmationBar
}

// New creates a Theme from a theme name ("formae" or "classic").
// Unknown names fall back to "formae".
func New(name string) *Theme {
	switch name {
	case "formae", "classic":
		// valid
	default:
		name = "formae"
	}
	p := PaletteByName(name)
	return &Theme{
		Name:    name,
		Palette: p,
		Styles:  NewStyles(p),
	}
}
