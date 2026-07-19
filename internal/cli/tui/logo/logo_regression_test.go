// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"strings"
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
// Note: NOT parallel — mutates the package-level encodeKittyFn seam.
func TestRender_KittyFallbackToBraille(t *testing.T) {
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
// Note: NOT parallel — mutates the package-level encodeITerm2Fn seam.
func TestRender_ITerm2FallbackToBraille(t *testing.T) {
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

// TestRender_KittyTextRightComposition stubs encodeKittyFn to return a
// synthetic image escape and asserts that Render(CapKitty, SizeFull, …)
// composes selectable terminal text to the right using CHA positioning.
// Note: NOT parallel — mutates the package-level encodeKittyFn seam.
func TestRender_KittyTextRightComposition(t *testing.T) {
	origKitty := encodeKittyFn
	encodeKittyFn = func(_ bool, _ int) string { return "\x1b_Ga=T,f=100,C=1,m=0;AAAA\x1b\\" }
	t.Cleanup(func() { encodeKittyFn = origKitty })

	art, rows := Render(CapKitty, SizeFull, "1.2.3")

	if art == "" {
		t.Fatal("Render(CapKitty, SizeFull) returned empty string")
	}

	// Must contain the C=1 image escape.
	if !strings.Contains(art, "C=1") {
		t.Error("Kitty output missing C=1 in image escape")
	}

	// Must contain CHA positioning for wordmark.
	if !strings.Contains(art, "\x1b[8G") {
		t.Errorf("Kitty output missing CHA positioning \\x1b[8G (graphicsTextCol=%d)", graphicsTextCol)
	}

	// Must contain selectable terminal text for "formae" and "v1.2.3".
	if !strings.Contains(art, "formae") {
		t.Error("Kitty output missing selectable 'formae' text")
	}
	if !strings.Contains(art, "v1.2.3") {
		t.Error("Kitty output missing selectable 'v1.2.3' text")
	}

	// rows must equal graphicsImageRows.
	if rows != graphicsImageRows {
		t.Errorf("expected rows=%d, got %d", graphicsImageRows, rows)
	}
}

// TestRender_ITerm2TextBelow stubs encodeITerm2Fn to return a synthetic
// image escape and asserts that Render(CapITerm2, SizeFull, …) places
// selectable terminal text BELOW the image (iTerm2 has no C=1).
// Note: NOT parallel — mutates the package-level encodeITerm2Fn seam.
func TestRender_ITerm2TextBelow(t *testing.T) {
	origITerm2 := encodeITerm2Fn
	encodeITerm2Fn = func(_ bool, _ int) string {
		return "\x1b]1337;File=inline=1;size=4:AAAA\a"
	}
	t.Cleanup(func() { encodeITerm2Fn = origITerm2 })

	art, rows := Render(CapITerm2, SizeFull, "2.0.0")

	if art == "" {
		t.Fatal("Render(CapITerm2, SizeFull) returned empty string")
	}

	// Text should appear after the image escape (below the image).
	afterEsc := art
	if idx := strings.LastIndex(art, "\a"); idx >= 0 {
		afterEsc = art[idx+1:]
	}
	if !strings.Contains(afterEsc, "formae") {
		t.Error("iTerm2 output missing selectable 'formae' text below image")
	}
	if !strings.Contains(afterEsc, "v2.0.0") {
		t.Error("iTerm2 output missing selectable 'v2.0.0' text below image")
	}

	if rows <= 0 {
		t.Errorf("expected rows>0, got %d", rows)
	}
}
