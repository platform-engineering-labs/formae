// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"bytes"
	"image"
	_ "image/png"
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
