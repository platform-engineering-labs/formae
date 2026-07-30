// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logo

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"

	xdraw "golang.org/x/image/draw"
)

// tintWordmarkLetters recolors the white "formae" letters of the logo image to
// tint while leaving the orange propeller untouched, preserving each pixel's
// alpha so antialiased edges stay smooth. It uses the same hue split as the
// braille renderer: a pixel is part of the propeller when its blue channel is
// less than half its red (orange #FF8201 has near-zero blue; the white letters
// have blue ≈ red). Fully transparent pixels are left transparent. Returns a new
// image; the input is not modified. A nil tint returns img unchanged.
func tintWordmarkLetters(img image.Image, tint color.Color) image.Image {
	if tint == nil {
		return img
	}
	tr, tg, tb, _ := tint.RGBA()
	tint8 := color.NRGBA{R: uint8(tr >> 8), G: uint8(tg >> 8), B: uint8(tb >> 8)}

	b := img.Bounds()
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, _, blue, a := img.At(x, y).RGBA()
			// Transparent or propeller (blue < half red) → copy through.
			if a == 0 || blue*2 < r {
				out.Set(x, y, img.At(x, y))
				continue
			}
			// Letter pixel → tint color at the original coverage (alpha).
			px := tint8
			px.A = uint8(a >> 8)
			out.SetNRGBA(x, y, px)
		}
	}
	return out
}

// graphicsPxPerCell approximates a terminal cell's pixel width; the propeller
// is scaled to cols*graphicsPxPerCell px so the Kitty/iTerm2 image occupies
// roughly `cols` columns instead of its full ~430px crop size.
const graphicsPxPerCell = 8

