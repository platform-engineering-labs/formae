// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Prototype: Logo rendering — braille art
// Run: go run ./docs/mockups/prototypes/logo/
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	_ "image/png"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"golang.org/x/image/draw"
)

const (
	defaultLogoPath = "/mnt/c/Users/wfhso/Downloads/Formae_Logo_dark.png"

	// Crop region for the propeller icon (right side of the 2134x556 image)
	cropX = 1550
	cropY = 0
	cropW = 584
	cropH = 556
)

func main() {
	th := theme.New("formae")
	p := th.Palette

	img, err := loadAndCropPropeller()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading image: %v\n", err)
		os.Exit(1)
	}

	title := lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true)
	version := lipgloss.NewStyle().Foreground(p.SecondaryAccent)
	border := lipgloss.NewStyle().Foreground(p.Border)

	fmt.Println()

	// Graphics protocol rendering
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--iterm":
			fmt.Println(title.Render("  iTerm2 inline images (Ghostty, iTerm2):"))
			fmt.Println()
			if err := renderITerm(img, 200); err != nil {
				fmt.Println(lipgloss.NewStyle().Foreground(p.TextSecondary).Render("  " + err.Error()))
			}
			fmt.Println()
		case "--kitty":
			fmt.Println(title.Render("  Kitty graphics protocol (WezTerm, Kitty):"))
			fmt.Println()
			if err := renderKitty(img, 200); err != nil {
				fmt.Println(lipgloss.NewStyle().Foreground(p.TextSecondary).Render("  " + err.Error()))
			}
			fmt.Println()
		}
	}

	// Show braille at multiple sizes
	sizes := []struct {
		name  string
		width int
	}{
		{"Full banner (20 chars wide)", 20},
		{"Medium (14 chars wide)", 14},
		{"Compact (8 chars wide)", 8},
		{"Tiny (5 chars wide)", 5},
	}

	for _, size := range sizes {
		fmt.Println(title.Render(fmt.Sprintf("  %s:", size.name)))
		fmt.Println()

		braille := renderBraille(img, size.width, p.SecondaryAccent)
		lines := strings.Split(braille, "\n")
		mid := len(lines) / 2
		for i, line := range lines {
			extra := ""
			if i == mid-1 {
				extra = "  " + title.Render("formae")
			} else if i == mid {
				extra = "  " + version.Render("v0.82.2")
			}
			fmt.Println("    " + line + extra)
		}
		fmt.Println()
	}

	// In context: as a TUI header
	fmt.Println(title.Render("  In context — TUI header bar:"))
	fmt.Println()

	w := 78
	compact := renderBraille(img, 5, p.SecondaryAccent)
	compactLines := strings.Split(compact, "\n")

	// Remove empty trailing lines
	for len(compactLines) > 0 && strings.TrimSpace(compactLines[len(compactLines)-1]) == "" {
		compactLines = compactLines[:len(compactLines)-1]
	}

	top := border.Render("  ┌" + strings.Repeat("─", w) + "┐")
	bot := border.Render("  └" + strings.Repeat("─", w) + "┘")

	fmt.Println(top)
	for i, line := range compactLines {
		var right string
		if i == len(compactLines)/2 {
			right = title.Render("formae status command") +
				padTo(w, " "+line+"  "+title.Render("formae status command"), "↻ live  ") +
				lipgloss.NewStyle().Foreground(p.PrimaryAccent).Render("↻ live")
		}
		content := " " + line
		if right != "" {
			content += "  " + right
		}
		// Pad to width
		visible := lipgloss.Width(content)
		if visible < w {
			content += strings.Repeat(" ", w-visible)
		}
		fmt.Println(border.Render("  │") + content + border.Render("│"))
	}
	fmt.Println(bot)

	fmt.Println()

	// In context: before apply simulation (non-TUI banner)
	fmt.Println(title.Render("  In context — print-and-exit banner:"))
	fmt.Println()

	banner := renderBraille(img, 10, p.SecondaryAccent)
	bannerLines := strings.Split(banner, "\n")
	for len(bannerLines) > 0 && strings.TrimSpace(bannerLines[len(bannerLines)-1]) == "" {
		bannerLines = bannerLines[:len(bannerLines)-1]
	}

	mid := len(bannerLines) / 2
	for i, line := range bannerLines {
		extra := ""
		if i == mid-1 {
			extra = "  " + title.Render("formae") + " " + version.Render("v0.82.2")
		} else if i == mid {
			extra = "  " + lipgloss.NewStyle().Foreground(p.TextSubtle).Render("Infrastructure as Code")
		}
		fmt.Println("  " + line + extra)
	}
	fmt.Println()
}

