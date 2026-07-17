// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildHelpTestModel builds a loaded model at 100×24 ready for help-overlay tests.
func buildHelpTestModel(t *testing.T) tea.Model {
	t.Helper()
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	// Load resources.
	rows := resourceRowsFromForma(fc.forma)
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	return mm
}

// pressKey is a convenience helper to send a rune key.
func pressKey(mm tea.Model, r rune) tea.Model {
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return mm
}

func pressEsc(mm tea.Model) tea.Model {
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	return mm
}

// ---------------------------------------------------------------------------
// TestHelp_QuestionMarkTogglesOpen
// ---------------------------------------------------------------------------

// TestHelp_QuestionMarkTogglesOpen: pressing ? opens the overlay; pressing ?
// again closes it; View is byte-identical to the pre-open view after closing.
func TestHelp_QuestionMarkTogglesOpen(t *testing.T) {
	mm := buildHelpTestModel(t)

	viewBefore := mm.(Model).View()

	// Press ? → overlay open.
	mm = pressKey(mm, '?')
	assert.True(t, mm.(Model).helpOpen, "helpOpen must be true after pressing ?")
	viewOverlay := mm.(Model).View()
	assert.NotEqual(t, viewBefore, viewOverlay, "view must change when help overlay is open")

	// Press ? again → overlay closed, view restored.
	mm = pressKey(mm, '?')
	assert.False(t, mm.(Model).helpOpen, "helpOpen must be false after pressing ? again")
	viewAfter := mm.(Model).View()
	assert.Equal(t, viewBefore, viewAfter, "view must be byte-identical after closing overlay")
}

// ---------------------------------------------------------------------------
// TestHelp_EscClosesOverlay
// ---------------------------------------------------------------------------

// TestHelp_EscClosesOverlay: esc closes the overlay; view is restored.
func TestHelp_EscClosesOverlay(t *testing.T) {
	mm := buildHelpTestModel(t)
	viewBefore := mm.(Model).View()

	mm = pressKey(mm, '?')
	require.True(t, mm.(Model).helpOpen)

	mm = pressEsc(mm)
	assert.False(t, mm.(Model).helpOpen, "esc must close the help overlay")
	assert.Equal(t, viewBefore, mm.(Model).View(), "view must be byte-identical after esc")
}

// ---------------------------------------------------------------------------
// TestHelp_FromDetailScreen
// ---------------------------------------------------------------------------

// TestHelp_FromDetailScreen: ? opens overlay from detail screen; esc closes
// overlay and returns to detail screen (not to list).
func TestHelp_FromDetailScreen(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	rows := resourceRowsFromForma(fc.forma)
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})

	// Open detail.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.True(t, mm.(Model).detailOpen, "detail must be open after enter")

	detailViewBefore := mm.(Model).View()

	// Press ? → overlay open.
	mm = pressKey(mm, '?')
	assert.True(t, mm.(Model).helpOpen, "helpOpen must be true after ? from detail screen")
	assert.True(t, mm.(Model).detailOpen, "detailOpen must remain true while overlay is open")

	// Press esc → overlay closed, back to detail screen.
	mm = pressEsc(mm)
	assert.False(t, mm.(Model).helpOpen, "esc must close the help overlay")
	assert.True(t, mm.(Model).detailOpen, "detail must still be open after closing overlay")
	assert.Equal(t, detailViewBefore, mm.(Model).View(), "detail view must be byte-identical after esc")
}

// ---------------------------------------------------------------------------
// TestHelp_KeysSwallowedWhileOpen
// ---------------------------------------------------------------------------

// TestHelp_KeysSwallowedWhileOpen: while the overlay is open, keys 2, s, /, q
// do nothing; ctrl+c still quits.
func TestHelp_KeysSwallowedWhileOpen(t *testing.T) {
	mm := buildHelpTestModel(t)
	mm = pressKey(mm, '?')
	require.True(t, mm.(Model).helpOpen)

	// 2 does not switch tab.
	activeBefore := mm.(Model).active
	mm = pressKey(mm, '2')
	assert.Equal(t, activeBefore, mm.(Model).active, "tab must not switch while overlay open")
	assert.True(t, mm.(Model).helpOpen, "overlay must remain open after pressing 2")

	// s does not open sort selector.
	mm = pressKey(mm, 's')
	assert.False(t, mm.(Model).sortOpen, "sort must not open while overlay is open")
	assert.True(t, mm.(Model).helpOpen)

	// / does not open filter bar.
	mm = pressKey(mm, '/')
	assert.False(t, mm.(Model).filterFocused, "filter must not open while overlay is open")
	assert.True(t, mm.(Model).helpOpen)

	// q does not quit (returns no tea.Quit cmd).
	var cmd tea.Cmd
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	assert.Nil(t, cmd, "q must not produce a quit cmd while overlay is open")
	assert.True(t, mm.(Model).helpOpen)

	// ctrl+c quits.
	_, quitCmd := mm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	require.NotNil(t, quitCmd)
	assert.Equal(t, tea.Quit(), quitCmd())
}

