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

	// Accent colors
	PrimaryAccent   lipgloss.AdaptiveColor // blue — IDs, links, interactive
	SecondaryAccent lipgloss.AdaptiveColor // orange — brand, progress, callouts

	// Semantic colors
	Error   lipgloss.AdaptiveColor // red — failures
	Warning lipgloss.AdaptiveColor // yellow/gold — drift, warnings

	// State colors
	Done       lipgloss.AdaptiveColor // green — success
	InProgress lipgloss.AdaptiveColor // dim/medium white
	Pending    lipgloss.AdaptiveColor // dark gray
}

// FormaePalette returns the new formae color palette.
// Gray/black base with blue and orange accents.
func FormaePalette() Palette {
	return Palette{
		Base:            lipgloss.AdaptiveColor{Light: "#F5F5F5", Dark: "#1A1A2E"},
		Surface:         lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#16213E"},
		TextPrimary:     lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#E8E8E8"},
		TextSecondary:   lipgloss.AdaptiveColor{Light: "#666666", Dark: "#888888"},
		TextSubtle:      lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"},
		Border:          lipgloss.AdaptiveColor{Light: "#DDDDDD", Dark: "#333355"},
		PrimaryAccent:   lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"},
		SecondaryAccent: lipgloss.AdaptiveColor{Light: "#FF6B00", Dark: "#FF8533"},
		Error:           lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"},
		Warning:         lipgloss.AdaptiveColor{Light: "#B5B55B", Dark: "#B5B55B"},
		Done:            lipgloss.AdaptiveColor{Light: "#16A34A", Dark: "#4ADE80"},
		InProgress:      lipgloss.AdaptiveColor{Light: "#444444", Dark: "#AAAAAA"},
		Pending:         lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"},
	}
}

// ClassicPalette returns the existing color scheme preserved via lipgloss.
// Matches the gookit/color values in internal/cli/display/color.go.
func ClassicPalette() Palette {
	return Palette{
		Base:            lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#000000"},
		Surface:         lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#111111"},
		TextPrimary:     lipgloss.AdaptiveColor{Light: "#000000", Dark: "#FFFFFF"},
		TextSecondary:   lipgloss.AdaptiveColor{Light: "#666666", Dark: "#808080"},
		TextSubtle:      lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"},
		Border:          lipgloss.AdaptiveColor{Light: "#CCCCCC", Dark: "#444444"},
		PrimaryAccent:   lipgloss.AdaptiveColor{Light: "#5B9BD5", Dark: "#ADD8E6"}, // LightBlue
		SecondaryAccent: lipgloss.AdaptiveColor{Light: "#B5B55B", Dark: "#B5B55B"}, // Gold
		Error:           lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF6666"}, // Red
		Warning:         lipgloss.AdaptiveColor{Light: "#B5B55B", Dark: "#B5B55B"}, // Gold
		Done:            lipgloss.AdaptiveColor{Light: "#008000", Dark: "#00FF00"}, // Green
		InProgress:      lipgloss.AdaptiveColor{Light: "#5B9BD5", Dark: "#ADD8E6"}, // LightBlue
		Pending:         lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"}, // Grey
	}
}

// PaletteByName returns the palette for the given theme name.
// Falls back to FormaePalette for unknown names.
func PaletteByName(name string) Palette {
	switch name {
	case "classic":
		return ClassicPalette()
	case "formae":
		return FormaePalette()
	default:
		return FormaePalette()
	}
}