func padTo(totalWidth int, left, right string) string {
	space := totalWidth - lipgloss.Width(left) - lipgloss.Width(right)
	if space < 1 {
		return " "
	}
	return strings.Repeat(" ", space)
}

func getLogoPath() string {
	for _, arg := range os.Args[1:] {
		if !strings.HasPrefix(arg, "--") {
			return arg
		}
	}
	return defaultLogoPath
}

func loadAndCropPropeller() (image.Image, error) {
	path := getLogoPath()
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
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

func renderBraille(img image.Image, widthChars int, fgColor lipgloss.AdaptiveColor) string {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	// Each braille char is 2 dots wide, 4 dots tall
	dotW := widthChars * 2
	dotH := int(float64(dotW) * float64(srcH) / float64(srcW))
	dotH = ((dotH + 3) / 4) * 4
	heightChars := dotH / 4

	resized := image.NewRGBA(image.Rect(0, 0, dotW, dotH))
	draw.BiLinear.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)

	style := lipgloss.NewStyle().Foreground(fgColor)

	var sb strings.Builder
	for cy := 0; cy < heightChars; cy++ {
		for cx := 0; cx < widthChars; cx++ {
			ch := encodeBrailleCell(resized, cx*2, cy*4)
			if ch == '\u2800' {
				sb.WriteRune(' ')
			} else {
				sb.WriteString(style.Render(string(ch)))
			}
		}
		if cy < heightChars-1 {
			sb.WriteRune('\n')
		}
	}

	return sb.String()
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

func encodeBrailleCell(img *image.RGBA, px, py int) rune {
	var ch rune = '\u2800'
	bounds := img.Bounds()

	for row := 0; row < 4; row++ {
		for col := 0; col < 2; col++ {
			x := px + col
			y := py + row
			if x >= bounds.Max.X || y >= bounds.Max.Y {
				continue
			}
			_, _, _, a := img.At(x, y).RGBA()
			if a < 0x8000 {
				continue // transparent pixel
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

// renderITerm renders an image using the iTerm2 inline images protocol.
// Supported by: iTerm2, Ghostty
func renderITerm(img image.Image, widthPx int) error {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	heightPx := int(float64(widthPx) * float64(srcH) / float64(srcW))

	resized := image.NewRGBA(image.Rect(0, 0, widthPx, heightPx))
	draw.BiLinear.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)

	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, resized); err != nil {
		return fmt.Errorf("png encode: %w", err)
	}

	rawBytes := imgBuf.Bytes()
	encoded := base64.StdEncoding.EncodeToString(rawBytes)

	// Build the complete escape sequence in a buffer to ensure atomic write.
	// Fragmented writes can cause terminals to misparse the sequence.
	var seq bytes.Buffer
	fmt.Fprintf(&seq, "\033]1337;File=inline=1;size=%d:%s\a", len(rawBytes), encoded)

	// Single atomic write to stdout
	_, err := os.Stdout.Write(seq.Bytes())
	if err != nil {
		return err
	}
	fmt.Println()
	return nil
}

// renderKitty renders an image using the Kitty graphics protocol.
// Supported by: WezTerm (with enable_kitty_graphics), Kitty
// Uses APC (Application Program Command) escape sequences.
// Requires tmux allow-passthrough if running inside tmux.
func renderKitty(img image.Image, widthPx int) error {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	heightPx := int(float64(widthPx) * float64(srcH) / float64(srcW))

	resized := image.NewRGBA(image.Rect(0, 0, widthPx, heightPx))
	draw.BiLinear.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)

	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, resized); err != nil {
		return fmt.Errorf("png encode: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(imgBuf.Bytes())

	// Kitty graphics protocol: APC G <params>;<payload> ST
	// Split payload into chunks of 4096 for large images
	chunkSize := 4096

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
			// First chunk: a=T (transmit+display), f=100 (PNG format), m=more
			fmt.Fprintf(&seq, "\033_Ga=T,f=100,m=%d;%s\033\\", more, chunk)
		} else {
			// Continuation chunks
			fmt.Fprintf(&seq, "\033_Gm=%d;%s\033\\", more, chunk)
		}
	}

	// Atomic write
	_, err := os.Stdout.Write(seq.Bytes())
	if err != nil {
		return err
	}
	fmt.Println()
	return nil
}
