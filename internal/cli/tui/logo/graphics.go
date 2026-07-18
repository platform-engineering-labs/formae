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

	"golang.org/x/image/draw"
)

// encodeKitty returns the Kitty APC graphics-protocol escape sequence for the
// embedded logo (dark or light variant) scaled to a fixed cellCols×cellRows
// character cell grid. The c=<cols>,r=<rows> parameters instruct Kitty to
// render the image into exactly that many cells, giving a known footprint.
// The PNG is base64-encoded and split into 4096-byte chunks within APC payloads
// per the Kitty protocol specification (a=T f=100 m=more).
// Returns the complete escape sequence as a string; does NOT write to stdout.
func encodeKitty(dark bool, cellCols, cellRows int) string {
	img, err := loadAndScaleForGraphics(dark, cellCols)
	if err != nil {
		return ""
	}

	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, img); err != nil {
		return ""
	}

	encoded := base64.StdEncoding.EncodeToString(imgBuf.Bytes())

	// Kitty graphics protocol: APC G <params>;<payload> ST
	// Split payload into chunks of 4096 for large images.
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
			// First chunk: a=T (transmit+display), f=100 (PNG format),
			// c=<cols>,r=<rows> fixes the cell footprint, m=more.
			fmt.Fprintf(&seq, "\033_Ga=T,f=100,c=%d,r=%d,m=%d;%s\033\\", cellCols, cellRows, more, chunk)
		} else {
			// Continuation chunks
			fmt.Fprintf(&seq, "\033_Gm=%d;%s\033\\", more, chunk)
		}
	}

	return seq.String()
}

// encodeITerm2 returns the iTerm2 inline-image escape sequence for the embedded
// logo (dark or light variant) scaled to a fixed cellCols×cellRows character
// cell grid. width=<cols>;height=<rows> (no px/% suffix = cell units) with
// preserveAspectRatio=0 instructs iTerm2 to fill exactly that grid.
// The entire PNG is base64-encoded and wrapped in a single atomic OSC 1337
// escape sequence — fragmented writes can cause terminals to misparse the
// sequence (prototype fix from commit 0e057261).
// Returns the complete escape sequence as a string; does NOT write to stdout.
func encodeITerm2(dark bool, cellCols, cellRows int) string {
	img, err := loadAndScaleForGraphics(dark, cellCols)
	if err != nil {
		return ""
	}

	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, img); err != nil {
		return ""
	}

	rawBytes := imgBuf.Bytes()
	encoded := base64.StdEncoding.EncodeToString(rawBytes)

	// Build the complete escape sequence in a buffer to ensure atomic write.
	// Fragmented writes can cause terminals to misparse the sequence.
	// width/height without units = cell count; preserveAspectRatio=0 fills grid.
	var seq bytes.Buffer
	fmt.Fprintf(&seq,
		"\033]1337;File=inline=1;size=%d;width=%d;height=%d;preserveAspectRatio=0:%s\a",
		len(rawBytes), cellCols, cellRows, encoded)

	return seq.String()
}

// loadAndScaleForGraphics decodes the embedded logo PNG (dark or light variant),
// crops the propeller icon, and scales it to fit cellCols character columns.
// Assumes each character cell is approximately pixelsPerCell wide.
func loadAndScaleForGraphics(dark bool, cellCols int) (image.Image, error) {
	img, err := loadAndCropPropeller(dark)
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	// Approximate pixel width from character columns (typical cell ~8px wide).
	const pixelsPerCell = 8
	widthPx := cellCols * pixelsPerCell
	heightPx := int(float64(widthPx) * float64(srcH) / float64(srcW))

	resized := image.NewRGBA(image.Rect(0, 0, widthPx, heightPx))
	draw.BiLinear.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)

	return resized, nil
}
