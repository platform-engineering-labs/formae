// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package logo provides brand asset embedding and terminal capability
// detection for rendering the Formae logo in CLI output.
package logo

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	brailleWidthFull    = 6 // ~6 braille cols for SizeFull (shrunk to match ~5-col graphics logo)
	brailleWidthCompact = 3 // ~3 braille cols for SizeCompact
	// brailleFullLogoWidth is the width (in braille cols) for the full-wordmark
	// braille renderer (white "formae" letters + orange propeller). Wider than
	// the propeller-only width because it spans the whole ~3.8:1 wordmark.
	brailleFullLogoWidth = 44
	// versionColor is the fixed hex for the "v{version}" string beside the
	// wordmark — light grey so it reads as secondary metadata next to the
	// bright-white letters (mirrors palette InProgress #AAAAAA; not AdaptiveColor
	// to avoid the OSC background query that leaks under tmux/ssh). Set to
	// "#E8E8E8" for off-white or "#FFFFFF" for pure white.
	versionColor = "#AAAAAA"
)

// hasDarkBackground is a seam for tests: it wraps termenv.HasDarkBackground
// so test code can override it and make Render deterministic in headless
// environments.  The default implementation is safe to call in any context.
//
//nolint:gochecknoglobals
var hasDarkBackground = func() bool {
	// We deliberately do NOT call termenv.HasDarkBackground(): it emits an OSC 11
	// background-color query plus a cursor-position (CPR) sentinel, and the reply
	// is not reliably drained — the leftover bytes leak into the shell as literal
	// text after the program exits (e.g. "…2c2c/3434;1R"). This happens under
	// tmux/screen/ssh AND in some terminals (e.g. Kitty) directly.
	//
	// Instead, honor COLORFGBG when the terminal exports it (no query needed):
	// it is "fg;bg" where the trailing field is the background color index; 7 and
	// 15 are light. Everything else assumes dark (the safe, common default; the
	// dark logo's white letters would vanish on a light background, but that only
	// affects light terminals that don't export COLORFGBG).
	if fgbg := os.Getenv("COLORFGBG"); fgbg != "" {
		parts := strings.Split(fgbg, ";")
		if bg := parts[len(parts)-1]; bg == "7" || bg == "15" {
			return false
		}
	}
	return true
}

// encodeKittyFn and encodeITerm2Fn are seams for the graphics encoders so
// tests can force the empty-return (error) path and verify the fallback chain.
//
//nolint:gochecknoglobals
var encodeKittyFn = func(dark bool, widthPx int) string {
	return encodeKittyFullLogo(dark, widthPx)
}

//nolint:gochecknoglobals
var encodeITerm2Fn = func(dark bool, cols int) string {
	return encodeITerm2(dark, cols)
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

// wordmarkStyle builds a two-line styled wordmark block:
//
//	formae      (white)
//	v{version}  (brand orange)
//
// Fixed colors are used (no AdaptiveColor) to avoid OSC terminal queries that
// leak as literal text in multiplexed sessions (tmux/screen/ssh).
//
// The two lines are joined with JoinVertical so that a MarginLeft applied to
// the outer block shifts BOTH lines equally — string concatenation with "\n"
// only pads the first line when used as a JoinHorizontal argument.
func wordmarkStyle(version string) string {
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	versionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandOrange))
	block := lipgloss.JoinVertical(lipgloss.Left, nameStyle.Render("formae"), versionStyle.Render("v"+version))
	return lipgloss.NewStyle().MarginLeft(2).Render(block)
}

// wordmarkLines returns the two raw ANSI-styled lines used for graphics
// text-right composition: nameLine ("formae" in white) and verLine
// ("v{version}" in brand orange). These are plain strings with ANSI color
// codes, suitable for direct terminal output via CHA positioning.
func wordmarkLines(version string) (nameLine, verLine string) {
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	versionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandOrange))
	return nameStyle.Render("formae"), versionStyle.Render("v" + version)
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
//   - SizeFull + CapKitty → Kitty APC graphics escape (C=1) + selectable
//     terminal text to the right, positioned with CHA (\x1b[<N>G).
//   - SizeFull + CapITerm2 → iTerm2 inline-image escape + wordmark below.
//   - SizeFull + CapBraille → braille art at full width.
//
// rows counts terminal lines consumed: braille = newlines+1; text = 1;
// graphics = graphicsImageRows (calibrated constant).
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

	wordmark := wordmarkStyle(version)

	// SizeFull: use the native renderer for the capability.
	// Graphics encoders return "" on image decode/encode error; in that case
	// we degrade gracefully: graphics → braille → text (never an empty string).
	switch cap {
	case CapKitty:
		// Kitty path: the FULL wordmark is rendered as one image (C=1 keeps the
		// cursor at the image top-left). The image already contains "formae", so
		// we place only the version to its right on the image's bottom row
		// (baseline-aligned), positioned with CHA + a real newline (the \x1b[1B
		// CUD escape is unreliable in Kitty after a C=1 image).
		img := encodeKittyFn(dark, graphicsFullLogoWidthPx)
		if img != "" {
			ver := lipgloss.NewStyle().Foreground(lipgloss.Color(versionColor)).Render("v" + version)
			var b strings.Builder
			b.WriteString(img)
			// Drop to the version's row, position it past the image, then advance
			// the rest of the way below the image so logs don't overlap it.
			b.WriteString(strings.Repeat("\n", graphicsFullLogoVersionRow))
			fmt.Fprintf(&b, "\x1b[%dG%s", graphicsFullLogoTextCol, ver)
			b.WriteString(strings.Repeat("\n", graphicsFullLogoImageRows-graphicsFullLogoVersionRow))
			return b.String(), graphicsFullLogoImageRows
		}
		// Fall through to full-logo braille on encoder failure.
		if full := renderFullLogoBraille(dark, brailleFullLogoWidth); full != "" {
			ver := lipgloss.NewStyle().Foreground(lipgloss.Color(versionColor)).MarginLeft(2).Render("v" + version)
			art = lipgloss.JoinHorizontal(lipgloss.Bottom, full, ver)
			rows = countRows(art)
			return art, rows
		}
		return "formae v" + version, 1
	case CapITerm2:
		// iTerm2 does not support C=1; wordmark is placed BELOW the image.
		// (Text-right composition for iTerm2 is a separate tuning effort.)
		img := encodeITerm2Fn(dark, graphicsFullCols)
		if img != "" {
			nameLine, verLine := wordmarkLines(version)
			art = img + "\n" + nameLine + "\n" + verLine
			return art, graphicsImageRows
		}
		// Fall through to braille on encoder failure.
		art = renderBraille(dark, brailleWidthFull)
		if art != "" {
			art = lipgloss.JoinHorizontal(lipgloss.Center, art, wordmark)
			rows = countRows(art)
			return art, rows
		}
		return "formae v" + version, 1
	default: // CapBraille
		// Full-wordmark braille: white "formae" letters + orange propeller, with
		// the version set to its right, vertically centered against the wordmark
		// (mirrors the old logo's wordmark-to-the-right placement).
		if full := renderFullLogoBraille(dark, brailleFullLogoWidth); full != "" {
			ver := lipgloss.NewStyle().
				Foreground(lipgloss.Color(versionColor)).
				MarginLeft(2).
				Render("v" + version)
			art = lipgloss.JoinHorizontal(lipgloss.Bottom, full, ver)
			rows = countRows(art)
			return art, rows
		}
		// Fall back to the propeller-only braille + text wordmark.
		art = renderBraille(dark, brailleWidthFull)
		art = lipgloss.JoinHorizontal(lipgloss.Center, art, wordmark)
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
