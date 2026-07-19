// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	_ "image/png"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestEncodeKitty_Golden(t *testing.T) {
	t.Parallel()
	out := encodeKitty(true, "0.87.0")
	tuitest.RequireGolden(t, []byte(out))
}

func TestEncodeITerm2_Golden(t *testing.T) {
	t.Parallel()
	out := encodeITerm2(true, "0.87.0")
	tuitest.RequireGolden(t, []byte(out))
}

// TestBuildBannerImage asserts that the composite image is wider than the
// propeller alone (proving text was appended) and at least logoPx tall.
func TestBuildBannerImage(t *testing.T) {
	t.Parallel()

	img, err := buildBannerImage(true, "0.87.0")
	if err != nil {
		t.Fatalf("buildBannerImage: %v", err)
	}
	if img == nil {
		t.Fatal("buildBannerImage returned nil image")
	}

	bounds := img.Bounds()
	if bounds.Dy() < logoPx {
		t.Errorf("image height %d < logoPx %d", bounds.Dy(), logoPx)
	}

	// Propeller-only width: scale cropW to logoPx height (match buildBannerImage math).
	// Use a runtime variable to avoid constant-expression truncation error.
	logoH := logoPx
	propW := int(float64(logoH) * float64(cropW) / float64(cropH))
	if bounds.Dx() <= propW {
		t.Errorf("image width %d <= propeller-only width %d; text was not appended", bounds.Dx(), propW)
	}
}

// TestRender_GraphicsNoTerminalWordmark asserts that Kitty and iTerm2 outputs
// are single graphics escapes with NO trailing terminal-text wordmark.
func TestRender_GraphicsNoTerminalWordmark(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cap  Capability
		esc  string
	}{
		{"Kitty", CapKitty, "\x1b_G"},
		{"ITerm2", CapITerm2, "\x1b]1337"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			art, _ := Render(tc.cap, SizeFull, "1.2.3")

			// Must contain the graphics escape.
			if !strings.Contains(art, tc.esc) {
				t.Errorf("expected %s escape in output, not found", tc.name)
			}

			// Must NOT contain terminal ANSI color for "formae" or "v1.2.3" as text.
			const whiteAnsi = "\x1b[38;2;255;255;255m"
			if strings.Contains(art, whiteAnsi+"formae") {
				t.Errorf("%s output contains terminal-text 'formae' with ANSI color (wordmark must be in image, not terminal text)", tc.name)
			}
			// Also check the raw text (without ANSI) does not appear after the escape sequence.
			// The image escape ends at \a or \\; text after that would be terminal wordmark.
			afterEsc := art
			if idx := strings.LastIndex(art, "\033\\"); idx >= 0 {
				afterEsc = art[idx+2:]
			} else if idx := strings.LastIndex(art, "\a"); idx >= 0 {
				afterEsc = art[idx+1:]
			}
			if strings.Contains(afterEsc, "formae") {
				t.Errorf("%s output has terminal-text 'formae' after the image escape; expected wordmark composited into image only", tc.name)
			}
		})
	}
}
