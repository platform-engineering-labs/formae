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
)

// hasDarkBackground is a seam for tests: it wraps termenv.HasDarkBackground
// so test code can override it and make Render deterministic in headless
// environments.  The default implementation is safe to call in any context.
//
//nolint:gochecknoglobals
var hasDarkBackground = func() bool {
	// termenv.HasDarkBackground() emits an OSC terminal query to detect the
	// background color. Under tmux/screen/ssh the reply is not passed through
	// and instead leaks as literal text to the shell after the program exits.
	// Skip the query in those environments and assume dark (the safer default).
	if os.Getenv("TMUX") != "" ||
		strings.HasPrefix(os.Getenv("TERM"), "screen") ||
		os.Getenv("SSH_TTY") != "" ||
		os.Getenv("SSH_CONNECTION") != "" {
		return true
	}
	return termenv.HasDarkBackground()
}

// encodeKittyFn and encodeITerm2Fn are seams for the graphics encoders so
// tests can force the empty-return (error) path and verify the fallback chain.
//
//nolint:gochecknoglobals
var encodeKittyFn = func(dark bool, cols int) string {
	return encodeKitty(dark, cols)
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
		// Kitty path: C=1 keeps cursor at image top-left; we then write
		// selectable terminal text to the right using CHA (\x1b[<N>G).
		img := encodeKittyFn(dark, graphicsFullCols)
		if img != "" {
			nameLine, verLine := wordmarkLines(version)
			var b strings.Builder
			b.WriteString(img)
			// "formae" on the image's top row at absolute column graphicsTextCol (1-based CHA).
			fmt.Fprintf(&b, "\x1b[%dG%s", graphicsTextCol, nameLine)
			// version one row down, same column.
			fmt.Fprintf(&b, "\r\x1b[1B\x1b[%dG%s", graphicsTextCol, verLine)
			// Move cursor below the image so following output doesn't overlap it.
			fmt.Fprintf(&b, "\r\x1b[%dB", graphicsImageRows-1)
			return b.String(), graphicsImageRows
		}
		// Fall through to braille on encoder failure.
		art = renderBraille(dark, brailleWidthFull)
		if art != "" {
			composed := lipgloss.JoinHorizontal(lipgloss.Center, art, wordmark)
			art = lipgloss.NewStyle().MarginTop(1).MarginLeft(3).Render(composed)
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
			composed := lipgloss.JoinHorizontal(lipgloss.Center, art, wordmark)
			art = lipgloss.NewStyle().MarginTop(1).MarginLeft(3).Render(composed)
			rows = countRows(art)
			return art, rows
		}
		return "formae v" + version, 1
	default: // CapBraille
		art = renderBraille(dark, brailleWidthFull)
		composed := lipgloss.JoinHorizontal(lipgloss.Center, art, wordmark)
		art = lipgloss.NewStyle().MarginTop(1).MarginLeft(3).Render(composed)
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
