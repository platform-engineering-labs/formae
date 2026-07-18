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
func TestRender_KittyFallbackToBraille(t *testing.T) {
	t.Parallel()

	origKitty := encodeKittyFn
	encodeKittyFn = func(_ bool, _, _ int) string { return "" }
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
	encodeITerm2Fn = func(_ bool, _, _ int) string { return "" }
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

// TestRender_KittyGraphicsWordmarkRight asserts that Render(CapKitty, SizeFull, …)
// places the wordmark to the RIGHT of the image via DEC cursor positioning — not
// just appended below. We verify: DEC save (ESC 7) is present, a cursor-forward
// sequence (ESC[nC) is present, and both "formae" and "v{version}" appear in the
// output.
func TestRender_KittyGraphicsWordmarkRight(t *testing.T) {
	t.Parallel()

	// Stub the encoder to return a deterministic non-empty string so the
	// real PNG encode path is skipped and the test remains fast + hermetic.
	origKitty := encodeKittyFn
	encodeKittyFn = func(_ bool, _, _ int) string { return "\x1b_Ga=T,f=100,c=12,r=6,m=0;AAAA\x1b\\" }
	t.Cleanup(func() { encodeKittyFn = origKitty })

	art, rows := Render(CapKitty, SizeFull, "1.2.3")

	if art == "" {
		t.Fatal("Render(CapKitty, SizeFull) returned empty string")
	}

	// DEC save cursor must be present (wordmark is positioned via save/restore).
	const decSave = "\x1b7"
	if !strings.Contains(art, decSave) {
		t.Errorf("graphics art missing DEC save cursor (ESC 7); wordmark is not positioned right-of-image")
	}

	// Cursor-forward sequence (ESC[nC) must be present.
	if !strings.Contains(art, "\x1b[") {
		t.Errorf("graphics art missing cursor-movement sequences (ESC[); wordmark is not positioned right-of-image")
	}

	// Both wordmark lines must appear.
	if !strings.Contains(art, "formae") {
		t.Errorf("graphics art missing 'formae' wordmark")
	}
	if !strings.Contains(art, "v1.2.3") {
		t.Errorf("graphics art missing 'v1.2.3' version")
	}

	// rows must equal graphicsRows exactly.
	if rows != graphicsRows {
		t.Errorf("rows = %d; want graphicsRows = %d", rows, graphicsRows)
	}
}

// TestRender_ITerm2GraphicsWordmarkRight is the iTerm2 equivalent of the above.
func TestRender_ITerm2GraphicsWordmarkRight(t *testing.T) {
	t.Parallel()

	origITerm2 := encodeITerm2Fn
	encodeITerm2Fn = func(_ bool, _, _ int) string {
		return "\x1b]1337;File=inline=1;size=4;width=12;height=6;preserveAspectRatio=0:AAAA\a"
	}
	t.Cleanup(func() { encodeITerm2Fn = origITerm2 })

	art, rows := Render(CapITerm2, SizeFull, "2.0.0")

	if art == "" {
		t.Fatal("Render(CapITerm2, SizeFull) returned empty string")
	}

	const decSave = "\x1b7"
	if !strings.Contains(art, decSave) {
		t.Errorf("graphics art missing DEC save cursor (ESC 7); wordmark is not positioned right-of-image")
	}

	if !strings.Contains(art, "formae") {
		t.Errorf("graphics art missing 'formae' wordmark")
	}
	if !strings.Contains(art, "v2.0.0") {
		t.Errorf("graphics art missing 'v2.0.0' version")
	}

	if rows != graphicsRows {
		t.Errorf("rows = %d; want graphicsRows = %d", rows, graphicsRows)
	}
}
