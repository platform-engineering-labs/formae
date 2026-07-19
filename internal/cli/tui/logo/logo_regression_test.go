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
	encodeKittyFn = func(_ bool, _ string) string { return "" }
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
	encodeITerm2Fn = func(_ bool, _ string) string { return "" }
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

// TestRender_KittyGraphicsNoTerminalWordmark asserts that Render(CapKitty, SizeFull, …)
// returns ONLY the image escape — no trailing terminal-text wordmark — because the
// wordmark is now composited into the image itself.
// Note: NOT parallel — mutates the package-level encodeKittyFn seam.
func TestRender_KittyGraphicsNoTerminalWordmark(t *testing.T) {
	origKitty := encodeKittyFn
	encodeKittyFn = func(_ bool, _ string) string { return "\x1b_Ga=T,f=100,m=0;AAAA\x1b\\" }
	t.Cleanup(func() { encodeKittyFn = origKitty })

	art, rows := Render(CapKitty, SizeFull, "1.2.3")

	if art == "" {
		t.Fatal("Render(CapKitty, SizeFull) returned empty string")
	}

	// The output must be exactly the image escape — no terminal-text wordmark.
	if strings.Contains(art, "formae") {
		t.Error("Kitty graphics art contains terminal-text 'formae' — wordmark must be composited into image, not terminal text")
	}
	if strings.Contains(art, "v1.2.3") {
		t.Error("Kitty graphics art contains terminal-text 'v1.2.3' — wordmark must be composited into image, not terminal text")
	}

	// No cursor-positioning escapes must be present.
	if strings.Contains(art, "\x1b7") {
		t.Error("Kitty graphics art contains DEC save cursor (ESC 7) — must not use cursor escapes")
	}
	if strings.Contains(art, "\x1b8") {
		t.Error("Kitty graphics art contains DEC restore cursor (ESC 8) — must not use cursor escapes")
	}

	// rows must be positive.
	if rows <= 0 {
		t.Errorf("expected rows>0, got %d", rows)
	}
}

// TestRender_ITerm2GraphicsNoTerminalWordmark asserts that Render(CapITerm2, SizeFull, …)
// returns ONLY the image escape — no trailing terminal-text wordmark — because the
// wordmark is now composited into the image itself.
// Note: NOT parallel — mutates the package-level encodeITerm2Fn seam.
func TestRender_ITerm2GraphicsNoTerminalWordmark(t *testing.T) {
	origITerm2 := encodeITerm2Fn
	encodeITerm2Fn = func(_ bool, _ string) string {
		return "\x1b]1337;File=inline=1;size=4:AAAA\a"
	}
	t.Cleanup(func() { encodeITerm2Fn = origITerm2 })

	art, rows := Render(CapITerm2, SizeFull, "2.0.0")

	if art == "" {
		t.Fatal("Render(CapITerm2, SizeFull) returned empty string")
	}

	// The output must be exactly the image escape — no terminal-text wordmark.
	if strings.Contains(art, "formae") {
		t.Error("iTerm2 graphics art contains terminal-text 'formae' — wordmark must be composited into image, not terminal text")
	}
	if strings.Contains(art, "v2.0.0") {
		t.Error("iTerm2 graphics art contains terminal-text 'v2.0.0' — wordmark must be composited into image, not terminal text")
	}

	// DEC save/restore must NOT be present for iTerm2.
	if strings.Contains(art, "\x1b7") {
		t.Error("iTerm2 graphics art contains DEC save cursor (ESC 7) — unexpected")
	}

	if rows <= 0 {
		t.Errorf("expected rows>0, got %d", rows)
	}
}
