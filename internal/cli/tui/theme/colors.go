// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import "github.com/charmbracelet/lipgloss"

// Palette defines the color values for a theme. Colors are lipgloss
// AdaptiveColor values where the first value is for light backgrounds
// and the second for dark backgrounds.
type Palette struct {
	// Base colors
	Base          lipgloss.AdaptiveColor // panel backgrounds, borders
	Surface       lipgloss.AdaptiveColor // elevated surfaces
	TextPrimary   lipgloss.AdaptiveColor // bright white — active content
	TextSecondary lipgloss.AdaptiveColor // medium gray — labels, metadata
	TextSubtle    lipgloss.AdaptiveColor // dark gray — hints, disabled
	Border        lipgloss.AdaptiveColor // panel borders
	Selection     lipgloss.AdaptiveColor // cursor-row highlight background

	// Accent colors
	PrimaryAccent   lipgloss.AdaptiveColor // blue — IDs, links, interactive
	SecondaryAccent lipgloss.AdaptiveColor // orange — brand, progress, callouts

	// Semantic colors
	Error       lipgloss.AdaptiveColor // red — failures
	ErrorSubtle lipgloss.AdaptiveColor // dimmed red — finished-failed rows
	ErrorBright lipgloss.AdaptiveColor // bright red — cursor on failed rows
	Warning     lipgloss.AdaptiveColor // yellow/gold — drift, warnings

	// State colors (brightness-based, not hue-based)
	Done       lipgloss.AdaptiveColor // bright white
	InProgress lipgloss.AdaptiveColor // dim/medium white
	Pending    lipgloss.AdaptiveColor // dark gray

	// Per-operation colors (rendered by simview/driftview in Plan 2).
	OpCreate  lipgloss.AdaptiveColor
	OpUpdate  lipgloss.AdaptiveColor
	OpDelete  lipgloss.AdaptiveColor
	OpReplace lipgloss.AdaptiveColor
	OpDetach  lipgloss.AdaptiveColor
	OpKeep    lipgloss.AdaptiveColor
}

// FormaePalette returns the new formae color palette.
// Gray/black base with blue and orange accents.
func FormaePalette() Palette {
	return Palette{
		Base:            lipgloss.AdaptiveColor{Light: "#F5F5F5", Dark: "#1A1A2E"},
		Surface:         lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#16213E"},
		TextPrimary:     lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#E8E8E8"},
		TextSecondary:   lipgloss.AdaptiveColor{Light: "#666666", Dark: "#AAAAAA"},
		TextSubtle:      lipgloss.AdaptiveColor{Light: "#999999", Dark: "#999999"},
		Border:          lipgloss.AdaptiveColor{Light: "#DDDDDD", Dark: "#555566"},
		Selection:       lipgloss.AdaptiveColor{Light: "#DDDDDD", Dark: "#3A3A3A"},
		PrimaryAccent:   lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#81D1DB"},
		SecondaryAccent: lipgloss.AdaptiveColor{Light: "#FF6B00", Dark: "#FF8533"},
		Error:           lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"},
		ErrorSubtle:     lipgloss.AdaptiveColor{Light: "#B45454", Dark: "#9B4444"},
		ErrorBright:     lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#FCA5A5"},
		// Decision (PLA-280): use gold #B5B55B for Warning rather than the
		// RFC's brighter yellow — it fits the muted grayscale aesthetic;
		// warnings still read as "colored" against the gray states.
		Warning:    lipgloss.AdaptiveColor{Light: "#B5B55B", Dark: "#B5B55B"},
		Done:       lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#E8E8E8"},
		InProgress: lipgloss.AdaptiveColor{Light: "#444444", Dark: "#AAAAAA"},
		Pending:    lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"},
	}
}
