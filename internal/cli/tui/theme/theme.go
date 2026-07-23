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

// New creates a Theme by resolving the given theme name against built-in and
// user themes (see Resolve). Empty/unknown names fall back to "quiet".
func New(name string) *Theme {
	return Resolve(name)
}
