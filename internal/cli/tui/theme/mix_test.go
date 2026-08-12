//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"fmt"
	"testing"
)

func TestHexRGB(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		r, g, b uint8
		ok      bool
	}{
		{"rrggbb", "#336699", 0x33, 0x66, 0x99, true},
		{"rgb shorthand", "#369", 0x33, 0x66, 0x99, true},
		{"uppercase", "#AABBCC", 0xAA, 0xBB, 0xCC, true},
		{"uppercase shorthand", "#FA3", 0xFF, 0xAA, 0x33, true},
		{"surrounding whitespace", "  #336699  ", 0x33, 0x66, 0x99, true},
		{"black", "#000000", 0, 0, 0, true},
		{"white", "#ffffff", 255, 255, 255, true},
		{"unparseable, no hash", "336699", 0, 0, 0, false},
		{"unparseable, ansi index", "4", 0, 0, 0, false},
		{"unparseable, empty", "", 0, 0, 0, false},
		{"unparseable, wrong length", "#3366", 0, 0, 0, false},
		{"unparseable, non-hex digits", "#zzzzzz", 0, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, g, b, ok := hexRGB(c.in)
			if ok != c.ok {
				t.Fatalf("hexRGB(%q) ok = %v, want %v", c.in, ok, c.ok)
			}
			if !c.ok {
				return
			}
			if r != c.r || g != c.g || b != c.b {
				t.Errorf("hexRGB(%q) = %d,%d,%d want %d,%d,%d", c.in, r, g, b, c.r, c.g, c.b)
			}
		})
	}
}

func TestMix(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		t    float64
		want string
	}{
		{"t=0 returns a", "#000000", "#ffffff", 0, "#000000"},
		{"t=1 returns b", "#000000", "#ffffff", 1, "#ffffff"},
		{"midpoint rounds half away from zero", "#000000", "#ffffff", 0.5, "#808080"},
		{"rgb shorthand operands", "#000", "#fff", 0.5, "#808080"},
		{"uppercase operands", "#000000", "#FFFFFF", 1, "#ffffff"},
		{"whitespace operands", "  #000000  ", "  #ffffff  ", 0, "#000000"},
		{"unparseable a returns empty", "not-a-color", "#ffffff", 0.5, ""},
		{"unparseable b returns empty", "#000000", "4", 0.5, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mix(c.a, c.b, c.t)
			if got != c.want {
				t.Errorf("mix(%q, %q, %v) = %q, want %q", c.a, c.b, c.t, got, c.want)
			}
		})
	}
}

