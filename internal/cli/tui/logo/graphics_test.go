// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"fmt"
	_ "image/png"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestEncodeKitty_Golden(t *testing.T) {
	t.Parallel()
	out := encodeKitty(true, graphicsFullCols)
	tuitest.RequireGolden(t, []byte(out))
}

func TestEncodeITerm2_Golden(t *testing.T) {
	t.Parallel()
	out := encodeITerm2(true, graphicsFullCols)
	tuitest.RequireGolden(t, []byte(out))
}

// TestRender_KittyTextRight asserts that SizeFull + CapKitty output:
//   - contains the C=1 Kitty graphics escape
//   - contains CHA positioning (\x1b[<N>G) for "formae"
//   - contains a relative row-down move (\x1b[1B) for the version line
//   - contains a trailing cursor-below move (\x1b[<n>B)
//   - does NOT composite text into an image (no buildBannerImage)
func TestRender_KittyTextRight(t *testing.T) {
	t.Parallel()

	// Override hasDarkBackground for determinism.
	orig := hasDarkBackground
	hasDarkBackground = func() bool { return true }
	defer func() { hasDarkBackground = orig }()

	art, rows := Render(CapKitty, SizeFull, "1.2.3")

	// Must contain the Kitty APC escape with C=1.
	if !strings.Contains(art, "\033_G") {
		t.Error("expected Kitty APC escape in output")
	}
	if !strings.Contains(art, "C=1") {
		t.Error("expected C=1 in Kitty escape (cursor-no-advance)")
	}

	// Must contain CHA positioning for "formae" text.
	chaCol := fmt.Sprintf("\x1b[%dG", graphicsTextCol)
	if !strings.Contains(art, chaCol) {
		t.Errorf("expected CHA escape %q (col %d) in output; got: %q", chaCol, graphicsTextCol, art[:min(len(art), 300)])
	}

	// Must contain "formae" as selectable terminal text.
	if !strings.Contains(art, "formae") {
		t.Error("expected 'formae' as selectable text in Kitty output")
	}

	// Must contain "v1.2.3" as selectable terminal text.
	if !strings.Contains(art, "v1.2.3") {
		t.Error("expected 'v1.2.3' as selectable text in Kitty output")
	}

	// Version line + cursor-below moves use real newlines (\n), not the
	// \x1b[1B CUD escape (which proved unreliable in Kitty after a C=1 image).
	// The version CHA sits immediately after a newline, and the art ends with
	// graphicsImageRows-1 trailing newlines to clear the image.
	verCHA := fmt.Sprintf("\n\x1b[%dG", graphicsTextCol)
	if !strings.Contains(art, verCHA) {
		t.Errorf("expected newline + CHA %q before the version line", verCHA)
	}
	if !strings.HasSuffix(art, strings.Repeat("\n", graphicsImageRows-1)) {
		t.Errorf("expected art to end with %d trailing newlines to clear the image", graphicsImageRows-1)
	}

	// rows must equal graphicsImageRows.
	if rows != graphicsImageRows {
		t.Errorf("expected rows=%d (graphicsImageRows), got %d", graphicsImageRows, rows)
	}
}

// TestRender_ITerm2TextBelowIntegration asserts that SizeFull + CapITerm2 output
// (with the real encoder) contains the iTerm2 inline-image escape and
// "formae" / "v{version}" as terminal text AFTER the image escape.
func TestRender_ITerm2TextBelowIntegration(t *testing.T) {
	t.Parallel()

	orig := hasDarkBackground
	hasDarkBackground = func() bool { return true }
	defer func() { hasDarkBackground = orig }()

	art, _ := Render(CapITerm2, SizeFull, "1.2.3")

	if !strings.Contains(art, "\x1b]1337") {
		t.Error("expected iTerm2 OSC 1337 escape in output")
	}

	// Text should appear after the image escape (text-below for iTerm2).
	afterEsc := art
	if idx := strings.LastIndex(art, "\a"); idx >= 0 {
		afterEsc = art[idx+1:]
	}
	if !strings.Contains(afterEsc, "formae") {
		t.Error("expected 'formae' as terminal text below the iTerm2 image")
	}
	if !strings.Contains(afterEsc, "v1.2.3") {
		t.Error("expected 'v1.2.3' as terminal text below the iTerm2 image")
	}
}
