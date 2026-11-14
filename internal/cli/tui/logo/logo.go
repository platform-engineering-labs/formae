// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package logo provides brand asset embedding and terminal capability
// detection for rendering the Formae logo in CLI output.
package logo

import (
	_ "embed"
	"fmt"
	"image/color"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// Capability represents the rendering mode the terminal supports.
type Capability int

const (
	// CapText is the plain-text fallback ("formae v{version}").
	CapText Capability = iota
	// CapBraille renders the propeller icon as braille art in brand orange.
	CapBraille
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

// HasDarkBackground reports whether the terminal background is dark, issuing no
// terminal query of its own. A fresh OSC 11 query (termenv) sends a
// cursor-position (CPR) sentinel whose reply is not reliably drained under
// tmux/ssh, where the leftover bytes leak into the shell as literal text (e.g.
// "…2c2c/3434;1R"), and a query sent after the graphics probe has disturbed the
// terminal can mis-detect LIGHT, which renders AdaptiveColor body text unreadable.
//
// Detection order:
//  1. FORMAE_APPEARANCE=light|dark — explicit user override (leak-free).
//  2. In an interactive terminal, including over ssh: the live OSC-11
//     background answer that Bubble Tea's package init already obtained through
//     lipgloss (so it follows appearance switches; termenv falls back to
//     COLORFGBG, then dark, if the terminal does not answer). It is read once at
//     startup, before any TUI/graphics probe, and memoized so per-frame callers
//     (e.g. the header icon) never re-read.
//  3. Under tmux (the multiplexer, not the user's terminal, would answer) and
//     off a TTY (piped/CI/dumb): COLORFGBG when the terminal exports it
//     ("fg;bg"; a trailing field of 7 or 15 means a light background),
//     otherwise dark.
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
//  3. auto-detect — the live OSC-11 answer in an interactive terminal outside
//     tmux, otherwise COLORFGBG, otherwise dark (see autoDetectDarkBackground).
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

// autoDetectDarkBackground is layer 3 of ResolveDarkBackground.
//
// In a direct interactive terminal it uses the terminal's live background colour
// (OSC 11 via lipgloss/termenv, which itself falls back to COLORFGBG when the
// terminal does not answer). COLORFGBG must not win there: terminals compute it
// once when the session starts, so it goes stale when the colours change later.
// iTerm2 is the common case: with a profile that has separate light and dark
// colours, a tab opened in dark mode keeps COLORFGBG=15;0 after macOS switches
// to light (and vice versa), which drew the logo and the huh prompts in the
// wrong palette.
//
// ssh is not excluded: Bubble Tea's package init has already sent the OSC 11
// query over the session (termenv only skips TERM=screen*/tmux*/dumb and
// non-TTYs) and read the reply with its own timeout, falling back to COLORFGBG
// and then black itself, so its answer is never worse than ours. Under tmux the
// multiplexer, not the user's terminal, would answer; there, and where there is
// nothing to query (non-TTY, CI, dumb), COLORFGBG is the only signal, and
// without it we assume dark. The query result is memoized
// so per-frame callers never re-query; the first call happens in cli.Start,
// before the banner's graphics probe touches the terminal.
func autoDetectDarkBackground() bool {
	env := terminalEnv()
	if env.IsTTY && !env.Dumb && !env.CI && !env.Tmux {
		darkBgOnce.Do(func() { darkBgQueried = queryDarkBackground() })
		return darkBgQueried
	}
	if fgbg := os.Getenv("COLORFGBG"); fgbg != "" {
		parts := strings.Split(fgbg, ";")
		bg := parts[len(parts)-1]
		return bg != "7" && bg != "15"
	}
	return true
}

//nolint:gochecknoglobals
var (
	darkBgOnce    sync.Once
	darkBgQueried bool

	// terminalEnv (the environment signals, shared with Detect) and
	// queryDarkBackground are seams so tests can drive the live-query branch
	// without a real terminal.
	//
	// queryDarkBackground reads lipgloss's default renderer rather than calling
	// termenv directly: Bubble Tea's package init (tea_init.go, v1) already makes
	// that renderer send the OSC-11 query and caches the answer, so reading it
	// here costs no second round trip. Without that init the renderer sends the
	// query itself on this first read (lipgloss Renderer.HasDarkBackground only
	// skips it once a value was set explicitly). The first read happens in
	// cli.Start, before lipgloss.SetHasDarkBackground replaces the cached value
	// with our decision.
	terminalEnv         = gatherEnv
	queryDarkBackground = lipgloss.HasDarkBackground
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

// encodeKittyFn is a seam for the graphics encoder so tests can force the
// empty-return (error) path and verify the fallback chain.
//
//nolint:gochecknoglobals
var encodeKittyFn = func(dark bool, widthPx int, tint color.Color) string {
	return encodeKittyFullLogo(dark, widthPx, tint)
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

// renderCfg holds optional overrides applied via RenderOption.
type renderCfg struct {
	wordmark lipgloss.TerminalColor // overrides the braille wordmark letter color
}

// RenderOption customizes Render.
type RenderOption func(*renderCfg)

// WithWordmarkColor overrides the color of the logo's "formae" letters (the
// propeller stays brand orange). Used to color the wordmark from the active
// theme. Applies to the braille renderer and, by tinting the letter pixels, to
// the Kitty graphics image.
func WithWordmarkColor(c lipgloss.TerminalColor) RenderOption {
	return func(cfg *renderCfg) { cfg.wordmark = c }
}

// wordmarkTintColor resolves the wordmark override to a concrete color for the
// detected background, for tinting the graphics-logo letters. It resolves the
// light/dark side explicitly (rather than via lipgloss AdaptiveColor, which
// would trigger a per-render background query that leaks as text under
// tmux/screen), matching how the rest of this package handles color. Returns nil
// when no override is set or the value can't be parsed (leaving the image
// untinted).
func wordmarkTintColor(c lipgloss.TerminalColor, dark bool) color.Color {
	var hex string
	switch v := c.(type) {
	case nil:
		return nil
	case lipgloss.AdaptiveColor:
		if dark {
			hex = v.Dark
		} else {
			hex = v.Light
		}
	case lipgloss.Color:
		hex = string(v)
	default:
		return nil
	}
	rgba, ok := parseHexColor(hex)
	if !ok {
		return nil
	}
	return rgba
}

// parseHexColor parses a "#RRGGBB" string into an opaque color. Returns ok=false
// for any other shape (empty, wrong length, non-hex).
func parseHexColor(s string) (color.RGBA, bool) {
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{}, false
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(s[1:], "%02x%02x%02x", &r, &g, &b); err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{R: r, G: g, B: b, A: 0xFF}, true
}

// Render produces the logo art string and the number of terminal rows it
// occupies, based on the detected terminal capability and the requested size.
//
// Dispatch rules:
//   - SizeNone → ("", 0) — always suppressed.
//   - CapText (any size) → "formae v{version}", rows=1.
//   - SizeCompact → braille ALWAYS, even for CapKitty.
//     Graphics protocols do not survive bubbletea alt-screen repaints;
//     the compact in-TUI header must use braille only (coexistence rule).
//   - SizeFull + CapKitty → Kitty APC graphics escape (C=1) + selectable
//     terminal text to the right, positioned with CHA (\x1b[<N>G).
//   - SizeFull + CapBraille → braille art at full width.
//
// rows counts terminal lines consumed: braille = newlines+1; text = 1;
// graphics = graphicsFullLogoImageRows (the pinned cell footprint).
func Render(cap Capability, size Size, version string, opts ...RenderOption) (art string, rows int) {
	var cfg renderCfg
	for _, o := range opts {
		o(&cfg)
	}

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
		img := encodeKittyFn(dark, graphicsFullLogoWidthPx, wordmarkTintColor(cfg.wordmark, dark))
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
		if full := renderFullLogoBraille(dark, brailleFullLogoWidth, cfg.wordmark); full != "" {
			ver := lipgloss.NewStyle().Foreground(lipgloss.Color(versionColor)).MarginLeft(2).Render("v" + version)
			art = lipgloss.JoinHorizontal(lipgloss.Bottom, full, ver)
			rows = countRows(art)
			return art, rows
		}
		return "formae v" + version, 1
	default: // CapBraille
		// Full-wordmark braille: white "formae" letters + orange propeller, with
		// the version set to its right, vertically centered against the wordmark
		// (mirrors the old logo's wordmark-to-the-right placement).
		if full := renderFullLogoBraille(dark, brailleFullLogoWidth, cfg.wordmark); full != "" {
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