func TestContrast(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want float64
	}{
		{"black on white is max contrast", "#000000", "#ffffff", 21},
		{"white on black is max contrast", "#ffffff", "#000000", 21},
		{"color against itself is 1", "#336699", "#336699", 1},
		{"black against itself is 1", "#000000", "#000000", 1},
		{"unparseable a returns 0", "nope", "#ffffff", 0},
		{"unparseable b returns 0", "#000000", "4", 0},
		{"both unparseable returns 0", "nope", "also-nope", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := contrast(c.a, c.b)
			if !almostEqual(got, c.want, 1e-9) {
				t.Errorf("contrast(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestAwayFromText(t *testing.T) {
	cases := []struct {
		name   string
		bg, fg string
		want   string
	}{
		{"dark background, light text -> black", "#000000", "#ffffff", "#000000"},
		{"light background, dark text -> white", "#ffffff", "#000000", "#ffffff"},
		{"equal luminance -> black (fg not darker than bg)", "#808080", "#808080", "#000000"},
		{"unparseable background is defined", "nope", "#ffffff", "#000000"},
		{"unparseable foreground is defined", "#000000", "nope", "#000000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := awayFromText(c.bg, c.fg)
			if got != c.want {
				t.Errorf("awayFromText(%q, %q) = %q, want %q", c.bg, c.fg, got, c.want)
			}
		})
	}
}

func TestTowardsText(t *testing.T) {
	cases := []struct {
		name   string
		bg, fg string
		want   string
	}{
		{"dark background, light text -> white", "#000000", "#ffffff", "#ffffff"},
		{"light background, dark text -> black", "#ffffff", "#000000", "#000000"},
		{"equal luminance -> white (the other extreme)", "#808080", "#808080", "#ffffff"},
		{"unparseable background is defined", "nope", "#ffffff", "#ffffff"},
		{"unparseable foreground is defined", "#000000", "nope", "#ffffff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := towardsText(c.bg, c.fg)
			if got != c.want {
				t.Errorf("towardsText(%q, %q) = %q, want %q", c.bg, c.fg, got, c.want)
			}
		})
	}
}

// almostEqual reports whether a and b differ by no more than eps, guarding
// the WCAG contrast assertions against floating-point rounding.
func almostEqual(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func TestSelectionBand(t *testing.T) {
	cases := []struct {
		name           string
		bg, fg, accent string
		want           string
	}{
		// The accent tint is the first candidate and wins outright once it
		// clears both floors — these are the built-in themes' own colors, so
		// the derived band must match the design's committed value exactly.
		{"accent tint wins on a dark background (nord)", "#2e3440", "#d8dee9", "#81a1c1", "#404c5c"},
		{"accent tint wins on a light background (rose-pine)", "#faf4ed", "#575279", "#56949f", "#d6dfdc"},

		{"malformed background yields no band", "not-a-color", "#c0caf5", "#7aa2f7", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := selectionBand(c.bg, c.fg, c.accent)
			if got != c.want {
				t.Errorf("selectionBand(%q, %q, %q) = %q, want %q", c.bg, c.fg, c.accent, got, c.want)
			}
		})
	}
}

// TestSelectionBand_MalformedForegroundFallsBackToAccentTint covers a
// foreground that fails to parse: contrast against it is always 0 (fails
// closed), so both readability loops reject every candidate and
// maxContrastAgainst ties at 0 across all three — its documented first-wins
// tie-break returns the first candidate, the accent tint.
func TestSelectionBand_MalformedForegroundFallsBackToAccentTint(t *testing.T) {
	const bg, accent = "#1a1b26", "#7aa2f7"
	want := mix(bg, accent, bandMix)
	if want == "" {
		t.Fatal("test setup: accent tint must be a valid color")
	}
	if got := selectionBand(bg, "not-a-color", accent); got != want {
		t.Errorf("selectionBand(%q, %q, %q) = %q, want the accent tint %q", bg, "not-a-color", accent, got, want)
	}
}

// TestSelectionBand_AccentEqualsForegroundStillClearsBothFloors covers the
// adversarial extreme where the theme's accent is the text color itself: the
// tint still only moves 22% of the way from the background, so it can still
// clear both floors on a background/foreground pair with enough headroom.
func TestSelectionBand_AccentEqualsForegroundStillClearsBothFloors(t *testing.T) {
	const bg, fg = "#1a1b26", "#c0caf5"
	want := mix(bg, fg, bandMix)
	if c := contrast(want, fg); c < minBandText {
		t.Fatalf("test setup: accent tint contrast against fg = %v, want >= %v", c, minBandText)
	}
	if c := contrast(want, bg); c < minBandVisible {
		t.Fatalf("test setup: accent tint contrast against bg = %v, want >= %v", c, minBandVisible)
	}
	if got := selectionBand(bg, fg, fg); got != want {
		t.Errorf("selectionBand(%q, %q, accent=fg) = %q, want the accent tint %q", bg, fg, got, want)
	}
}

// TestSelectionBand_NoAccentFallsBackToANeutralCandidate covers an Omarchy
// palette with neither accent nor color4 set (mapOmarchyPalette's primary
// tint is then ""): the accent-tint candidate is unparseable and always
// loses, so the band must come from the neutral away-/towards-text
// candidates instead.
func TestSelectionBand_NoAccentFallsBackToANeutralCandidate(t *testing.T) {
	const bg, fg = "#1a1b26", "#c0caf5"
	if got := mix(bg, "", bandMix); got != "" {
		t.Fatalf("test setup: mix with an empty accent must be unparseable, got %q", got)
	}
	want := mix(bg, towardsText(bg, fg), bandMix)
	if c := contrast(want, fg); c < minBandText {
		t.Fatalf("test setup: towards-text candidate contrast against fg = %v, want >= %v", c, minBandText)
	}
	if got := selectionBand(bg, fg, ""); got != want {
		t.Errorf("selectionBand(%q, %q, \"\") = %q, want %q", bg, fg, got, want)
	}
}

// TestSelectionBand_BackgroundAlreadyAtAnExtreme covers a background pinned
// to pure black or pure white — the away-from-text neutral candidate has no
// room to move further in that direction, but the accent tint still has
// somewhere to go and wins.
func TestSelectionBand_BackgroundAlreadyAtAnExtreme(t *testing.T) {
	cases := []struct {
		name           string
		bg, fg, accent string
	}{
		{"black background", "#000000", "#ffffff", "#ff8800"},
		{"white background", "#ffffff", "#000000", "#0044ff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := mix(c.bg, c.accent, bandMix)
			if got := contrast(want, c.fg); got < minBandText {
				t.Fatalf("test setup: accent tint contrast against fg = %v, want >= %v", got, minBandText)
			}
			if got := contrast(want, c.bg); got < minBandVisible {
				t.Fatalf("test setup: accent tint contrast against bg = %v, want >= %v", got, minBandVisible)
			}
			if got := selectionBand(c.bg, c.fg, c.accent); got != want {
				t.Errorf("selectionBand(%q, %q, %q) = %q, want the accent tint %q", c.bg, c.fg, c.accent, got, want)
			}
		})
	}
}

