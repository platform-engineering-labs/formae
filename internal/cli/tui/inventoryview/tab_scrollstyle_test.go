// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// scrolledResourcesTab builds a resources tab whose row count far exceeds the
// table height, then moves the cursor to the last row so the viewport is
// scrolled past its first page.
func scrolledResourcesTab(t *testing.T, th *theme.Theme, rows []row) tabModel {
	t.Helper()
	tm := newTabModel(th, newSpecs(nil)[TabResources])
	tm.state = tabLoaded
	tm.allRows = rows
	tm = tm.setSize(100, 12)
	tm = tm.sync(0)

	// Walk the cursor to the last row, as delegateNav does per keypress.
	down := tea.KeyMsg{Type: tea.KeyDown}
	for i := 0; i < len(rows); i++ {
		tbl, _ := tm.table.Update(down)
		tm.table = tbl
		tm = tm.sync(0)
	}
	require.Equal(t, len(rows)-1, tm.table.Cursor(), "cursor must be on the last row")
	return tm
}

// TestScrolledView_UnmanagedStaysStyled pins that per-cell styling follows the
// rendered rows, not the absolute row index: after scrolling past the first
// page, the "⚠ unmanaged" cells that are actually on screen must still carry
// the Unmanaged style.
func TestScrolledView_UnmanagedStaysStyled(t *testing.T) {
	th := theme.New("formae")

	// 40 managed rows first (sorted before "z-"), then 5 unmanaged rows that only
	// become visible once the viewport scrolls.
	var rows []row
	for i := 0; i < 40; i++ {
		rows = append(rows, resourceRow(pkgmodel.Resource{
			NativeID: "arn:aws:s3:::bucket",
			Stack:    "production",
			Type:     "AWS::S3::Bucket",
			Label:    "a-bucket-" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
		}))
	}
	for i := 0; i < 5; i++ {
		rows = append(rows, resourceRow(pkgmodel.Resource{
			NativeID: "arn:aws:s3:::old-logs",
			Stack:    "$unmanaged",
			Type:     "AWS::S3::Bucket",
			Label:    "z-orphan-" + string(rune('a'+i)),
		}))
	}

	tm := scrolledResourcesTab(t, th, rows)
	joined := strings.Join(tm.view(th, 0, "⠋"), "\n")

	require.Contains(t, joined, "unmanaged", "precondition: unmanaged rows must be on screen")
	styled := th.Styles.Unmanaged.Render("⚠ unmanaged")
	plainCount := strings.Count(joined, "⚠ unmanaged")
	assert.Equal(t, plainCount, strings.Count(joined, styled),
		"every on-screen '⚠ unmanaged' cell must carry the Unmanaged style after scrolling")
}

// TestScrolledView_CursorRowStaysHighlighted pins that the full-width cursor
// bar survives scrolling past the first page.
func TestScrolledView_CursorRowStaysHighlighted(t *testing.T) {
	th := theme.New("formae")
	rows := buildFixtureResources(40)

	tm := scrolledResourcesTab(t, th, rows)
	lines := tm.view(th, 0, "⠋")

	probe := lipgloss.NewStyle().Background(th.Palette.Selection).Render("x")
	bgSGR := probe[:strings.IndexByte(probe, 'm')]
	found := false
	for _, l := range lines {
		if strings.Contains(l, bgSGR) {
			found = true
			break
		}
	}
	assert.True(t, found, "cursor row must keep the Selection background after scrolling")
}
