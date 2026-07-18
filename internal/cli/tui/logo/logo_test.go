// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"bytes"
	"image"
	_ "image/png"
	"strings"
	"testing"
)

func TestLogoBytes_DecodablePNG(t *testing.T) {
	t.Parallel()
	for _, dark := range []bool{true, false} {
		dark := dark
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := logoBytes(dark)
			if len(b) == 0 {
				t.Fatal("logoBytes returned empty slice")
			}
			img, _, err := image.Decode(bytes.NewReader(b))
			if err != nil {
				t.Fatalf("image.Decode: %v", err)
			}
			bounds := img.Bounds()
			if bounds.Dx() == 0 || bounds.Dy() == 0 {
				t.Fatalf("decoded image has zero bounds: %v", bounds)
			}
		})
	}
}

// TestRender_None asserts that SizeNone always returns ("", 0) regardless of capability.
func TestRender_None(t *testing.T) {
	t.Parallel()
	art, rows := Render(CapBraille, SizeNone, "x")
	if art != "" {
		t.Errorf("expected empty art, got %q", art)
	}
	if rows != 0 {
		t.Errorf("expected rows=0, got %d", rows)
	}
}

// TestRender_Text asserts that CapText produces the plain-text version string.
func TestRender_Text(t *testing.T) {
	t.Parallel()
	art, rows := Render(CapText, SizeFull, "1.2.3")
	if art != "formae v1.2.3" {
		t.Errorf("expected %q, got %q", "formae v1.2.3", art)
	}
	if rows != 1 {
		t.Errorf("expected rows=1, got %d", rows)
	}
}

// TestRender_CompactAlwaysBraille enforces the coexistence rule: SizeCompact
// must return braille art (contains U+2800-range runes) and MUST NOT contain
// a Kitty APC escape (\x1b_G) or iTerm2 OSC escape (\x1b]1337), even when
// the capability is CapKitty.
func TestRender_CompactAlwaysBraille(t *testing.T) {
	t.Parallel()
	art, rows := Render(CapKitty, SizeCompact, "x")

	// Must not contain graphics escapes.
	if strings.Contains(art, "\x1b_G") {
		t.Error("SizeCompact returned a Kitty graphics escape — violates coexistence rule")
	}
	if strings.Contains(art, "\x1b]1337") {
		t.Error("SizeCompact returned an iTerm2 graphics escape — violates coexistence rule")
	}

	// Must contain at least one braille rune (U+2800..U+28FF).
	hasBraille := false
	for _, r := range art {
		if r >= 0x2800 && r <= 0x28FF {
			hasBraille = true
			break
		}
	}
	if !hasBraille {
		t.Errorf("SizeCompact did not return braille art; got %q", art)
	}

	// rows must be positive.
	if rows <= 0 {
		t.Errorf("expected rows>0 for compact braille, got %d", rows)
	}
}

// TestRender_FullBrailleRows asserts that SizeFull with CapBraille returns a
// multi-row string and that rows matches the number of newlines+1.
func TestRender_FullBrailleRows(t *testing.T) {
	t.Parallel()
	art, rows := Render(CapBraille, SizeFull, "v1")
	if art == "" {
		t.Fatal("expected non-empty art for SizeFull CapBraille")
	}
	newlines := strings.Count(art, "\n")
	expected := newlines + 1
	if rows != expected {
		t.Errorf("rows mismatch: art has %d newlines so expected rows=%d, got %d", newlines, expected, rows)
	}
}

// TestRender_FullBrailleWordmark asserts that SizeFull with CapBraille includes
// the wordmark ("formae" and "v1.2.3") alongside braille runes, on separate
// lines, with white (#FFFFFF → ANSI 38;2;255;255;255) for "formae" and brand
// orange (#FF8201 → ANSI 38;2;255;130;1) for the version.
func TestRender_FullBrailleWordmark(t *testing.T) {
	t.Parallel()
	art, _ := Render(CapBraille, SizeFull, "1.2.3")
	if art == "" {
		t.Fatal("expected non-empty art for SizeFull CapBraille")
	}
	if !strings.Contains(art, "formae") {
		t.Errorf("SizeFull CapBraille art missing wordmark 'formae'; got %q", art[:min(len(art), 200)])
	}
	if !strings.Contains(art, "v1.2.3") {
		t.Errorf("SizeFull CapBraille art missing version 'v1.2.3'; got %q", art[:min(len(art), 200)])
	}
	if !hasBrailleRune(art) {
		t.Errorf("SizeFull CapBraille art contains no braille runes; got %q", art[:min(len(art), 200)])
	}

	// "formae" and "v1.2.3" must appear on separate lines.
	lines := strings.Split(art, "\n")
	formaeLineIdx := -1
	versionLineIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "formae") {
			formaeLineIdx = i
		}
		if strings.Contains(line, "v1.2.3") {
			versionLineIdx = i
		}
	}
	if formaeLineIdx < 0 {
		t.Error("'formae' not found on any line")
	}
	if versionLineIdx < 0 {
		t.Error("'v1.2.3' not found on any line")
	}
	if formaeLineIdx >= 0 && versionLineIdx >= 0 && formaeLineIdx == versionLineIdx {
		t.Errorf("'formae' and 'v1.2.3' are on the same line (%d); expected separate lines", formaeLineIdx)
	}

	// White (#FFFFFF) ANSI sequence must be present for "formae".
	const whiteANSI = "38;2;255;255;255"
	if !strings.Contains(art, whiteANSI) {
		t.Errorf("expected white ANSI color (%s) for 'formae'; not found in art", whiteANSI)
	}

	// Brand orange (#FF8201 = rgb(255,130,1)) ANSI sequence must be present for version.
	const orangeANSI = "38;2;255;130;1"
	if !strings.Contains(art, orangeANSI) {
		t.Errorf("expected brand-orange ANSI color (%s) for version; not found in art", orangeANSI)
	}

	// AdaptiveColor must not have been used — no terminal background query artifact.
	// (Guard: AdaptiveColor emits ESC]10 or ESC]11 queries; plain colors do not.)
	const adaptiveQueryPrefix = "\x1b]11"
	if strings.Contains(art, adaptiveQueryPrefix) {
		t.Error("art contains an OSC terminal background query (AdaptiveColor leak); expected plain fixed colors only")
	}
}

// TestRender_CompactNoWordmark asserts that SizeCompact does NOT include the
// wordmark (it is a compact icon only).
func TestRender_CompactNoWordmark(t *testing.T) {
	t.Parallel()
	art, _ := Render(CapBraille, SizeCompact, "1.2.3")
	if strings.Contains(art, "formae") {
		t.Errorf("SizeCompact art must NOT contain wordmark 'formae'; got %q", art)
	}
	if strings.Contains(art, "v1.2.3") {
		t.Errorf("SizeCompact art must NOT contain version 'v1.2.3'; got %q", art)
	}
}

// TestHasDarkBackground_Tmux asserts that hasDarkBackground returns true under
// tmux without querying the terminal.
func TestHasDarkBackground_Tmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
	// Clear other env vars that might interfere.
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")

	if !hasDarkBackground() {
		t.Error("hasDarkBackground() should return true under TMUX without querying")
	}
}