// TestSelectionBand_VisibilityConcededInFavorOfABetterCandidate covers the
// adversarial "accent == background" extreme. The accent-tint candidate then
// degenerates to the background itself (mix(bg, bg, t) == bg), so its
// contrast against the background is exactly 1 — always short of
// minBandVisible. On this background/foreground pair the away-from-text
// neutral candidate falls short of minBandVisible too (a real, not
// contrived, near-boundary value below the 1.10 floor), so both are passed
// over in favor of the towards-text candidate, which clears both floors.
func TestSelectionBand_VisibilityConcededInFavorOfABetterCandidate(t *testing.T) {
	const bg, fg = "#1a1b26", "#c0caf5"

	accentTint := mix(bg, bg, bandMix)
	if accentTint != bg {
		t.Fatalf("test setup: mix(bg, bg, bandMix) = %q, want bg itself %q", accentTint, bg)
	}
	if got := contrast(accentTint, bg); got != 1 {
		t.Fatalf("test setup: contrast(bg, bg) = %v, want exactly 1", got)
	}

	away := mix(bg, awayFromText(bg, fg), bandMix)
	if got := contrast(away, bg); got >= minBandVisible {
		t.Fatalf("test setup: away-from-text candidate contrast against bg = %v, want < %v (this case needs it below the floor)", got, minBandVisible)
	}

	towards := mix(bg, towardsText(bg, fg), bandMix)
	if got := contrast(towards, fg); got < minBandText {
		t.Fatalf("test setup: towards-text candidate contrast against fg = %v, want >= %v", got, minBandText)
	}
	if got := contrast(towards, bg); got < minBandVisible {
		t.Fatalf("test setup: towards-text candidate contrast against bg = %v, want >= %v", got, minBandVisible)
	}

	if got := selectionBand(bg, fg, bg); got != towards {
		t.Errorf("selectionBand(%q, %q, accent=bg) = %q, want the towards-text candidate %q", bg, fg, got, towards)
	}
}

// TestSelectionBand_ThresholdBoundaries pins real (bg, fg, accent) triples
// whose derived contrast lands within a hundredth of a unit of minBandText or
// minBandVisible on either side, so the >= comparisons in selectionBand's
// loops are exercised at their edges rather than comfortably inside them.
// Each case first asserts, from the primitives directly, that the setup
// really does land where the case claims — a numeric coincidence discovered
// by search, not hand-tuned — before checking selectionBand's choice.
func TestSelectionBand_ThresholdBoundaries(t *testing.T) {
	t.Run("accent tint lands just above the readability floor", func(t *testing.T) {
		const bg, fg, accent = "#3333dd", "#99ff66", "#99ff66"
		want := mix(bg, accent, bandMix)
		if got := contrast(want, fg); got < minBandText || got > minBandText+0.01 {
			t.Fatalf("test setup: accent tint contrast against fg = %v, want within 0.01 of %v", got, minBandText)
		}
		if got := selectionBand(bg, fg, accent); got != want {
			t.Errorf("selectionBand(%q, %q, %q) = %q, want the accent tint %q", bg, fg, accent, got, want)
		}
	})

	t.Run("accent tint lands just below the readability floor and is skipped", func(t *testing.T) {
		const bg, fg, accent = "#ccdd88", "#3300cc", "#3300cc"
		accentTint := mix(bg, accent, bandMix)
		if got := contrast(accentTint, fg); got >= minBandText || got < minBandText-0.01 {
			t.Fatalf("test setup: accent tint contrast against fg = %v, want within 0.01 below %v", got, minBandText)
		}
		got := selectionBand(bg, fg, accent)
		if got == accentTint {
			t.Errorf("selectionBand(%q, %q, %q) = %q, the accent tint, but it falls short of minBandText", bg, fg, accent, got)
		}
		if c := contrast(got, fg); c < minBandText {
			t.Errorf("selectionBand(%q, %q, %q) = %q, contrast against fg = %v, want >= %v", bg, fg, accent, got, c, minBandText)
		}
	})

	t.Run("a candidate lands just above the visibility floor", func(t *testing.T) {
		// Here it is the away-from-text neutral candidate (not the accent
		// tint) that lands just above the floor — the accent tint fails an
		// earlier check on this pair, so the loop moves on to it.
		const bg, fg, accent = "#222244", "#0099cc", "#0099cc"
		want := mix(bg, awayFromText(bg, fg), bandMix)
		if got := contrast(want, bg); got < minBandVisible || got > minBandVisible+0.01 {
			t.Fatalf("test setup: away-from-text candidate contrast against bg = %v, want within 0.01 of %v", got, minBandVisible)
		}
		if got := contrast(want, fg); got < minBandText {
			t.Fatalf("test setup: away-from-text candidate contrast against fg = %v, want >= %v", got, minBandText)
		}
		if got := selectionBand(bg, fg, accent); got != want {
			t.Errorf("selectionBand(%q, %q, %q) = %q, want the away-from-text candidate %q", bg, fg, accent, got, want)
		}
	})
}

