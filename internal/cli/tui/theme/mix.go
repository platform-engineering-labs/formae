// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"math"
	"strconv"
	"strings"
)

// bandMix is how far the cursor-row selection band is blended from the
// theme's background towards a candidate tint.
const bandMix = 0.22

// minBandText is the WCAG contrast floor the selection band must clear
// against the row's foreground text — the enforced readability invariant.
const minBandText = 4.5

// minBandVisible is the preferred (but conceded) WCAG contrast floor the
// selection band should clear against the background, so the band is
// visible as well as readable.
const minBandVisible = 1.10

// hexRGB parses a CSS-style hex color, "#rgb" or "#rrggbb", case-insensitive
// and with surrounding whitespace trimmed. Anything else — including
// termenv's bare ANSI index form (e.g. "4") — is "no color": ok is false and
// r, g, b are zero.
func hexRGB(s string) (r, g, b uint8, ok bool) {
	s = strings.TrimSpace(s)
	if len(s) == 0 || s[0] != '#' {
		return 0, 0, 0, false
	}
	hex := s[1:]

	expand := func(c byte) (byte, byte) { return c, c }
	var rr, gg, bb string
	switch len(hex) {
	case 3:
		r0, r1 := expand(hex[0])
		g0, g1 := expand(hex[1])
		b0, b1 := expand(hex[2])
		rr, gg, bb = string([]byte{r0, r1}), string([]byte{g0, g1}), string([]byte{b0, b1})
	case 6:
		rr, gg, bb = hex[0:2], hex[2:4], hex[4:6]
	default:
		return 0, 0, 0, false
	}

	rv, err := strconv.ParseUint(rr, 16, 8)
	if err != nil {
		return 0, 0, 0, false
	}
	gv, err := strconv.ParseUint(gg, 16, 8)
	if err != nil {
		return 0, 0, 0, false
	}
	bv, err := strconv.ParseUint(bb, 16, 8)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8(rv), uint8(gv), uint8(bv), true
}

// mix blends a towards b by t in sRGB space, rounding each channel half
// away from zero, and returns the result as a lowercase "#rrggbb" string.
// It returns "" if either color is unparseable. t is expected to lie in
// [0,1] (a package constant at its single call site) and is not validated.
func mix(a, b string, t float64) string {
	ar, ag, ab, ok := hexRGB(a)
	if !ok {
		return ""
	}
	br, bg, bb, ok := hexRGB(b)
	if !ok {
		return ""
	}

	blend := func(x, y uint8) uint8 {
		v := float64(x) + (float64(y)-float64(x))*t
		return uint8(math.Round(v))
	}

	r, g, bl := blend(ar, br), blend(ag, bg), blend(ab, bb)
	const hexDigits = "0123456789abcdef"
	buf := [7]byte{'#'}
	buf[1] = hexDigits[r>>4]
	buf[2] = hexDigits[r&0xf]
	buf[3] = hexDigits[g>>4]
	buf[4] = hexDigits[g&0xf]
	buf[5] = hexDigits[bl>>4]
	buf[6] = hexDigits[bl&0xf]
	return string(buf[:])
}

// relativeLuminance computes the WCAG 2.x relative luminance of an sRGB
// channel triplet.
func relativeLuminance(r, g, b uint8) float64 {
	linearize := func(c uint8) float64 {
		v := float64(c) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*linearize(r) + 0.7152*linearize(g) + 0.0722*linearize(b)
}

// contrast returns the WCAG 2.x contrast ratio between a and b, in [1,21].
// It returns 0 when either color is unparseable, so every ">= floor"
// comparison against it fails closed.
func contrast(a, b string) float64 {
	ar, ag, ab, ok := hexRGB(a)
	if !ok {
		return 0
	}
	br, bg, bb, ok := hexRGB(b)
	if !ok {
		return 0
	}

	la := relativeLuminance(ar, ag, ab)
	lb := relativeLuminance(br, bg, bb)
	lmax, lmin := la, lb
	if lmin > lmax {
		lmax, lmin = lmin, lmax
	}
	return (lmax + 0.05) / (lmin + 0.05)
}

// luminanceOrZero is relativeLuminance for a hex color string, treating an
// unparseable color as luminance 0 (black) so awayFromText/towardsText still
// return a defined extreme rather than an arbitrary one.
func luminanceOrZero(hex string) float64 {
	r, g, b, ok := hexRGB(hex)
	if !ok {
		return 0
	}
	return relativeLuminance(r, g, b)
}

// awayFromText returns the extreme, "#000000" or "#ffffff", whose WCAG
// relative luminance lies on the opposite side of bg from fg: "#000000"
// when fg is at least as light as bg, else "#ffffff".
func awayFromText(bg, fg string) string {
	if luminanceOrZero(fg) >= luminanceOrZero(bg) {
		return "#000000"
	}
	return "#ffffff"
}

// towardsText returns the extreme opposite of awayFromText.
func towardsText(bg, fg string) string {
	if awayFromText(bg, fg) == "#000000" {
		return "#ffffff"
	}
	return "#000000"
}

// selectionBand derives the cursor-row selection band from the theme's own
// background, guarded by a contrast check against the foreground text that
// keeps its own color on that row. It tries, in order: the background tinted
// with the theme's accent; a neutral background mix moved away from the
// text; and a neutral background mix moved towards the text (for a
// background already at an extreme, where "away" has nowhere left to go).
// The first candidate that is both readable and visible wins; failing that,
// the first that is at least readable; failing that, whichever candidate is
// most readable, so the band and the background always come from the same
// theme.
func selectionBand(bg, fg, accent string) string {
	candidates := []string{
		mix(bg, accent, bandMix),               // 1. tinted with the theme's own accent
		mix(bg, awayFromText(bg, fg), bandMix), // 2. neutral, moved away from the text
		mix(bg, towardsText(bg, fg), bandMix),  // 3. for a background already at an extreme
	}
	// readable and visible
	for _, c := range candidates {
		if contrast(c, fg) >= minBandText && contrast(c, bg) >= minBandVisible {
			return c
		}
	}
	// readable — the enforced invariant; a weak band beats unreadable text
	for _, c := range candidates {
		if contrast(c, fg) >= minBandText {
			return c
		}
	}
	// best available, so the band and the background always come from the same theme
	return maxContrastAgainst(fg, candidates)
}

// maxContrastAgainst returns whichever candidate has the highest contrast
// against fg. Ties (including an all-unparseable candidate list, where every
// contrast is 0) keep the first candidate.
func maxContrastAgainst(fg string, candidates []string) string {
	best := ""
	bestContrast := -1.0
	for _, c := range candidates {
		if cc := contrast(c, fg); cc > bestContrast {
			best, bestContrast = c, cc
		}
	}
	return best
}
