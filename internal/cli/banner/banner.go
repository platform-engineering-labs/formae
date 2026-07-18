// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package banner provides the formae ASCII banner, constants, and related
// terminal helpers. It is a leaf package with no import cycles.
package banner

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

const (
	// Tool is the canonical CLI tool name.
	Tool = "formae"

	// DocRoot is the root URL for online documentation.
	DocRoot = "https://docs.formae.io/en/latest"

	// BannerBlue is the blue portion of the ASCII art banner.
	BannerBlue = `
   oooooo      ooooo            oooo     oooo     ooooo
 0oo       o0o0     oo0o      o0o      0oo   ooo0o    0o0
ooo       0oo          o0    0o0      ooo     ooo      oo0
ooo      oo0           oo0   oo       oo       oo       oo
oooooo0  ooo            oo   oo       oo       oo       oo
ooo       oo           0oo   oo       oo       oo       oo
ooo        0oo       o0o     oo       oo       oo       oo
ooo           o00000oo       oo       oo       o0       oo
`

	// BannerGold is the gold portion of the ASCII art banner.
	BannerGold = `
  ooo     oooo
0o   00 0o   00o
o0    0oo     oo
      o00  o0oo
  o00 ooo
00    o00    o0o
o0   oooo0   ooo
 0000o   0000o      vversion
`
)

var (
	th           = theme.New("formae")
	blueStyle    = lipgloss.NewStyle().Foreground(th.Palette.PrimaryAccent)
	goldStyle    = lipgloss.NewStyle().Foreground(th.Palette.Warning)
	cachedBanner = buildBanner()
)

func buildBanner() string {
	linesBlue := strings.Split(BannerBlue, "\n")
	linesGold := strings.Split(BannerGold, "\n")
	maxLines := max(len(linesGold), len(linesBlue))

	var combinedLines []string
	for i := range maxLines {
		line1 := ""
		if i < len(linesBlue) {
			line1 = blueStyle.Render(linesBlue[i])
		}
		line2 := ""
		if i < len(linesGold) {
			line2 = goldStyle.Render(linesGold[i])
		}
		combinedLines = append(combinedLines, line1+line2)
	}
	return strings.Join(combinedLines, "\n")
}

// ClearScreen clears the terminal screen.
func ClearScreen() {
	fmt.Print("\033[2J")   // Clear screen
	fmt.Print("\033[2;1H") // Move cursor to top left
}

// PrintBanner prints the formae ASCII art banner with the current version.
func PrintBanner() {
	fmt.Println(strings.Replace(cachedBanner, "version", formae.Version, 1))
}

// DefaultLinks returns the formatted links block used in help templates.
func DefaultLinks() string {
	return "\n" + goldStyle.Render("Code: ") + "https://github.com/platform-engineering-labs/formae" +
		"\n" + goldStyle.Render("Docs: ") + DocRoot +
		"\n" + goldStyle.Render("Bugs: ") + "https://github.com/platform-engineering-labs/formae/issues"
}
