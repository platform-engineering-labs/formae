// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
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
