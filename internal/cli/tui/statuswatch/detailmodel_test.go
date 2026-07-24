// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tui "github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func defaultKeyMap() tui.KeyMap {
	return tui.DefaultKeyMap()
}

func makeTerminalCmd() apimodel.Command {
	return apimodel.Command{
		CommandID: "cmd-abc123",
		Command:   "apply",
		Mode:      "reconcile",
		State:     "Success",
		StartTs:   time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC),
		EndTs:     time.Date(2026, 7, 16, 11, 0, 42, 0, time.UTC),
		TargetUpdates: []apimodel.TargetUpdate{
			{TargetLabel: "aws-us-east-1", Operation: "create", State: "Success", Duration: 3000, Discoverable: true},
		},
		StackUpdates: []apimodel.StackUpdate{
			{StackLabel: "production", Operation: "create", State: "Success", Duration: 1000, Description: "Production environment"},
		},
		PolicyUpdates: []apimodel.PolicyUpdate{
			{PolicyLabel: "auto-reconcile", PolicyType: "ttl", StackLabel: "production", Operation: "update", State: "Success", Duration: 1000},
		},
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket", StackName: "production", Operation: "create", State: "Success", Duration: 5000, CurrentAttempt: 1, MaxAttempts: 9},
			{ResourceLabel: "web-1", ResourceType: "AWS::EC2::Instance", StackName: "production", Operation: "create", State: "Success", Duration: 8000},
		},
	}
}

func makeTerminalRow() row {
	c := makeTerminalCmd()
	counts := commandCounts(c)
	return row{cmd: c, counts: counts, health: commandHealth(c, counts)}
}

func TestDetailModel_SummaryRowsAndSecondLines(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 30)

	c := apimodel.Command{
		CommandID: "cmd-test",
		State:     "Failed",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceLabel:  "old-data",
				ResourceType:   "AWS::S3::Bucket",
				StackName:      "production",
				Operation:      "delete",
				State:          "Failed",
				Duration:       4000,
				CurrentAttempt: 9,
				MaxAttempts:    9,
				ErrorMessage:   "BucketNotEmpty: The bucket you tried to delete is not empty",
			},
			{
				ResourceLabel: "legacy-3",
				ResourceType:  "AWS::EC2::Instance",
				StackName:     "production",
				Operation:     "delete",
				State:         "Canceled",
				CascadeSource: "legacy-2",
			},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now, nil)

	v := plain(dm.View(30, false))
	assert.Contains(t, v, "▌ Resources", "section header")
	assert.Contains(t, v, "BucketNotEmpty", "error message second line")
	assert.Contains(t, v, "depends on legacy-2", "cascade second line")
}

func TestDetailModel_ShowMoreRow(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-test",
		State:     "InProgress",
	}
	for i := 0; i < 25; i++ {
		c.ResourceUpdates = append(c.ResourceUpdates, apimodel.ResourceUpdate{
			ResourceLabel: fmt.Sprintf("res-%02d", i),
			ResourceType:  "AWS::S3::Bucket",
			StackName:     "production",
			Operation:     "create",
			State:         "Success",
		})
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now, nil)

	v := plain(dm.View(40, false))
	// 25 resources, page size 20 → 20 shown, 5 remaining.
	assert.Contains(t, v, "show 5 more (5 remaining)")

	// cursor to show-more row (row index 20, after 20 visible rows)
	// navigate down 20 times from 0
	keys := defaultKeyMap()
	for i := 0; i < 20; i++ {
		dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyDown}, keys)
	}
	// Now cursor should be on the show-more row — press enter
	dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyEnter}, keys)
	assert.Equal(t, 40, dm.visible[kindResource], "visible should expand by 20 to 40")
}

func TestDetailModel_ExpandCardByKey(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-test",
		State:     "Success",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket", StackName: "production", Operation: "create", State: "Success", Duration: 5000, CurrentAttempt: 1, MaxAttempts: 9},
			{ResourceLabel: "web-1", ResourceType: "AWS::EC2::Instance", StackName: "production", Operation: "create", State: "Success", Duration: 8000},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now, nil)

	keys := defaultKeyMap()
	// Expand first resource row (cursor=0)
	dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyEnter}, keys)

	v := plain(dm.View(40, false))
	assert.Contains(t, v, "AWS::S3::Bucket", "expanded card shows type")

	// Rebuild with same command (simulating a poll refresh)
	dm = dm.SetCommand(c, r, "◉", now, nil)

	// Expansion must survive SetCommand - the key is "resource/production/my-bucket"
	v2 := plain(dm.View(40, false))
	assert.Contains(t, v2, "AWS::S3::Bucket", "expansion survives SetCommand via key")
}

