// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

// TestRender_KittyWordmarkTintChangesImage asserts that supplying a wordmark
// color recolors the Kitty logo image (the letters), so the emitted graphics
// escape differs from the untinted one — the end-to-end wiring for theming the
// graphics logo.
func TestRender_KittyWordmarkTintChangesImage(t *testing.T) {
	orig := hasDarkBackground
	hasDarkBackground = func() bool { return true }
	defer func() { hasDarkBackground = orig }()

	plain, _ := Render(CapKitty, SizeFull, "1.2.3")
	tinted, _ := Render(CapKitty, SizeFull, "1.2.3",
		WithWordmarkColor(lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"}))

	if plain == tinted {
		t.Error("Kitty logo image must change when a wordmark tint is applied")
	}
	if !strings.Contains(tinted, "\033_G") {
		t.Error("tinted output must still be a Kitty graphics escape")
	}
}

// TestTintWordmarkLetters verifies the wordmark tint recolors the white
// "formae" letters to the theme color while leaving the orange propeller
// untouched, preserving each pixel's alpha (antialiasing).
func TestTintWordmarkLetters(t *testing.T) {
	t.Parallel()

	src := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 230, G: 230, B: 230, A: 255}) // white letter
	src.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 130, B: 1, A: 255})   // orange propeller
	src.SetNRGBA(2, 0, color.NRGBA{R: 230, G: 230, B: 230, A: 128}) // antialiased letter edge

	tint := color.NRGBA{R: 96, G: 165, B: 250, A: 255} // rich blue #60A5FA
	out := tintWordmarkLetters(src, tint)

	// Letter pixel → tint color, full alpha.
	if got := color.NRGBAModel.Convert(out.At(0, 0)).(color.NRGBA); got != (color.NRGBA{R: 96, G: 165, B: 250, A: 255}) {
		t.Errorf("white letter pixel: got %+v, want blue tint at full alpha", got)
	}
	// Orange propeller pixel → unchanged.
	if got := color.NRGBAModel.Convert(out.At(1, 0)).(color.NRGBA); got != (color.NRGBA{R: 255, G: 130, B: 1, A: 255}) {
		t.Errorf("orange propeller pixel: got %+v, want unchanged orange", got)
	}
	// Antialiased letter edge → tint color, original alpha preserved.
	if got := color.NRGBAModel.Convert(out.At(2, 0)).(color.NRGBA); got != (color.NRGBA{R: 96, G: 165, B: 250, A: 128}) {
		t.Errorf("antialiased letter pixel: got %+v, want blue tint at alpha 128", got)
	}
}

func TestEncodeITerm2_Golden(t *testing.T) {
	t.Parallel()
	out := encodeITerm2(true, graphicsFullCols)
	tuitest.RequireGolden(t, []byte(out))
}

// TestKittyFullLogo_PinsCellFootprint asserts the Kitty image is transmitted
// with an explicit cell footprint (c=cols,r=rows). Pinning the footprint is what
// makes the version placement zoom-robust: the image always occupies exactly
// graphicsFullLogoCols×graphicsFullLogoImageRows cells regardless of font zoom,
// so the version at column graphicsFullLogoCols+1 stays aligned. Without it, the
// natural-size image spans a zoom-dependent number of cells and the version
// drifts.
func TestKittyFullLogo_PinsCellFootprint(t *testing.T) {
	t.Parallel()

	out := encodeKittyFullLogo(true, graphicsFullLogoWidthPx, nil)
	if out == "" {
		t.Fatal("encodeKittyFullLogo returned empty string")
	}

	want := fmt.Sprintf("c=%d,r=%d", graphicsFullLogoCols, graphicsFullLogoImageRows)
	if !strings.Contains(out, want) {
		t.Errorf("Kitty escape must pin the cell footprint %q for zoom-robust placement; head: %q",
			want, out[:min(len(out), 96)])
	}

	// The version column must sit two cells past the pinned image width, so the
	// two constants cannot drift apart.
	if graphicsFullLogoTextCol != graphicsFullLogoCols+2 {
		t.Errorf("graphicsFullLogoTextCol (%d) must be graphicsFullLogoCols+2 (%d)",
			graphicsFullLogoTextCol, graphicsFullLogoCols+2)
	}
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
