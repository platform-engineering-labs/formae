// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logo

import (
	"bytes"
	"image"
	_ "image/png"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/image/draw"
)

const (
	// Crop region for the propeller icon (right side of the ~2134×556 wordmark image).
	cropX = 1642
	cropY = 64
	cropW = 430
	cropH = 429

	// brandOrange is the brand color used for braille rendering.
	brandOrange = "#FF8201"
)

// renderBraille decodes the embedded logo PNG (dark or light variant), crops
// the propeller icon, downscales it to widthChars braille columns, and returns
// the braille art string styled in brand orange.
func renderBraille(dark bool, widthChars int) string {
	img, err := loadAndCropPropeller(dark)
	if err != nil {
		return ""
	}
	return encodeBrailleArt(img, widthChars)
}

// loadAndCropPropeller decodes logoBytes(dark) and crops to the propeller rect.
func loadAndCropPropeller(dark bool) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(logoBytes(dark)))
	if err != nil {
		return nil, err
	}

	cropRect := image.Rect(cropX, cropY, cropX+cropW, cropY+cropH)
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := img.(subImager); ok {
		return si.SubImage(cropRect), nil
	}

	cropped := image.NewRGBA(image.Rect(0, 0, cropW, cropH))
	draw.Copy(cropped, image.Point{}, img, cropRect, draw.Src, nil)
	return cropped, nil
}

// encodeBrailleArt downscales img to widthChars braille columns and returns
// the styled braille string.
func encodeBrailleArt(img image.Image, widthChars int) string {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	dotW := widthChars * 2
	dotH := int(float64(dotW) * float64(srcH) / float64(srcW))
	dotH = ((dotH + 3) / 4) * 4
	heightChars := dotH / 4

	resized := image.NewRGBA(image.Rect(0, 0, dotW, dotH))
	draw.BiLinear.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)

	style := lipgloss.NewStyle().Foreground(lipgloss.Color(brandOrange))

	rows := make([]string, 0, heightChars)
	for cy := 0; cy < heightChars; cy++ {
		var row strings.Builder
		for cx := 0; cx < widthChars; cx++ {
			ch := encodeBrailleCell(resized, cx*2, cy*4)
			if ch == '⠀' {
				row.WriteRune(' ')
			} else {
				row.WriteString(style.Render(string(ch)))
			}
		}
		rows = append(rows, row.String())
	}

	// Strip trailing all-blank rows (cells that rendered as spaces only).
	for len(rows) > 0 {
		last := rows[len(rows)-1]
		trimmed := strings.TrimSpace(last)
		// Also trim any ANSI-styled blank braille (the style.Render of '⠀' is
		// replaced by a space above, so plain TrimSpace is sufficient).
		if trimmed == "" {
			rows = rows[:len(rows)-1]
		} else {
			break
		}
	}

	return strings.Join(rows, "\n")
}

// Braille dot bit positions:
//
//	[0x01] [0x08]   row 0
//	[0x02] [0x10]   row 1
//	[0x04] [0x20]   row 2
//	[0x40] [0x80]   row 3
var dotBits = [4][2]rune{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// encodeBrailleCell converts a 2×4 pixel block to a single braille rune.
func encodeBrailleCell(img *image.RGBA, px, py int) rune {
	ch := '⠀'
	bounds := img.Bounds()

	for row := 0; row < 4; row++ {
		for col := 0; col < 2; col++ {
			x := px + col
			y := py + row
			if x >= bounds.Max.X || y >= bounds.Max.Y {
				continue
			}
			_, _, _, a := img.At(x, y).RGBA()
			// Bilinear downscaling encodes stroke coverage in alpha; a 50%
			// cutoff eats thin strokes at small sizes, so accept ~30%.
			if a < 0x5000 {
				continue
			}
			r, g, b, _ := img.At(x, y).RGBA()
			brightness := (r + g + b) / 3
			if brightness < 0xD000 {
				ch |= dotBits[row][col]
			}
		}
	}
	return ch
}
