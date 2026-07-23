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

func TestEncodeITerm2_Golden(t *testing.T) {
	t.Parallel()
	out := encodeITerm2(true, graphicsFullCols)
	tuitest.RequireGolden(t, []byte(out))
}

// TestRender_KittyFullLogo asserts that SizeFull + CapKitty output:
//   - contains the C=1 Kitty graphics escape (the full wordmark image)
//   - drops graphicsFullLogoVersionRow rows then positions the version via CHA
//     (\x1b[<N>G) at graphicsFullLogoTextCol using real newlines (not CUD)
//   - contains "v1.2.3" as selectable text (the "formae" letters live IN the image)
//   - advances the cursor fully below the image (graphicsFullLogoImageRows rows)
func TestRender_KittyFullLogo(t *testing.T) {
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

	// Version is placed on its row: graphicsFullLogoVersionRow real newlines,
	// then a CHA to graphicsFullLogoTextCol (no \x1b[1B CUD — unreliable in Kitty
	// after a C=1 image).
	verPos := strings.Repeat("\n", graphicsFullLogoVersionRow) +
		fmt.Sprintf("\x1b[%dG", graphicsFullLogoTextCol)
	if !strings.Contains(art, verPos) {
		t.Errorf("expected version row-drop + CHA %q in output; tail: %q", verPos, art[max(0, len(art)-120):])
	}

	// Must contain "v1.2.3" as selectable terminal text.
	if !strings.Contains(art, "v1.2.3") {
		t.Error("expected 'v1.2.3' as selectable text in Kitty output")
	}

	// Art ends by advancing the rest of the way below the image.
	tail := strings.Repeat("\n", graphicsFullLogoImageRows-graphicsFullLogoVersionRow)
	if !strings.HasSuffix(art, tail) {
		t.Errorf("expected art to end with %d trailing newlines to clear the image", graphicsFullLogoImageRows-graphicsFullLogoVersionRow)
	}

	// rows must equal graphicsFullLogoImageRows.
	if rows != graphicsFullLogoImageRows {
		t.Errorf("expected rows=%d (graphicsFullLogoImageRows), got %d", graphicsFullLogoImageRows, rows)
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
