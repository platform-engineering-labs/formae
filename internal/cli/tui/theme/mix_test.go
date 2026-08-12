//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import "testing"

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