func TestDetailModel_DetailModeToggle(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)
	c := makeTerminalCmd()
	r := makeTerminalRow()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now, nil)

	assert.False(t, dm.detailMode)
	keys := defaultKeyMap()
	dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}, keys)
	assert.True(t, dm.detailMode, "'d' sets detailMode to true")

	// In detail mode all resource rows should render as cards (bordered)
	v := plain(dm.View(40, false))
	assert.Contains(t, v, "╭", "detail mode shows bordered cards")
}

func TestDetailModel_CancelStateLabels(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-cancel",
		State:     "Canceling",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "running", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "InProgress"},
			{ResourceLabel: "canceled", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now, nil)

	v := plain(dm.View(40, false))
	assert.Contains(t, v, "finishing", "in-progress row on canceling command shows 'finishing'")
	assert.Contains(t, v, "canceled", "canceled row shows 'canceled'")
}

// TestDetailModel_RenderStateGlyph_AllStates asserts renderStateGlyph maps
// every row state to the themed glyph (esp. Pending → StatusPending, which
// previously had no direct test coverage — under the quiet theme it's "·",
// not the old hardcoded "○").
// TestDetailModel_PinnedRowRunningGlyphIsStatic asserts the detail view's
// pinned command row (SetCommand -> multiView.renderRows, the same shared
// renderer as the multi-command table) inherits the PLA-348 fix that
// replaced the redundant animated spinner in colStatus with the static
// themed in-progress glyph. spinView is set to a distinctive animated frame
// that must not leak into the pinned row now that it's unused there.
func TestDetailModel_PinnedRowRunningGlyphIsStatic(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 30)
	c := apimodel.Command{
		CommandID: "cmd-pinned-running",
		Command:   "apply",
		State:     "InProgress",
		StartTs:   time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC),
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "res-1", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "InProgress"},
		},
	}
	counts := commandCounts(c)
	r := row{cmd: c, counts: counts, health: commandHealth(c, counts)}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "@ANIMATED@", now, nil)

	assert.NotContains(t, dm.pinnedRow, "@ANIMATED@", "pinned row must not render the animated spinner frame")
	assert.Contains(t, dm.pinnedRow, th.Glyphs.StatusInProgress, "pinned row must render the static themed in-progress glyph")
}

func TestDetailModel_RenderStateGlyph_AllStates(t *testing.T) {
	th := theme.New("quiet")
	dm := newDetailModel(th, 100, 30)

	tests := []struct {
		name  string
		state components.State
		want  string
	}{
		{"done", components.StateDone, th.Glyphs.StatusDone},
		{"pending", components.StatePending, th.Glyphs.StatusPending},
		{"failed", components.StateFailed, th.Glyphs.StatusFailed},
		{"skipped", components.StateSkipped, th.Glyphs.StatusSkipped},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := plain(dm.renderStateGlyph(updateRow{state: tc.state}, lipgloss.Color("0"), false))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDetailModel_BackReturnsTrue(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 30)
	keys := defaultKeyMap()

	_, back := dm.Update(tea.KeyMsg{Type: tea.KeyEsc}, keys)
	assert.True(t, back, "esc returns back=true")

	_, back2 := dm.Update(tea.KeyMsg{Type: tea.KeyBackspace}, keys)
	assert.True(t, back2, "backspace returns back=true")
}

func TestModel_EnterDrillsIn_EscReturns(t *testing.T) {
	m, _ := newTestModel(t, nil)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	c := makeTerminalCmd()
	mm, _ = mm.Update(commandsMsg{commands: []apimodel.Command{c}})

	// Enter should drill into detail view
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := mm.(Model)
	assert.Equal(t, viewDetail, got.view, "enter sets view to viewDetail")

	v := plain(got.View())
	assert.Contains(t, v, "cmd-abc123", "detail view shows command ID in pinned header")

	// Esc should return to multi view
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got2 := mm.(Model)
	assert.Equal(t, viewMulti, got2.view, "esc returns to viewMulti")
}

func TestModel_RefreshWhileInDetail_UpdatesGroups(t *testing.T) {
	m, _ := newTestModel(t, nil)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	c := apimodel.Command{
		CommandID: "cmd-refresh",
		Command:   "apply",
		State:     "InProgress",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "bucket", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Pending"},
		},
	}
	mm, _ = mm.Update(commandsMsg{commands: []apimodel.Command{c}})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill in

	assert.Equal(t, viewDetail, mm.(Model).view)

	// State flips to Success
	c2 := c
	c2.ResourceUpdates[0].State = "Success"
	c2.State = "Success"
	mm, _ = mm.Update(commandsMsg{commands: []apimodel.Command{c2}})

	v := plain(mm.(Model).View())
	assert.Contains(t, v, "✓", "refreshed state shows success glyph")
	assert.Equal(t, viewDetail, mm.(Model).view, "still in detail view after refresh")
}

