// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package logo provides brand asset embedding and terminal capability
// detection for rendering the Formae logo in CLI output.
package logo

import (
	_ "embed"
	"strings"

	"github.com/muesli/termenv"
)

// Capability represents the rendering mode the terminal supports.
type Capability int

const (
	// CapText is the plain-text fallback ("formae v{version}").
	CapText Capability = iota
	// CapBraille renders the propeller icon as braille art in brand orange.
	CapBraille
	// CapITerm2 renders via the iTerm2 inline-image protocol.
	CapITerm2
	// CapKitty renders via the Kitty APC graphics protocol.
	CapKitty
)

// Size controls how much vertical space the logo occupies.
type Size int

const (
	// SizeNone suppresses the logo entirely (e.g. narrow terminals).
	SizeNone Size = iota
	// SizeCompact renders a small icon prefix (braille only, even for graphics caps).
	SizeCompact
	// SizeFull renders the logo at full size using the native renderer for the cap.
	SizeFull
)

// Fixed widths (in braille character columns) for each size tier.
const (
	brailleWidthFull    = 10 // ~10 braille cols for SizeFull
	brailleWidthCompact = 3  // ~3 braille cols for SizeCompact

	// graphicsFullCols is the character-column width for SizeFull graphics.
	// At ~8 px/cell this gives ~80 px, matching a single propeller icon.
	graphicsFullCols = 10
)

// hasDarkBackground is a seam for tests: it wraps termenv.HasDarkBackground
// so test code can override it and make Render deterministic in headless
// environments.  The default implementation calls termenv directly.
//
//nolint:gochecknoglobals
var hasDarkBackground = func() bool {
	return termenv.HasDarkBackground()
}

//go:embed assets/Formae_Logo_dark.png
var logoDark []byte

//go:embed assets/Formae_Logo_light.png
var logoLight []byte

// logoBytes returns the embedded PNG bytes for the given theme variant.
func logoBytes(dark bool) []byte {
	if dark {
		return logoDark
	}
	return logoLight
}

// Render produces the logo art string and the number of terminal rows it
// occupies, based on the detected terminal capability and the requested size.
//
// Dispatch rules:
//   - SizeNone → ("", 0) — always suppressed.
//   - CapText (any size) → "formae v{version}", rows=1.
//   - SizeCompact → braille ALWAYS, even for CapKitty/CapITerm2.
//     Graphics protocols do not survive bubbletea alt-screen repaints;
//     the compact in-TUI header must use braille only (coexistence rule).
//   - SizeFull + CapKitty → Kitty APC graphics escape.
//   - SizeFull + CapITerm2 → iTerm2 inline-image escape.
//   - SizeFull + CapBraille → braille art at full width.
//
// rows counts terminal lines consumed: braille = newlines+1; text = 1;
// graphics = the pixel height divided by pixelsPerCell (see graphics.go).
func Render(cap Capability, size Size, version string) (art string, rows int) {
	// SizeNone: suppress unconditionally.
	if size == SizeNone {
		return "", 0
	}

	// CapText: plain-text fallback at any non-None size.
	if cap == CapText {
		return "formae v" + version, 1
	}

	dark := hasDarkBackground()

	// SizeCompact: braille only — coexistence rule.
	if size == SizeCompact {
		art = renderBraille(dark, brailleWidthCompact)
		rows = countRows(art)
		return art, rows
	}

	// SizeFull: use the native renderer for the capability.
	switch cap {
	case CapKitty:
		art = encodeKitty(dark, graphicsFullCols)
		// Row count: pixel height / pixelsPerCell.
		rows = graphicsRowCount(graphicsFullCols)
		return art, rows
	case CapITerm2:
		art = encodeITerm2(dark, graphicsFullCols)
		rows = graphicsRowCount(graphicsFullCols)
		return art, rows
	default: // CapBraille
		art = renderBraille(dark, brailleWidthFull)
		rows = countRows(art)
		return art, rows
	}
}

// countRows returns the number of terminal rows in art (newlines + 1).
// An empty string returns 0.
func countRows(art string) int {
	if art == "" {
		return 0
	}
	return strings.Count(art, "\n") + 1
}

// graphicsRowCount computes the terminal row count for a graphics image sized
// to cellCols character columns.  The propeller crop is square (cropW×cropH,
// both ~430 px), so the pixel height equals the pixel width.  With
// pixelsPerCell = 8 px/col the row height is also 8 px/row, giving:
//
//	rows = pixelHeight / pixelsPerCell = (cellCols * pixelsPerCell) / pixelsPerCell = cellCols
//
// For a square image rendered at graphicsFullCols cols the row count equals the
// column count.
func graphicsRowCount(cellCols int) int {
	// The propeller crop is square: height ≈ width.
	// pixelsPerCell (8 px) is the same horizontally and vertically so rows = cols.
	if cellCols <= 0 {
		return 1
	}
	return cellCols
}