// scalePropeller downscales the cropped propeller to targetW px wide,
// preserving aspect ratio. Without this the full ~430px crop renders as a
// huge (>25-row) image in the terminal.
func scalePropeller(img image.Image, targetW int) image.Image {
	b := img.Bounds()
	if b.Dx() == 0 {
		return img
	}
	targetH := int(float64(b.Dy()) * float64(targetW) / float64(b.Dx()))
	if targetH < 1 {
		targetH = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	xdraw.CatmullRom.Scale(out, out.Bounds(), img, b, xdraw.Over, nil)
	return out
}

// Calibrated layout constants for Kitty graphics rendering.
// These values were measured in a live Kitty terminal using the natural-size
// propeller image and may need per-terminal tuning.
const (
	// graphicsFullCols is the image natural width in terminal cells (Kitty cell estimate).
	graphicsFullCols = 10
	// graphicsImageRows is the image height in terminal rows — used to move the
	// cursor below the image after text composition.
	graphicsImageRows = 3
)

// Calibrated layout constants for the FULL-WORDMARK Kitty variant (white
// "formae" letters + orange propeller rendered as one image). These are
// separate from the propeller-only constants and need live tuning in Kitty.
const (
	// graphicsFullLogoWidthPx is the full wordmark's exact rendered width in
	// pixels — the single precise size knob. Height follows proportionally
	// (cropped logo aspect h/w = 0.2145): 600px → ~129px tall, and +14px width
	// buys +3px height. Kitty renders the PNG at this natural pixel size.
	graphicsFullLogoWidthPx = 623
	// graphicsFullLogoCols is the number of terminal columns the image is pinned
	// to via the Kitty c= key. Pinning the cell footprint (together with r=,
	// graphicsFullLogoImageRows) is what makes the version placement zoom-robust:
	// the image occupies exactly graphicsFullLogoCols×graphicsFullLogoImageRows
	// cells at ANY font zoom (cell pixels scale uniformly, so the pinned footprint
	// and its aspect ratio stay constant), so the version at column
	// graphicsFullLogoCols+1 never drifts. Without pinning, the natural-size image
	// spans zoom-dependent cells and the version slides out of alignment.
	graphicsFullLogoCols = 40
	// graphicsFullLogoTextCol is the 1-based column (CHA) where the version text
	// begins — two cells past the pinned image width (graphicsFullLogoCols+2) so
	// it sits just clear of the logo's right edge.
	graphicsFullLogoTextCol = graphicsFullLogoCols + 2
	// graphicsFullLogoImageRows is the image height in terminal rows — the total
	// cursor advance needed to move fully below the image (measured live: the
	// ×2.5 image is ~4 rows tall, so advancing 4 lands just below it and the
	// banner's single trailing blank line is the only gap before the logs).
	graphicsFullLogoImageRows = 4
	// graphicsFullLogoVersionRow is the 0-based image row the version text sits on
	// (lifted above the bottom so it aligns with the wordmark, not the image edge).
	graphicsFullLogoVersionRow = 3
)

// encodeKittyFullLogo returns the Kitty APC graphics-protocol escape for the
// FULL wordmark (white "formae" letters + orange propeller), cropped to opaque
// content bounds and scaled to widthPx pixels wide. C=1 is set so the cursor
// does NOT advance after the image, leaving it at the image top-left for
// subsequent text positioning. Returns "" on decode error; does NOT write to
// stdout.
// When tint is non-nil, the white "formae" letters are recolored to it (the
// orange propeller is left as-is) so the graphics logo follows the active theme.
func encodeKittyFullLogo(dark bool, widthPx int, tint color.Color) string {
	img, err := loadFullLogoImage(dark)
	if err != nil {
		return ""
	}
	img = scalePropeller(img, widthPx)
	img = tintWordmarkLetters(img, tint)
	return kittyEncodeImage(img, graphicsFullLogoCols, graphicsFullLogoImageRows)
}

// kittyEncodeImage PNG-encodes img and returns the Kitty APC graphics escape
// sequence (a=T, f=100, C=1), chunked into 4096-byte APC payloads. C=1 keeps
// the cursor at the image top-left for subsequent text positioning. When cols
// and rows are both > 0 the image is pinned to that cell footprint via the c=/r=
// keys, so it occupies a fixed number of cells independent of font zoom (the
// basis for zoom-robust version placement); pass 0 to keep the natural size.
// Returns "" on encode error; does NOT write to stdout.
func kittyEncodeImage(img image.Image, cols, rows int) string {
	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, img); err != nil {
		return ""
	}

	encoded := base64.StdEncoding.EncodeToString(imgBuf.Bytes())

	// Kitty graphics protocol: APC G <params>;<payload> ST
	// Split payload into chunks of 4096 for large images.
	// C=1: cursor does not move after the image (stays at image top-left).
	const chunkSize = 4096

	var seq bytes.Buffer
	for i := 0; i < len(encoded); i += chunkSize {
		end := i + chunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		chunk := encoded[i:end]

		more := 1
		if end >= len(encoded) {
			more = 0
		}

		if i == 0 {
			// First chunk: a=T (transmit+display), f=100 (PNG format), C=1 (no cursor advance), m=more.
			// c=/r= pin the image to a fixed cell footprint so its size (and the
			// version text positioned beside it) stays put across font zoom.
			// q=2 suppresses ALL terminal responses: we never read the tty, so
			// any ack (iTerm2 3.6+ replies "ESC_Gi=0;OK" even without an image
			// id) would sit in the input buffer and echo into the shell as
			// stray "Gi=0,p=0;OK" text after the command exits.
			footprint := ""
			if cols > 0 && rows > 0 {
				footprint = fmt.Sprintf("c=%d,r=%d,", cols, rows)
			}
			fmt.Fprintf(&seq, "\033_Ga=T,f=100,C=1,q=2,%sm=%d;%s\033\\", footprint, more, chunk)
		} else {
			// Continuation chunks: the spec allows only m and optionally q
			// here. Kitty proper inherits q from the first chunk, but repeat
			// q=2 for implementations that don't.
			fmt.Fprintf(&seq, "\033_Gq=2,m=%d;%s\033\\", more, chunk)
		}
	}

	return seq.String()
}

// loadFullLogoImage decodes the embedded wordmark PNG and crops it to its opaque
// content bounds (trimming transparent padding).
func loadFullLogoImage(dark bool) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(logoBytes(dark)))
	if err != nil {
		return nil, err
	}
	if cropped := cropToOpaqueBounds(img); cropped != nil {
		return cropped, nil
	}
	return img, nil
}

// encodeITerm2 returns the iTerm2 inline-image escape sequence for the
// propeller image at natural size for the given theme.
// The entire PNG is base64-encoded and wrapped in a single atomic OSC 1337
// escape sequence — fragmented writes can cause terminals to misparse the
// sequence (prototype fix from commit 0e057261).
// Returns the complete escape sequence as a string; does NOT write to stdout.
func encodeITerm2(dark bool, cols int) string {
	img, err := loadAndCropPropeller(dark)
	if err != nil {
		return ""
	}
	img = scalePropeller(img, cols*graphicsPxPerCell)

	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, img); err != nil {
		return ""
	}

	rawBytes := imgBuf.Bytes()
	encoded := base64.StdEncoding.EncodeToString(rawBytes)

	// Build the complete escape sequence in a buffer to ensure atomic write.
	// Fragmented writes can cause terminals to misparse the sequence.
	// inline=1 with natural size — no forced grid, no preserveAspectRatio=0.
	var seq bytes.Buffer
	fmt.Fprintf(&seq,
		"\033]1337;File=inline=1;size=%d:%s\a",
		len(rawBytes), encoded)

	return seq.String()
}