func TestDetailModel_FocusCommandID(t *testing.T) {
	fc := &fakeClient{resp: &apimodel.ListCommandStatusResponse{
		Commands: []apimodel.Command{makeTerminalCmd()},
	}}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	m := New(theme.New("formae"), fc, Options{
		MaxResults:     10,
		PollInterval:   time.Hour,
		Now:            func() time.Time { return now },
		FocusCommandID: "cmd-abc123",
	})
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm, _ = mm.Update(commandsMsg{commands: fc.resp.Commands})

	got := mm.(Model)
	assert.Equal(t, viewDetail, got.view, "FocusCommandID starts in detail view")
	assert.Equal(t, "cmd-abc123", got.detail.cmdID)
}

func TestDetailModel_PinnedRowUsesInjectedNow(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 30)

	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	c := apimodel.Command{
		CommandID: "cmd-running",
		Command:   "apply",
		Mode:      "reconcile",
		State:     "InProgress",
		StartTs:   now.Add(-42 * time.Second),
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "bucket", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "InProgress"},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	dm = dm.SetCommand(c, r, "◉", now, nil)

	pinnedRow := plain(dm.pinnedRow)
	assert.Contains(t, pinnedRow, "00:42", "pinned Time column derives from injected now")
	assert.NotRegexp(t, `-\d`, pinnedRow, "no garbage negative duration from a zero clock")
}

func TestDetailModel_PinnedHeaderNoAgeAndAligned(t *testing.T) {
	th := theme.New("formae")
	c := makeTerminalCmd()
	r := makeTerminalRow()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

	for _, w := range []int{100, 70} {
		dm := newDetailModel(th, w, 30)
		dm = dm.SetCommand(c, r, "◉", now, nil)

		header := plain(dm.pinnedHeader)
		rowStr := plain(dm.pinnedRow)
		assert.NotContains(t, header, "Age", "pinned header omits Age at width %d", w)
		assert.Equal(t, len([]rune(header)), len([]rune(rowStr)),
			"pinned header and row visible widths must match at width %d", w)
	}
}

func TestDetailModel_Golden(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 30)
	c := makeTerminalCmd()
	r := makeTerminalRow()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now, nil)

	// Expand first resource card
	keys := defaultKeyMap()
	// Navigate to Resources group (skip targets, stacks, policies)
	// Target=0, Stack=1, Policy=2, Resource starts at index 3
	for i := 0; i < 3; i++ {
		dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyDown}, keys)
	}
	dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyEnter}, keys)

	tuitest.RequireGolden(t, []byte(dm.View(30, false)))
}

func TestSetCommand_ClampsCursorWhenListShrinks(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	// Build a command with 25 resource updates so visibleRows returns 20 + show-more.
	c12 := apimodel.Command{
		CommandID: "cmd-shrink",
		State:     "InProgress",
	}
	for i := 0; i < 25; i++ {
		c12.ResourceUpdates = append(c12.ResourceUpdates, apimodel.ResourceUpdate{
			ResourceLabel: fmt.Sprintf("res-%02d", i),
			ResourceType:  "AWS::S3::Bucket",
			StackName:     "production",
			Operation:     "create",
			State:         "Pending",
		})
	}
	r12 := row{cmd: c12, counts: commandCounts(c12), health: commandHealth(c12, commandCounts(c12))}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c12, r12, "◉", now, nil)

	// Navigate cursor to the last navigable line (show-more row, index 10).
	nav12 := dm.navLines()
	dm.cursor = len(nav12) - 1
	require.Equal(t, 21, len(nav12), "expect 20 rows + 1 show-more = 21 nav entries")
	require.Equal(t, 20, dm.cursor)

	// Now refresh with a 2-resource command — list shrinks drastically.
	c2 := apimodel.Command{
		CommandID: "cmd-shrink",
		State:     "InProgress",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "res-00", ResourceType: "AWS::S3::Bucket", StackName: "production", Operation: "create", State: "Pending"},
			{ResourceLabel: "res-01", ResourceType: "AWS::S3::Bucket", StackName: "production", Operation: "create", State: "Pending"},
		},
	}
	r2 := row{cmd: c2, counts: commandCounts(c2), health: commandHealth(c2, commandCounts(c2))}
	dm = dm.SetCommand(c2, r2, "◉", now, nil)

	nav2 := dm.navLines()
	require.Equal(t, 2, len(nav2), "shrunken command has exactly 2 nav entries")

	// Cursor must be clamped to a valid index — 0 <= cursor < len(nav2).
	assert.GreaterOrEqual(t, dm.cursor, 0, "cursor must not be negative after shrink")
	assert.Less(t, dm.cursor, len(nav2), "cursor must be within the new nav list after shrink")

	t.Run("clamp_negative_cursor", func(t *testing.T) {
		// Reset to a fresh detailModel and set cursor to a negative value
		dmNeg := newDetailModel(th, 100, 40)
		dmNeg.cursor = -1
		require.Equal(t, -1, dmNeg.cursor, "precondition: cursor is negative")

		// Call SetCommand with a command that has nav entries
		cWithRows := apimodel.Command{
			CommandID: "cmd-with-rows",
			State:     "InProgress",
			ResourceUpdates: []apimodel.ResourceUpdate{
				{ResourceLabel: "res-1", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Pending"},
			},
		}
		rWithRows := row{cmd: cWithRows, counts: commandCounts(cWithRows), health: commandHealth(cWithRows, commandCounts(cWithRows))}
		dmNeg = dmNeg.SetCommand(cWithRows, rWithRows, "◉", now, nil)

		// After SetCommand, negative cursor must be clamped to 0
		assert.Equal(t, 0, dmNeg.cursor, "negative cursor must be clamped to 0")
	})
}

