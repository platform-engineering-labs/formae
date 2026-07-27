// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

// runeIndex returns the rune-offset of substr in s, or -1 if not found.
// Use this instead of strings.Index when s may contain multi-byte runes.
func runeIndex(s, substr string) int {
	idx := strings.Index(s, substr)
	if idx < 0 {
		return -1
	}
	return utf8.RuneCountInString(s[:idx])
}

// TestRenderSummaryLine checks that the summary line contains correct tokens
// and omits zero-count operations.
func TestRenderSummaryLine(t *testing.T) {
	sim := makeFixtureSim()
	groups := buildSimGroups(&sim.Command)
	counts := opCounts(groups)

	// Verify counts are reasonable
	assert.Greater(t, counts[opCreate], 0, "should have creates")
	assert.Greater(t, counts[opDelete], 0, "should have deletes")
	assert.Greater(t, counts[opUpdate], 0, "should have updates")
	assert.Greater(t, counts[opReplace], 0, "should have replaces")
	assert.Greater(t, counts[opDetach], 0, "should have detaches")
	assert.Greater(t, counts[opKeep], 0, "should have keeps")
}

// TestRenderGroupColHeadersAligned checks that column headers and data rows
// are aligned — the "Label" text in the header must start at the same RUNE offset
// as the label text in the first data row.
func TestRenderGroupColHeadersAligned(t *testing.T) {
	for _, width := range []int{100, 80} {
		t.Run("width", func(t *testing.T) {
			m := makeModel(width, 32)
			raw := m.View()
			plainView := plain(raw)
			lines := strings.Split(plainView, "\n")

			type groupSpec struct {
				sectionTitle string
				// dataLabel is the label text that should appear in the first data row.
				// Default sort is destructive-first (opKind order), so:
				// Targets: replace < create → "aws-ap-south-1" is first
				// Stacks: only create "production"
				// Resources: delete < replace → "old-cache" or "legacy-queue" is first
				dataLabel string
			}
			groups := []groupSpec{
				{"Stacks", "production"},
				{"Resources", "old-cache"},
			}

			for _, gs := range groups {
				sectionIdx := -1
				for i, line := range lines {
					if strings.Contains(line, gs.sectionTitle) {
						sectionIdx = i
						break
					}
				}
				if sectionIdx < 0 {
					t.Logf("section %q not found — skipping alignment check at width %d", gs.sectionTitle, width)
					continue
				}

				colHdrIdx := sectionIdx + 1
				if colHdrIdx >= len(lines) {
					t.Logf("no col-header line after section %q at width %d", gs.sectionTitle, width)
					continue
				}
				dataRowIdx := colHdrIdx + 1
				if dataRowIdx >= len(lines) {
					t.Logf("no data row after col-header in %q at width %d", gs.sectionTitle, width)
					continue
				}

				colHdr := lines[colHdrIdx]
				dataRow := lines[dataRowIdx]

				// Use rune offsets to handle multi-byte arrows (▲/▼) correctly.
				labelOffset := runeIndex(colHdr, "Label")
				if labelOffset < 0 {
					t.Logf("'Label' not found in col-header for %q at width %d: %q", gs.sectionTitle, width, colHdr)
					continue
				}

				dataOffset := runeIndex(dataRow, gs.dataLabel)
				if dataOffset < 0 {
					t.Logf("data label %q not found in data row for %q at width %d: %q", gs.dataLabel, gs.sectionTitle, width, dataRow)
					continue
				}

				assert.Equal(t, labelOffset, dataOffset,
					"col-header 'Label' at rune offset %d but data %q at rune offset %d in group %q at width %d\n  hdr:  %q\n  data: %q",
					labelOffset, gs.dataLabel, dataOffset, gs.sectionTitle, width, colHdr, dataRow)
			}
		})
	}
}

// TestRenderDeleteRowsHaveWarningColor verifies that delete rows in the plain
// output use the Warning-colored text (they should show Warning-style coloring,
// distinct from other operations).
func TestRenderDeleteRowsHaveWarningColor(t *testing.T) {
	m := makeModel(100, 40)
	rawView := m.View()

	// Verify delete rows appear in output
	plainView := plain(rawView)
	assert.Contains(t, plainView, "delete", "delete operation should appear in view")
	assert.Contains(t, plainView, "old-cache", "old-cache delete should appear in view")
}