// bandLevels are 16 per-channel levels (step 17, spanning 0..255) used to
// build the background grid for the selection-band property tests below.
var bandLevels = [16]uint8{0, 17, 34, 51, 68, 85, 102, 119, 136, 153, 170, 187, 204, 221, 238, 255}

// bandForegroundLevels takes every third of bandLevels (6 levels), giving a
// coarser foreground grid so the combined background x foreground grid below
// stays fast.
var bandForegroundLevels = [6]uint8{bandLevels[0], bandLevels[3], bandLevels[6], bandLevels[9], bandLevels[12], bandLevels[15]}

// hexGrid returns every "#rrggbb" combination of the given per-channel
// levels.
func hexGrid(levels []uint8) []string {
	out := make([]string, 0, len(levels)*len(levels)*len(levels))
	for _, r := range levels {
		for _, g := range levels {
			for _, b := range levels {
				out = append(out, fmt.Sprintf("#%02x%02x%02x", r, g, b))
			}
		}
	}
	return out
}

// TestSelectionBandHoldsItsInvariant is a bounded deterministic grid over
// backgrounds (16 levels per channel), foregrounds (a coarser 6-level
// subgrid), and the two adversarial accents (the foreground and the
// background itself). For every (bg, fg) pair readable on its own terms
// (contrast(fg, bg) >= minBandText), the derived band must stay readable
// against fg too.
func TestSelectionBandHoldsItsInvariant(t *testing.T) {
	bgs := hexGrid(bandLevels[:])
	fgs := hexGrid(bandForegroundLevels[:])

	checked := 0
	for _, bg := range bgs {
		for _, fg := range fgs {
			if contrast(fg, bg) < minBandText {
				continue
			}
			for _, accent := range [2]string{fg, bg} {
				band := selectionBand(bg, fg, accent)
				if got := contrast(band, fg); got < minBandText {
					t.Fatalf("selectionBand(%q, %q, %q) = %q, contrast against fg = %v, want >= %v",
						bg, fg, accent, band, got, minBandText)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("grid produced no readable (bg, fg) pairs to check — test is vacuous")
	}
	t.Logf("checked %d readable (bg, fg, accent) combinations", checked)
}

// TestSelectionBandCandidate2Monotonicity checks the away-from-text neutral
// candidate never reads worse against the foreground than the plain
// background does, over every (bg, fg) pair in the grid — including
// unreadable ones, since this property doesn't depend on readability already
// being cleared.
func TestSelectionBandCandidate2Monotonicity(t *testing.T) {
	bgs := hexGrid(bandLevels[:])
	fgs := hexGrid(bandForegroundLevels[:])

	checked := 0
	for _, bg := range bgs {
		for _, fg := range fgs {
			base := contrast(fg, bg)
			candidate := mix(bg, awayFromText(bg, fg), bandMix)
			if got := contrast(candidate, fg); got < base {
				t.Fatalf("contrast(mix(bg, awayFromText(bg,fg), bandMix), fg) = %v, want >= contrast(bg,fg) = %v (bg=%q fg=%q)",
					got, base, bg, fg)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("grid produced no (bg, fg) pairs to check — test is vacuous")
	}
	t.Logf("checked %d (bg, fg) combinations", checked)
}