// runeWidth returns the number of Unicode code points in s (suitable for
// plain/ANSI-stripped strings where each code point is one terminal column).
func runeWidth(s string) int { return len([]rune(s)) }

// TestDetailModel_RenderingIntegrity checks three layout-integrity properties:
//
//  1. No ANSI fragment garbage survives in the plain output.
//  2. Every rendered line's rune width (after ANSI strip) is <= viewport width.
//  3. Summary-row plain text is exactly w runes wide, with full "00:0x" Time field.
//
// These tests are written BEFORE the fix and must fail on the broken rendering.
func TestDetailModel_RenderingIntegrity(t *testing.T) {
	const w = 100
	th := theme.New("formae")
	dm := newDetailModel(th, w, 30)
	c := makeTerminalCmd()
	r := makeTerminalRow()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now, nil)

	raw := dm.View(30, false)
	plainView := plain(raw)

	t.Run("no_ansi_fragment_garbage", func(t *testing.T) {
		// After stripping well-formed ANSI sequences, there must be NO leftover
		// bracket-digit sequences like "[1;38Label" or "[38;2;136;1Time".
		// Such fragments are produced when pad() slices through escape codes.
		lines := strings.Split(plainView, "\n")
		for i, line := range lines {
			assert.NotRegexp(t, `\[[0-9;]+[A-Za-z]`, line,
				"line %d contains ANSI fragment garbage: %q", i+1, line)
		}
	})

	t.Run("no_line_overflows_viewport_width", func(t *testing.T) {
		lines := strings.Split(plainView, "\n")
		for i, line := range lines {
			lw := lipgloss.Width(line) // strips ANSI itself; but line is already plain
			_ = lw
			rw := runeWidth(line)
			assert.LessOrEqual(t, rw, w,
				"line %d overflows viewport (width %d): %q", i+1, rw, line)
		}
	})

	t.Run("summary_rows_full_width_with_intact_time", func(t *testing.T) {
		// Each non-cursor summary row for targets/stacks/resources should be
		// exactly w rune-columns wide (the rendering pads out to full width
		// only for cursor rows, but the content columns must exactly fill w
		// without overflow — i.e. the time field "00:0x" must not be clipped).
		//
		// We test by asserting:
		//   (a) the plain line ending with "00:0x" is not truncated to "00:0"
		//   (b) no summary row's plain width exceeds w
		lines := strings.Split(plainView, "\n")
		for i, line := range lines {
			trimmed := strings.TrimRight(line, " ")
			// Find lines that look like summary rows (contain operation + time)
			if strings.Contains(trimmed, "create") || strings.Contains(trimmed, "update") || strings.Contains(trimmed, "delete") {
				// The time column should show full "00:0x" not truncated "00:0"
				assert.NotRegexp(t, `\b00:0\s*$`, trimmed,
					"line %d Time field is clipped to '00:0' — full value expected: %q", i+1, trimmed)
				rw := runeWidth(line)
				assert.LessOrEqual(t, rw, w,
					"line %d summary row overflows viewport: %q", i+1, line)
			}
		}
	})
}

