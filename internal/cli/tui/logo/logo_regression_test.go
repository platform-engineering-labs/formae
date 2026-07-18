// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"testing"
)

// hasBrailleRune reports whether s contains at least one braille rune
// (Unicode range U+2800..U+28FF).
func hasBrailleRune(s string) bool {
	for _, r := range s {
		if r >= 0x2800 && r <= 0x28FF {
			return true
		}
	}
	return false
}

// TestRender_KittyFallbackToBraille stubs encodeKittyFn to return "" (simulating
// an encode error) and asserts that Render(CapKitty, SizeFull, …) degrades to
// braille rather than returning an empty string.
func TestRender_KittyFallbackToBraille(t *testing.T) {
	t.Parallel()

	origKitty := encodeKittyFn
	encodeKittyFn = func(_ bool, _ int) string { return "" }
	t.Cleanup(func() { encodeKittyFn = origKitty })

	art, rows := Render(CapKitty, SizeFull, "1.2.3")

	if art == "" {
		t.Fatal("Render(CapKitty, SizeFull) returned empty string after encoder failure — expected braille fallback")
	}
	if !hasBrailleRune(art) {
		t.Errorf("Render(CapKitty, SizeFull) did not fall back to braille; got %q", art)
	}
	if rows <= 0 {
		t.Errorf("expected rows>0, got %d", rows)
	}
}

// TestRender_ITerm2FallbackToBraille stubs encodeITerm2Fn to return "" and
// asserts that Render(CapITerm2, SizeFull, …) degrades to braille.
func TestRender_ITerm2FallbackToBraille(t *testing.T) {
	t.Parallel()

	origITerm2 := encodeITerm2Fn
	encodeITerm2Fn = func(_ bool, _ int) string { return "" }
	t.Cleanup(func() { encodeITerm2Fn = origITerm2 })

	art, rows := Render(CapITerm2, SizeFull, "1.2.3")

	if art == "" {
		t.Fatal("Render(CapITerm2, SizeFull) returned empty string after encoder failure — expected braille fallback")
	}
	if !hasBrailleRune(art) {
		t.Errorf("Render(CapITerm2, SizeFull) did not fall back to braille; got %q", art)
	}
	if rows <= 0 {
		t.Errorf("expected rows>0, got %d", rows)
	}
}
