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
// places the wordmark to the RIGHT of the image via C=1 + cursor-forward
// sequences (no DEC save/restore). We verify: cursor-forward (ESC[nC) is
// present, DEC save (ESC 7) is NOT present, and both wordmark lines appear.
func TestRender_KittyGraphicsWordmarkRight(t *testing.T) {
	t.Parallel()

	// Stub the encoder to return a deterministic non-empty string so the
	// real PNG encode path is skipped and the test remains fast + hermetic.
	origKitty := encodeKittyFn
	encodeKittyFn = func(_ bool, _, _ int) string { return "\x1b_Ga=T,f=100,C=1,c=8,r=4,m=0;AAAA\x1b\\" }
	t.Cleanup(func() { encodeKittyFn = origKitty })

	art, rows := Render(CapKitty, SizeFull, "1.2.3")

	if art == "" {
		t.Fatal("Render(CapKitty, SizeFull) returned empty string")
	}

	// DEC save/restore must NOT be present — Kitty uses C=1 for deterministic placement.
	const decSave = "\x1b7"
	if strings.Contains(art, decSave) {
		t.Error("Kitty graphics art contains DEC save cursor (ESC 7) — should use C=1 instead")
	}
	const decRestore = "\x1b8"
	if strings.Contains(art, decRestore) {
		t.Error("Kitty graphics art contains DEC restore cursor (ESC 8) — should use C=1 instead")
	}

	// Cursor-forward sequence (ESC[nC) must be present for right-of-image placement.
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

// TestRender_ITerm2GraphicsWordmarkBelow asserts that Render(CapITerm2, SizeFull, …)
// places the wordmark BELOW the image (not right-of-image) — iTerm2 has no C=1
// equivalent so safe newline placement is used. DEC save/restore must NOT appear.
func TestRender_ITerm2GraphicsWordmarkBelow(t *testing.T) {
	t.Parallel()

	origITerm2 := encodeITerm2Fn
	encodeITerm2Fn = func(_ bool, _, _ int) string {
		return "\x1b]1337;File=inline=1;size=4;width=8;height=4;preserveAspectRatio=0:AAAA\a"
	}
	t.Cleanup(func() { encodeITerm2Fn = origITerm2 })

	art, _ := Render(CapITerm2, SizeFull, "2.0.0")

	if art == "" {
		t.Fatal("Render(CapITerm2, SizeFull) returned empty string")
	}

	// DEC save/restore must NOT be present for iTerm2.
	const decSave = "\x1b7"
	if strings.Contains(art, decSave) {
		t.Error("iTerm2 graphics art contains DEC save cursor (ESC 7) — unexpected")
	}

	// Wordmark must appear below: the image escape comes first, then newline(s), then text.
	imgSeq := "\x1b]1337;"
	imgIdx := strings.Index(art, imgSeq)
	formaeIdx := strings.Index(art, "formae")
	verIdx := strings.Index(art, "v2.0.0")

	if formaeIdx < 0 {
		t.Errorf("graphics art missing 'formae' wordmark")
	}
	if verIdx < 0 {
		t.Errorf("graphics art missing 'v2.0.0' version")
	}
	if imgIdx >= 0 && formaeIdx > 0 && formaeIdx < imgIdx {
		t.Error("'formae' appears BEFORE the image escape — expected below the image")
	}

	// The wordmark section (after the image) must contain a newline between
	// the image escape and the wordmark text (below, not inline).
	afterImg := art[imgIdx+len(imgSeq):]
	if !strings.Contains(afterImg, "\n") {
		t.Error("no newline between iTerm2 image escape and wordmark — expected wordmark below image")
	}
}