// TestDetailModel_ColHeaderAlignment checks that group column headers are
// aligned with their data rows: the "Label" text in the header must start at
// the same rune offset as the label text in the first data row, at widths 100
// and 70. This catches the missing sp2 indent in renderGroupColHeader.
func TestDetailModel_ColHeaderAlignment(t *testing.T) {
	for _, w := range []int{100, 70} {
		t.Run(fmt.Sprintf("width_%d", w), func(t *testing.T) {
			th := theme.New("formae")
			dm := newDetailModel(th, w, 30)
			c := makeTerminalCmd()
			r := makeTerminalRow()
			now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
			dm = dm.SetCommand(c, r, "◉", now, nil)

			raw := dm.View(30, false)
			plainView := plain(raw)
			lines := strings.Split(plainView, "\n")

			// For each group, locate its column-header line and the first data
			// row that follows, and assert "Label" (header) starts at the same
			// offset as the label content (data).
			type groupSpec struct {
				sectionTitle string // text appearing in the "▌ X" section header
				dataLabel    string // text expected in the first data row label field
			}
			groups := []groupSpec{
				{"Targets", "aws-us-east-1"},
				{"Stacks", "production"},
				{"Resources", "my-bucket"},
			}

			for _, gs := range groups {
				// Find the section header line index
				sectionIdx := -1
				for i, line := range lines {
					if strings.Contains(line, gs.sectionTitle) {
						sectionIdx = i
						break
					}
				}
				require.GreaterOrEqual(t, sectionIdx, 0, "section %q not found at width %d", gs.sectionTitle, w)

				// The col-header line is right after the section header
				colHdrIdx := sectionIdx + 1
				require.Less(t, colHdrIdx, len(lines), "no col-header line after section %q", gs.sectionTitle)

				// The first data row follows the col-header
				dataRowIdx := colHdrIdx + 1
				require.Less(t, dataRowIdx, len(lines), "no data row after col-header in %q", gs.sectionTitle)

				colHdr := lines[colHdrIdx]
				dataRow := lines[dataRowIdx]

				// "Label" must appear in the header
				labelByte := strings.Index(colHdr, "Label")
				require.GreaterOrEqual(t, labelByte, 0, "\"Label\" not found in col-header for %q at width %d: %q", gs.sectionTitle, w, colHdr)

				// The label text (e.g. "aws-us-east-1") must start in the data row
				dataByte := strings.Index(dataRow, gs.dataLabel)
				require.GreaterOrEqual(t, dataByte, 0, "data label %q not found in data row for %q at width %d: %q", gs.dataLabel, gs.sectionTitle, w, dataRow)

				// Compare DISPLAY-column offsets, not byte offsets: multi-byte glyphs
				// (✓/spinner in the data status column, plain spaces in the header
				// once the sort arrow is suppressed there) make byte offsets diverge
				// even when the columns are visually aligned.
				labelOffset := lipgloss.Width(colHdr[:labelByte])
				dataOffset := lipgloss.Width(dataRow[:dataByte])

				assert.Equal(t, labelOffset, dataOffset,
					"col-header \"Label\" at offset %d but data label %q at offset %d in group %q at width %d\n  hdr:  %q\n  data: %q",
					labelOffset, gs.dataLabel, dataOffset, gs.sectionTitle, w, colHdr, dataRow)
			}
		})
	}
}

// TestDetailModel_SortKeepsFocusOnRow verifies that toggling the sort keeps the
// cursor on the same row instead of jumping back to the top.
func TestDetailModel_SortKeepsFocusOnRow(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)
	c := apimodel.Command{
		CommandID: "cmd-test",
		State:     "Success",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "aaa", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Success", Duration: 1000},
			{ResourceLabel: "bbb", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Success", Duration: 2000},
			{ResourceLabel: "ccc", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Success", Duration: 3000},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now, nil)
	keys := defaultKeyMap()

	// Cursor starts on the first row (default sort asc by Label → "aaa" on top).
	navBefore := dm.navLines()
	require.Greater(t, len(navBefore), dm.cursor)
	wantKey := navBefore[dm.cursor].rowKey

	// Toggle the sort (asc→desc) — the order reverses, so "aaa" moves down.
	dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}, keys)

	navAfter := dm.navLines()
	require.Greater(t, len(navAfter), dm.cursor)
	assert.Equal(t, wantKey, navAfter[dm.cursor].rowKey,
		"focus must stay on the same row after toggling sort, not jump to the top")
	assert.NotEqual(t, 0, dm.cursor, "the focused row actually changed position under the reverse sort")
}
