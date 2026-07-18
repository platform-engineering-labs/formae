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

// ── Graphics-terminal tuning constants ────────────────────────────────────────
// These control the image cell grid and wordmark placement for Kitty/iTerm2.
// Adjust these values to tune the layout on your specific graphics terminal.
//
//	graphicsCols          — image width in character cells (e.g. 8).
//	graphicsRows          — image height in character cells (e.g. 4).
//	                        Terminal cells are ~2:1 tall:wide, so for a square
//	                        logo graphicsCols ≈ 2*graphicsRows is a good start.
//	graphicsWordmarkGap   — blank columns between the right edge of the image
//	                        and the left edge of the wordmark text (Kitty only).
//
// ─────────────────────────────────────────────────────────────────────────────
const (
	graphicsCols        = 8 // image width in cells (tune for graphics terminals)
	graphicsRows        = 4 // image height in cells (tune for graphics terminals)
	graphicsWordmarkGap = 2 // gap columns between image and wordmark (tune; Kitty only)
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
var encodeKittyFn = func(dark bool, cols, rows int) string {
	return encodeKitty(dark, cols, rows)
}

//nolint:gochecknoglobals
var encodeITerm2Fn = func(dark bool, cols, rows int) string {
	return encodeITerm2(dark, cols, rows)
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

// wordmarkLines returns the two styled wordmark lines as separate strings
// (no margin, no join) so callers can position them individually via cursor
// escape sequences.
//
//	line0 → "formae" in white (#FFFFFF)
//	line1 → "v{version}" in brand orange (#FF8201)
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

	wordmark := wordmarkStyle(version)

	// SizeFull: use the native renderer for the capability.
	// Graphics encoders return "" on image decode/encode error; in that case
	// we degrade gracefully: graphics → braille → text (never an empty string).
	//
	// Offset note: the 1-line top margin + 3-space left indent below are
	// user-tunable D2 live items.
	switch cap {
	case CapKitty:
		art = encodeKittyFn(dark, graphicsCols, graphicsRows)
		if art != "" {
			// Kitty: C=1 keeps the cursor at the image origin after display,
			// enabling deterministic right-of-image wordmark placement.
			art = composeKittyWordmark(art, graphicsCols, graphicsRows, version)
			rows = graphicsRows
			return art, rows
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
		art = encodeITerm2Fn(dark, graphicsCols, graphicsRows)
		if art != "" {
			// iTerm2 has no C=1 equivalent and moves the cursor unpredictably
			// after inline images. Place the wordmark BELOW the image instead.
			// Right-of-image for iTerm2 is a separate live-tune if needed.
			art = composeITerm2Wordmark(art, version)
			rows = graphicsRows + 2 // image rows + 2 wordmark lines below
			return art, rows
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

// composeKittyWordmark places the wordmark to the RIGHT of a Kitty graphics
// image using the C=1 (cursor-does-not-move) parameter. Because C=1 keeps the
// cursor at the image's top-left origin after display, every subsequent move is
// deterministic — no DEC save/restore needed.
//
// Layout (cursor starts at origin = top-left of image cell):
//
//	img (C=1)     — emitted; cursor stays at origin
//	↓ mid rows    — CUD to the vertical midpoint
//	→ gCols+gap   — CUF past image + gap
//	"formae"      — name line; then back UP to origin row, CR
//	↓ mid+1 rows  — CUD to version row
//	→ gCols+gap   — CUF past image + gap
//	"v{version}"  — version line; then back UP to origin row, CR
//	↓ gRows rows  — CUD past the full image height
//
// Guard: \x1b[0A and \x1b[0B are treated by terminals as \x1b[1A / \x1b[1B,
// which would be wrong. When mid==0 the up/down moves are omitted entirely.
func composeKittyWordmark(img string, gCols, gRows int, version string) string {
	nameLine, verLine := wordmarkLines(version)

	// Vertical middle of the image: place name on row mid, version on mid+1.
	mid := gRows / 2

	var b strings.Builder
	// Kitty C=1: cursor stays at origin (top-left) after image display.
	b.WriteString(img)

	// Line 1: "formae" — go down to mid row, right past image+gap, print,
	// then return up to origin row + carriage return.
	if mid > 0 {
		fmt.Fprintf(&b, "\x1b[%dB", mid)
	}
	fmt.Fprintf(&b, "\x1b[%dC", gCols+graphicsWordmarkGap)
	b.WriteString(nameLine)
	if mid > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", mid)
	}
	b.WriteByte('\r')

	// Line 2: "v{version}" — go down to mid+1 row, right past image+gap, print,
	// then return up to origin row + carriage return.
	midPlus1 := mid + 1
	fmt.Fprintf(&b, "\x1b[%dB", midPlus1)
	fmt.Fprintf(&b, "\x1b[%dC", gCols+graphicsWordmarkGap)
	b.WriteString(verLine)
	fmt.Fprintf(&b, "\x1b[%dA", midPlus1)
	b.WriteByte('\r')

	// Move cursor below the full image so subsequent output is below the logo.
	fmt.Fprintf(&b, "\x1b[%dB", gRows)

	return b.String()
}

// composeITerm2Wordmark places the wordmark BELOW the iTerm2 inline image.
// iTerm2 has no equivalent of Kitty's C=1 parameter; after displaying an inline
// image it moves the cursor to an unpredictable position. Placing the wordmark
// below (on newlines) is safe and reliable. Right-of-image positioning for
// iTerm2 can be revisited as a separate live-tune if needed.
func composeITerm2Wordmark(img, version string) string {
	nameLine, verLine := wordmarkLines(version)
	return img + "\n" + nameLine + "\n" + verLine
}
