//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// headerLine returns the rendered column-header row (the one carrying the
// "NativeID" title), isolating it from data rows whose labels are also
// accent-colored.
func headerLine(lines []string) string {
	for _, ln := range lines {
		if strings.Contains(ln, "NativeID") {
			return ln
		}
	}
	return ""
}

// TestInventoryHeader_RichThreeState locks the rich header treatment (matching
// simview): the sorted column renders in the accent color and the navigated
// column gets a background band, so left/right column navigation is visible.
func TestInventoryHeader_RichThreeState(t *testing.T) {
	rich := theme.New("rich")
	tab := makeTab(rich, buildFixtureResources(5))
	tab = tab.setSize(goldenWidth, goldenHeight)
	tab = tab.sync(0)
	tab.sortCol = 0 // Label is the sorted column
	tab.sortHi = 1  // Stack is the navigated column

	hdr := headerLine(tab.view(rich, 0, "⠋"))
	if hdr == "" {
		t.Fatal("could not find the column-header line")
	}
	const richBlue = "38;2;96;165;250" // rich PrimaryAccent — the sorted column
	assert.Contains(t, hdr, richBlue, "rich sorted-column header must be accent blue")
	assert.Contains(t, hdr, "48;2;", "rich navigated-column header must carry a background band")
}

// TestInventoryHeader_QuietBrighten confirms the brighten path (quiet) uses no
// per-column background band — only brightness distinguishes the columns.
func TestInventoryHeader_QuietBrighten(t *testing.T) {
	quiet := theme.New("quiet")
	tab := makeTab(quiet, buildFixtureResources(5))
	tab = tab.setSize(goldenWidth, goldenHeight)
	tab = tab.sync(0)
	tab.sortCol = 0
	tab.sortHi = 1

	hdr := headerLine(tab.view(quiet, 0, "⠋"))
	if hdr == "" {
		t.Fatal("could not find the column-header line")
	}
	assert.NotContains(t, hdr, "48;2;", "quiet header must not use a background band (brighten mode)")
}
