// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

// Theme holds the active color palette and derived lipgloss styles.
type Theme struct {
	Name    string
	Palette Palette
	Styles  Styles
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