// ---------------------------------------------------------------------------
// TestHelp_ExactFillWithOverlay
// ---------------------------------------------------------------------------

// TestHelp_ExactFillWithOverlay: View() with overlay open at 100×24 (list screen)
// returns exactly 24 lines.
func TestHelp_ExactFillWithOverlay(t *testing.T) {
	mm := buildHelpTestModel(t)
	mm = pressKey(mm, '?')
	view := mm.(Model).View()
	lines := strings.Split(view, "\n")
	assert.Equal(t, 24, len(lines), "View must return exactly height lines with overlay open (list screen)")
}

// TestHelp_ExactFillWithOverlayFromDetail: View() with overlay open from detail
// screen returns exactly 24 lines.
func TestHelp_ExactFillWithOverlayFromDetail(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	rows := resourceRowsFromForma(fc.forma)
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.True(t, mm.(Model).detailOpen)

	mm = pressKey(mm, '?')
	view := mm.(Model).View()
	lines := strings.Split(view, "\n")
	assert.Equal(t, 24, len(lines), "View must return exactly height lines with overlay open (detail screen)")
}

// ---------------------------------------------------------------------------
// TestHelp_QuestionNotRoutedInFilterBar
// ---------------------------------------------------------------------------

// TestHelp_QuestionNotRoutedInFilterBar: pressing ? while filter bar is focused
// inserts ? into the filter (does not open overlay).
func TestHelp_QuestionNotRoutedInFilterBar(t *testing.T) {
	mm := buildHelpTestModel(t)

	// Open filter bar.
	mm = pressKey(mm, '/')
	require.True(t, mm.(Model).filterFocused, "filter must be focused after /")

	// Press ? — must go to the textinput, not open overlay.
	mm = pressKey(mm, '?')
	assert.False(t, mm.(Model).helpOpen, "? must not open overlay while filter bar is focused")
}

// ---------------------------------------------------------------------------
// TestNarrow_FooterSwitches_At80Boundary
// ---------------------------------------------------------------------------

// TestNarrow_FooterSwitches_At80Boundary: at width 79 the narrow footer path is
// taken (statusLineNarrow); at width 80 the full path is taken.
func TestNarrow_FooterSwitches_At80Boundary(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}

	buildAt := func(width int) string {
		m := newTestInventoryModel(t, fc, opts)
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		rows := []row{
			resourceRow(pkgmodel.Resource{NativeID: "arn:aws:s3:::my-bucket", Stack: "production", Type: "AWS::S3::Bucket", Label: "my-bucket"}),
			resourceRow(pkgmodel.Resource{NativeID: "i-0abc123456789", Stack: "production", Type: "AWS::EC2::Instance", Label: "web-1"}),
			resourceRow(pkgmodel.Resource{NativeID: "arn:aws:s3:::old-logs", Stack: "unmanaged", Type: "AWS::S3::Bucket", Label: "old-logs"}),
		}
		mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
		return mm.(Model).View()
	}

	// At width 79: narrow path — combined footer line contains "·" and compact
	// glyphs; entity noun is absent; body is one line taller than at width 80.
	view79 := buildAt(79)
	assert.Contains(t, view79, "·", "width 79 must show combined narrow footer with · separator")
	assert.NotContains(t, view79, "resources", "width 79 must drop entity noun (short count only)")
	lines79 := strings.Split(view79, "\n")
	assert.Equal(t, 24, len(lines79), "width 79 must still fill exactly 24 lines")

	// At width 80: full path — separate status line carries entity noun "resources".
	view80 := buildAt(80)
	assert.Contains(t, view80, "resources", "width 80 must show full status line with entity noun")
	assert.NotContains(t, view80, "·", "width 80 must not show narrow · separator")
}