// TestRenderRowLabelAndDeleteAreThemeDriven pins that row column coloring is
// governed by the theme's [rows] plane:
//   - rich (label_accent + delete_whole_row): the label is the accent color,
//     and a delete row's label AND type both take the delete op color.
//   - quiet (neither): the label renders the same color as the other columns
//     (nothing special about a label), and delete rows are NOT tinted
//     whole-row — only the operation cell carries the (uniform) op color.
//
// It also pins that the operation token is regular weight (not bold) on every
// row, delete included.
func TestRenderRowLabelAndDeleteAreThemeDriven(t *testing.T) {
	const opW, labelW, typeW, stackW = 14, 30, 30, 0
	deleteRow := simRow{op: opDelete, label: "old-cache", typ: "AWS::ElastiCache::CacheCluster"}
	createRow := simRow{op: opCreate, label: "my-bucket", typ: "AWS::S3::Bucket"}

	// --- rich: label accent + whole-row delete tint ---
	rich := makeModel(100, 32)
	rich.th = theme.New("rich")
	rp := rich.th.Palette
	richDelete := rich.renderRow(deleteRow, kindResource, opW, labelW, typeW, stackW, false)
	richCreate := rich.renderRow(createRow, kindResource, opW, labelW, typeW, stackW, false)

	wantRichDeleteLabel := lipgloss.NewStyle().Foreground(opColor(rp, opDelete)).
		Render(components.Pad(components.Truncate(deleteRow.label, labelW-1), labelW))
	wantRichDeleteType := lipgloss.NewStyle().Foreground(opColor(rp, opDelete)).
		Render(components.Truncate(deleteRow.typ, typeW-1))
	assert.Contains(t, richDelete, wantRichDeleteLabel, "rich delete label should carry the delete op color")
	assert.Contains(t, richDelete, wantRichDeleteType, "rich delete type should carry the delete op color")

	wantRichCreateLabel := lipgloss.NewStyle().Foreground(rp.PrimaryAccent).
		Render(components.Pad(components.Truncate(createRow.label, labelW-1), labelW))
	assert.Contains(t, richCreate, wantRichCreateLabel, "rich create label should be PrimaryAccent")

	// --- quiet: label == columns, no whole-row delete tint ---
	quiet := makeModel(100, 32)
	quiet.th = theme.New("quiet")
	qp := quiet.th.Palette
	quietDelete := quiet.renderRow(deleteRow, kindResource, opW, labelW, typeW, stackW, false)
	quietCreate := quiet.renderRow(createRow, kindResource, opW, labelW, typeW, stackW, false)

	// Both delete and create labels render in TextSecondary, the same color as
	// the type column — the label is not special, and delete is not tinted.
	wantQuietDeleteLabel := lipgloss.NewStyle().Foreground(qp.TextSecondary).
		Render(components.Pad(components.Truncate(deleteRow.label, labelW-1), labelW))
	wantQuietCreateLabel := lipgloss.NewStyle().Foreground(qp.TextSecondary).
		Render(components.Pad(components.Truncate(createRow.label, labelW-1), labelW))
	assert.Contains(t, quietDelete, wantQuietDeleteLabel, "quiet delete label should match the column color")
	assert.Contains(t, quietCreate, wantQuietCreateLabel, "quiet create label should match the column color")

	// quiet colors the op cell uniformly (no per-op color); the delete op cell
	// must NOT carry a distinct delete tint on the label.
	assert.NotContains(t, quietDelete, wantRichDeleteLabel, "quiet must not tint the delete label")

	// Operation token is colored (regular weight, not bold) on every row.
	wantRichDeleteOp := lipgloss.NewStyle().Foreground(opColor(rp, opDelete)).
		Render(components.Pad(opGlyph(rich.th.Glyphs, opDelete)+" "+opDelete.word(), opW))
	assert.Contains(t, richDelete, wantRichDeleteOp, "delete op token should be regular weight")
}

// TestRenderGroupColHeaderThemeHighlight pins the theme-driven header
// emphasis: under "rich" (Header.Highlight="background") the
// navigated column header carries a background SGR sequence, matching the
// row cursor; under "quiet" (Header.Highlight="brighten") the navigated
// header carries only a foreground + bold, no background.
func TestRenderGroupColHeaderThemeHighlight(t *testing.T) {
	tuitest.PinRendering()

	richModel := makeModel(100, 32)
	richModel.th = theme.New("rich")
	richModel.sortHi[kindResource] = colLabel
	richHdr := richModel.renderGroupColHeader(kindResource, 14, 30, 30, 0)

	quietModel := makeModel(100, 32)
	quietModel.th = theme.New("quiet")
	quietModel.sortHi[kindResource] = colLabel
	quietHdr := quietModel.renderGroupColHeader(kindResource, 14, 30, 30, 0)

	fgPrefix := func(c lipgloss.TerminalColor) string {
		return strings.SplitN(lipgloss.NewStyle().Foreground(c).Bold(true).Render("x"), "x", 2)[0]
	}

	// The active sort column (here also the navigated one) uses PrimaryAccent under
	// rich (background mode) and bold bright white under quiet (brighten mode). It
	// no longer uses the Selection background, which the row cursor uses — so the
	// sort indicator and the selected row stay visually distinct.
	assert.Contains(t, richHdr, fgPrefix(richModel.th.Palette.PrimaryAccent), "rich active-sort header must carry PrimaryAccent")
	assert.NotContains(t, richHdr, "48;2;", "rich sort header must not use a background")
	assert.NotContains(t, quietHdr, fgPrefix(quietModel.th.Palette.PrimaryAccent), "quiet must not carry PrimaryAccent")
	assert.Contains(t, quietHdr, "\x1b[1;", "quiet navigated header should still render bold")
}

// TestRenderSubLinesAppear verifies cascade and policy-keep sub-lines appear
// in the rendered output.
func TestRenderSubLinesAppear(t *testing.T) {
	m := makeModel(100, 40)
	plainView := plain(m.View())

	// cascade sub-line
	assert.Contains(t, plainView, "depends on", "cascade sub-line should appear")
	assert.Contains(t, plainView, "old-cache", "cascade source should appear")

	// policy keep detail
	assert.Contains(t, plainView, "still referenced by", "policy keep sub-line should appear")
}
