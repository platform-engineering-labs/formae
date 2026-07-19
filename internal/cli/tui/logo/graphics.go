// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logo

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"

	xdraw "golang.org/x/image/draw"
)

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
	// graphicsFullLogoTextCol is the 1-based column (CHA) where the version text
	// begins — past the image's rendered cell width plus a small gap.
	graphicsFullLogoTextCol = 41
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
func encodeKittyFullLogo(dark bool, widthPx int) string {
	img, err := loadFullLogoImage(dark)
	if err != nil {
		return ""
	}
	img = scalePropeller(img, widthPx)
	return kittyEncodeImage(img)
}

// kittyEncodeImage PNG-encodes img and returns the Kitty APC graphics escape
// sequence (a=T, f=100, C=1), chunked into 4096-byte APC payloads. C=1 keeps
// the cursor at the image top-left for subsequent text positioning. Returns ""
// on encode error; does NOT write to stdout.
func kittyEncodeImage(img image.Image) string {
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
			// No c=/r= forcing — natural size preserves aspect ratio correctly.
			fmt.Fprintf(&seq, "\033_Ga=T,f=100,C=1,m=%d;%s\033\\", more, chunk)
		} else {
			// Continuation chunks
			fmt.Fprintf(&seq, "\033_Gm=%d;%s\033\\", more, chunk)
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