// ---------------------------------------------------------------------------
// TestNarrow_WidthBound_30
// ---------------------------------------------------------------------------

// TestNarrow_WidthBound_30: at width 30 every View() line has lipgloss.Width ≤ 30,
// View() returns exactly height lines, and no panic occurs.
func TestNarrow_WidthBound_30(t *testing.T) {
	const narrowWidth = 30
	const narrowHeight = 24

	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m

	require.NotPanics(t, func() {
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: narrowWidth, Height: narrowHeight})
		rows := []row{
			resourceRow(pkgmodel.Resource{NativeID: "arn:aws:s3:::my-bucket", Stack: "production", Type: "AWS::S3::Bucket", Label: "my-bucket"}),
			resourceRow(pkgmodel.Resource{NativeID: "i-0abc123456789", Stack: "production", Type: "AWS::EC2::Instance", Label: "web-1"}),
			resourceRow(pkgmodel.Resource{NativeID: "arn:aws:s3:::old-logs", Stack: "unmanaged", Type: "AWS::S3::Bucket", Label: "old-logs"}),
		}
		mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	}, "Update at width 30 must not panic")

	var view string
	require.NotPanics(t, func() {
		view = mm.(Model).View()
	}, "View at width 30 must not panic")

	lines := strings.Split(view, "\n")
	assert.Equal(t, narrowHeight, len(lines), "View must return exactly height lines at width 30")

	for i, line := range lines {
		w := lipgloss.Width(line)
		assert.LessOrEqualf(t, w, narrowWidth,
			"line %d is %d cols wide (limit %d): %q", i, w, narrowWidth, line)
	}
}

// ---------------------------------------------------------------------------
// Golden tests
// ---------------------------------------------------------------------------

// TestGolden_HelpOverlay: help overlay open at 100×24, list screen with resources loaded.
func TestGolden_HelpOverlay(t *testing.T) {
	mm := buildHelpTestModel(t)
	mm = pressKey(mm, '?')
	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}

// TestGolden_Narrow56: 56-wide loaded resources — NativeID dropped, narrow footer.
func TestGolden_Narrow56(t *testing.T) {
	const w = 56
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: w, Height: 24})
	rows := []row{
		resourceRow(pkgmodel.Resource{NativeID: "arn:aws:s3:::my-bucket", Stack: "production", Type: "AWS::S3::Bucket", Label: "my-bucket"}),
		resourceRow(pkgmodel.Resource{NativeID: "i-0abc123456789", Stack: "production", Type: "AWS::EC2::Instance", Label: "web-1"}),
		resourceRow(pkgmodel.Resource{NativeID: "i-0abc123456xyz", Stack: "production", Type: "AWS::EC2::Instance", Label: "web-2"}),
		resourceRow(pkgmodel.Resource{NativeID: "arn:aws:s3:::logs", Stack: "unmanaged", Type: "AWS::S3::Bucket", Label: "logs"}),
	}
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}

// TestGolden_Narrow30: 30-wide loaded resources — proportional-shrink path,
// every line ≤ 30, exact fill.
func TestGolden_Narrow30(t *testing.T) {
	const w = 30
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: w, Height: 24})
	rows := []row{
		resourceRow(pkgmodel.Resource{NativeID: "arn:aws:s3:::my-bucket", Stack: "production", Type: "AWS::S3::Bucket", Label: "my-bucket"}),
		resourceRow(pkgmodel.Resource{NativeID: "i-0abc123456789", Stack: "production", Type: "AWS::EC2::Instance", Label: "web-1"}),
		resourceRow(pkgmodel.Resource{NativeID: "arn:aws:s3:::old-logs", Stack: "unmanaged", Type: "AWS::S3::Bucket", Label: "old-logs"}),
	}
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})

	view := mm.(Model).View()

	// Width-bound assertion: every line ≤ 30.
	lines := strings.Split(view, "\n")
	assert.Equal(t, 24, len(lines), "View must return exactly height lines at width 30")
	for i, line := range lines {
		wl := lipgloss.Width(line)
		assert.LessOrEqualf(t, wl, w,
			"line %d is %d cols wide (limit %d): %q", i, wl, w, line)
	}

	tuitest.RequireGolden(t, []byte(view))
}
