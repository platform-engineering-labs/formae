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
	"sync"

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
// HasDarkBackground reports whether the terminal background is dark WITHOUT
// emitting a terminal query. termenv.HasDarkBackground() sends an OSC 11
// background-color query plus a cursor-position (CPR) sentinel whose reply is
// not reliably drained: the leftover bytes leak into the shell as literal text
// (e.g. "…2c2c/3434;1R"), and the query also mis-detects — defaulting to LIGHT —
// once the graphics probe has disturbed the terminal, which renders AdaptiveColor
// body text unreadable (dark-on-dark).
//
// Detection order:
//  1. FORMAE_APPEARANCE=light|dark — explicit user override (leak-free).
//  2. COLORFGBG when the terminal exports it ("fg;bg"; a trailing field of 7 or
//     15 means a light background) — leak-free.
//  3. Auto-detect via an OSC-11 background-color query, but ONLY in a direct
//     interactive terminal. Under tmux/screen/ssh the query's reply is not
//     reliably drained and leaks into the shell as literal text, and off a TTY
//     (piped/CI) there is nothing to query — so those all assume dark. The query
//     result is memoized so per-frame callers (e.g. the header icon) never
//     re-query, and the seed happens once at startup before any TUI/graphics
//     probe disturbs the terminal.
//
// Exported so the CLI can seed lipgloss's global AdaptiveColor resolution once at
// startup (lipgloss.SetHasDarkBackground). It carries no config layer, so it is
// the right entry point for per-frame callers with no config in scope; commands
// that have loaded a profile should re-seed via ResolveDarkBackground so the
// cli.appearance setting takes effect.
func HasDarkBackground() bool {
	return ResolveDarkBackground("")
}

// ResolveDarkBackground reports whether to treat the terminal background as dark,
// applying the full appearance precedence:
//
//  1. FORMAE_APPEARANCE env (light|dark) — explicit user override.
//  2. appearance — the cli.appearance profile setting (light|dark|auto). A
//     light/dark value wins over auto-detection; "auto" (and empty/unknown)
//     defer to it.
//  3. auto-detect — COLORFGBG, then an OSC-11 query in a direct interactive
//     terminal (see autoDetectDarkBackground).
//
// The env layer sits above config because an env override is a per-invocation
// escape hatch (e.g. forcing light in a screenshot), whereas the config value is
// the persistent default.
func ResolveDarkBackground(appearance string) bool {
	if dark, ok := parseAppearance(os.Getenv("FORMAE_APPEARANCE")); ok {
		return dark
	}
	if dark, ok := parseAppearance(appearance); ok {
		return dark
	}
	return autoDetectDarkBackground()
}

// parseAppearance maps an appearance string to its dark-background boolean. ok is
// false for "auto", "", or any unrecognized value — those defer to the next layer
// in the precedence chain.
func parseAppearance(s string) (dark bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "light":
		return false, true
	case "dark":
		return true, true
	default:
		return false, false
	}
}

// autoDetectDarkBackground is layer 3 of ResolveDarkBackground: COLORFGBG when the
// terminal exports it, otherwise an OSC-11 query gated to a direct interactive
// terminal (tmux/screen/ssh and non-TTY contexts assume dark to avoid a leaking,
// mis-detecting query). The query result is memoized so per-frame callers never
// re-query.
func autoDetectDarkBackground() bool {
	if fgbg := os.Getenv("COLORFGBG"); fgbg != "" {
		parts := strings.Split(fgbg, ";")
		bg := parts[len(parts)-1]
		return bg != "7" && bg != "15"
	}
	env := gatherEnv()
	if !env.IsTTY || env.Tmux || env.SSH || env.Dumb || env.CI {
		return true
	}
	darkBgOnce.Do(func() { darkBgQueried = termenv.HasDarkBackground() })
	return darkBgQueried
}

//nolint:gochecknoglobals
var (
	darkBgOnce    sync.Once
	darkBgQueried bool
)

// hasDarkBackground is a seam for tests; it defaults to HasDarkBackground so
// test code can override background detection deterministically.
//
//nolint:gochecknoglobals
var hasDarkBackground = HasDarkBackground

// MiniPropellerWidth is the braille-column width of the compact header icon.
// Six columns render as three rows, which keeps the propeller recognizable
// (fewer rows squash it into a near-square blob). Braille — not the Kitty
// graphics protocol — is used in the live TUI header because an alt-screen model
// repaints every frame; an inline graphics image would flicker and leave
// artifacts. The one-shot startup banner uses Kitty graphics instead.
const MiniPropellerWidth = 6

// MiniPropellerRows is the rendered height of the header icon.
const MiniPropellerRows = 3

// MiniPropeller returns the formae propeller as a compact braille icon (brand
// orange) for embedding in a header/title bar. Always returns exactly
// MiniPropellerRows rows, each MiniPropellerWidth braille columns wide.
func MiniPropeller() []string {
	rows := strings.Split(renderBraille(hasDarkBackground(), MiniPropellerWidth), "\n")
	for len(rows) < MiniPropellerRows {
		rows = append(rows, strings.Repeat(" ", MiniPropellerWidth))
	}
	return rows[:MiniPropellerRows]
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
// The wordmark color is resolved via the package's own leak-safe
// HasDarkBackground (no AdaptiveColor, so no per-render OSC query that would
// leak as literal text under tmux/screen/ssh) — bright on dark, near-black on
// light so "formae" stays legible on light terminals.
//
// The two lines are joined with JoinVertical so that a MarginLeft applied to
// the outer block shifts BOTH lines equally — string concatenation with "\n"
// only pads the first line when used as a JoinHorizontal argument.
func wordmarkStyle(version string) string {
	nameStyle := lipgloss.NewStyle().Foreground(brandTextColor(HasDarkBackground()))
	versionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandOrange))
	block := lipgloss.JoinVertical(lipgloss.Left, nameStyle.Render("formae"), versionStyle.Render("v"+version))
	return lipgloss.NewStyle().MarginLeft(2).Render(block)
}

// brandTextColor returns the "formae" wordmark / logo-letter color for the
// detected background: bright (#E8E8E8) on dark, near-black (#1A1A1A) on light,
// mirroring the theme's TextPrimary. Takes an explicit dark flag so callers can
// pass the value they already resolved without re-querying.
func brandTextColor(dark bool) lipgloss.Color {
	if dark {
		return lipgloss.Color("#E8E8E8")
	}
	return lipgloss.Color("#1A1A1A")
}

// wordmarkLines returns the two raw ANSI-styled lines used for graphics
// text-right composition: nameLine ("formae" in white) and verLine
// ("v{version}" in brand orange). These are plain strings with ANSI color
// codes, suitable for direct terminal output via CHA positioning.
func wordmarkLines(version string) (nameLine, verLine string) {
	nameStyle := lipgloss.NewStyle().Foreground(brandTextColor(HasDarkBackground()))
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
