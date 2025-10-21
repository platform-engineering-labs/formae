// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package display

import (
	"fmt"
	"strings"

	"github.com/platform-engineering-labs/formae"
)

func combineBanners(bannerBlue, bannerGold string) string {
	linesBlue := strings.Split(bannerBlue, "\n")
	linesGold := strings.Split(bannerGold, "\n")
	maxLines := max(len(linesGold), len(linesBlue))

	var combinedLines []string
	for i := range maxLines {
		line1 := ""
		if i < len(linesBlue) {
			line1 = LightBlue(linesBlue[i])
		}

		line2 := ""
		if i < len(linesGold) {
			line2 = Gold(linesGold[i])
		}

		combinedLines = append(combinedLines, line1+line2)
	}

	return strings.Join(combinedLines, "\n")
}

var banner = combineBanners(LightBlue(BannerBlue), Gold(BannerGold))

func ClearScreen() {
	fmt.Print("\033[2J")   // Move cursor to top left
	fmt.Print("\033[2;1H") // Clear screen
}

func PrintBanner() {
	fmt.Println(strings.Replace(banner, "version", formae.Version, 1))
}

func Success(msg string) {
	fmt.Print(Green(fmt.Sprintf("%s\n", msg)))
}

func Warning(msg string) {
	fmt.Print(Gold(fmt.Sprintf("Warning: %s\n", msg)))
}

func Error(msg string) {
	fmt.Print(Red(fmt.Sprintf("Error: %s\n", msg)))
}

func DefaultLinks() string {
	return Links("Docs", "")
}

func Links(docLinkName string, deepLinkName string) string {
	deepLink := DocRoot
	if deepLinkName != "" {
		deepLink += "/" + deepLinkName
	}

	return "\n" + Gold("Code: ") + "https://github.com/platform-engineering-labs/formae" +
		"\n" + Gold(fmt.Sprintf("%s: ", docLinkName)) + deepLink +
		"\n" + Gold("Bugs: ") + "https://github.com/platform-engineering-labs/formae/issues"
}
