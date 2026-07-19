// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logo

import (
	"image"
	"image/color"
	"io"

	xfont "golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"golang.org/x/image/draw"
)

// Tunable layout constants for the composite banner image.
const (
	logoPx        = 72 // propeller height in pixels
	nameSizePx    = 30 // "formae" font size in points
	verSizePx     = 20 // "v{version}" font size in points
	wordmarkGapPx = 16 // horizontal gap between propeller and text block
	lineGapPx     = 4  // vertical gap between name and version lines
)

var (
	colorWhite  = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	colorOrange = color.RGBA{R: 255, G: 130, B: 1, A: 255}
)

// buildBannerImage composes the propeller and wordmark ("formae" / "v{version}")
// into a single RGBA image. The propeller is scaled to logoPx height and placed
// on the left; the wordmark block is centered vertically to the right.
// The canvas background is transparent so the terminal's bg shows through.
func buildBannerImage(dark bool, version string) (image.Image, error) {
	propImg, err := loadAndCropPropeller(dark)
	if err != nil {
		return nil, err
	}

	// Scale propeller to logoPx height preserving aspect ratio.
	srcBounds := propImg.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	propH := logoPx
	propW := int(float64(propH) * float64(srcW) / float64(srcH))

	scaledProp := image.NewRGBA(image.Rect(0, 0, propW, propH))
	draw.BiLinear.Scale(scaledProp, scaledProp.Bounds(), propImg, srcBounds, draw.Over, nil)

	// Load fonts.
	nameFace, err := parseFace(gobold.TTF, nameSizePx)
	if err != nil {
		return nil, err
	}
	if c, ok := nameFace.(io.Closer); ok {
		defer func() { _ = c.Close() }()
	}

	verFace, err := parseFace(goregular.TTF, verSizePx)
	if err != nil {
		return nil, err
	}
	if c, ok := verFace.(io.Closer); ok {
		defer func() { _ = c.Close() }()
	}

	nameText := "formae"
	verText := "v" + version

	nameAdv := xfont.MeasureString(nameFace, nameText)
	verAdv := xfont.MeasureString(verFace, verText)

	nameW := nameAdv.Ceil()
	verW := verAdv.Ceil()
	textW := nameW
	if verW > textW {
		textW = verW
	}

	nameMetrics := nameFace.Metrics()
	verMetrics := verFace.Metrics()

	nameLineH := (nameMetrics.Ascent + nameMetrics.Descent).Ceil()
	verLineH := (verMetrics.Ascent + verMetrics.Descent).Ceil()
	textBlockH := nameLineH + lineGapPx + verLineH

	canvasW := propW + wordmarkGapPx + textW
	canvasH := propH
	if textBlockH > canvasH {
		canvasH = textBlockH
	}

	canvas := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
	// Transparent by default (zero-value color.RGBA has A=0).

	// Draw propeller centered vertically.
	propY := (canvasH - propH) / 2
	draw.Draw(canvas, image.Rect(0, propY, propW, propY+propH), scaledProp, image.Point{}, draw.Over)

	// Draw text block centered vertically.
	textX := propW + wordmarkGapPx
	textBlockY := (canvasH - textBlockH) / 2

	// Draw "formae" line.
	nameBaselineY := textBlockY + nameMetrics.Ascent.Ceil()
	nameDrawer := &xfont.Drawer{
		Dst:  canvas,
		Src:  image.NewUniform(colorWhite),
		Face: nameFace,
		Dot:  fixed.P(textX, nameBaselineY),
	}
	nameDrawer.DrawString(nameText)

	// Draw "v{version}" line.
	verBaselineY := nameBaselineY + nameMetrics.Descent.Ceil() + lineGapPx + verMetrics.Ascent.Ceil()
	verDrawer := &xfont.Drawer{
		Dst:  canvas,
		Src:  image.NewUniform(colorOrange),
		Face: verFace,
		Dot:  fixed.P(textX, verBaselineY),
	}
	verDrawer.DrawString(verText)

	return canvas, nil
}

// parseFace parses ttfBytes and returns a font.Face at sizePt.
func parseFace(ttfBytes []byte, sizePt float64) (xfont.Face, error) {
	f, err := opentype.Parse(ttfBytes)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size: sizePt,
		DPI:  96,
	})
}
