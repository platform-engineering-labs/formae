//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func TestApplyTheme_SwapsThemeAndRebuildsSpinner(t *testing.T) {
	m := New(theme.New("quiet"), nil, Options{})
	m2 := m.ApplyTheme(theme.New("rich"))
	if m2.th.Name != "rich" {
		t.Errorf("model theme = %q, want rich", m2.th.Name)
	}
	for i := range m2.tabs {
		if m2.tabs[i].th.Name != "rich" {
			t.Errorf("tabs[%d].th = %q, want rich (must be threaded)", i, m2.tabs[i].th.Name)
		}
	}
	wantFrames := theme.New("rich").Spinner.Frames
	if len(wantFrames) > 0 && m2.spinner.Spinner.Frames[0] != wantFrames[0] {
		t.Errorf("spinner not rebuilt: frame[0] = %q, want %q", m2.spinner.Spinner.Frames[0], wantFrames[0])
	}
}

func TestUpdate_ApplyThemeMsgApplies(t *testing.T) {
	m := New(theme.New("quiet"), nil, Options{})
	updated, _ := m.Update(theme.ApplyThemeMsg{Theme: theme.New("rich")})
	if got := updated.(Model).th.Name; got != "rich" {
		t.Errorf("after ApplyThemeMsg, theme = %q, want rich", got)
	}
}

// TestApplyTheme_RebuildsTabStyledCells gates a live theme swap while a tab
// holds cached per-cell styled replacements. tabModel.sync builds
// styledCells ONCE per fetch/sort/filter event from the tabModel's own th
// field (see tab.go's styleCell application), and loadedView/applyCellStyles
// (tabview.go) reuse those cached strings verbatim every frame. Before the
// fix, ApplyTheme swapped tabs[i].th but never re-ran sync, so a cell already
// on screen (e.g. the "⚠ unmanaged" Stack cell, styled red via
// th.Styles.StatusFailed) stayed rendered in the OLD theme's color until the
// next unrelated fetch/sort/filter happened to call sync again. This proves
// the styled replacement is actually re-rendered, not just that th changed.
//
// quiet and rich have different Error colors (light #DC2626 vs #FF0000, dark
// #F87171 vs #FF0000), so StatusFailed's rendering genuinely differs between
// them — a real (not synthetic) regression signal.
func TestApplyTheme_RebuildsTabStyledCells(t *testing.T) {
	quiet := theme.New("quiet")
	m := New(quiet, nil, Options{})
	m.tabs[TabResources].allRows = []row{
		{cells: []string{"web-1", "⚠ unmanaged", "AWS::EC2::Instance", "i-123"}, title: "web-1"},
	}
	m.tabs[TabResources] = m.tabs[TabResources].sync(0)

	before := m.tabs[TabResources].styledCells[0][1]
	if before.styled == before.plain {
		t.Fatalf("precondition: styleCell must style col 1 under quiet, got styled == plain (%q)", before.plain)
	}

	m2 := m.ApplyTheme(theme.New("rich"))

	after := m2.tabs[TabResources].styledCells[0][1]
	if after.plain != "⚠ unmanaged" {
		t.Errorf("styledCells data lost across ApplyTheme: plain = %q, want %q", after.plain, "⚠ unmanaged")
	}
	if after.styled == before.styled {
		t.Errorf("styledCells not rebuilt: styled output identical to the stale quiet-theme rendering (%q)", after.styled)
	}
}

func TestCloseWatcher_NilSafe(t *testing.T) {
	m := New(theme.New("quiet"), nil, Options{}) // no watcher (not omarchy)
	m.closeWatcher()                             // must not panic
}

// TestApplyTheme_RecolorsOpenDetailScreen gates PLA-348: the open detail
// screen (opened via enter) keeps stale colors after a live Omarchy theme
// change. openDetail bakes the colorized detail body into m.detailViewport
// exactly once, via colorizeDetailLine(m.th, ...) piped into vp.SetContent —
// see openDetail in model.go. Before the fix, ApplyTheme threaded the new
// theme into tabs/table/query/spinner but never rebuilt the detail viewport,
// so a resource's detail screen left open across a theme swap kept rendering
// its "Key: value" lines in the OLD theme's colors until the user pressed
// esc then enter again to force a rebuild.
//
// quiet and rich share identical TextPrimary/TextSecondary (see
// TestTable_SetThemeRebuildsHeaderAndCellStyles), so this test builds a
// synthetic re-themed copy with different neutrals — the same shape of
// change the Omarchy live-follow watcher produces when the OS palette
// changes — to get a real, guaranteed color diff on the "Key: value" lines
// that colorizeDetailLine renders with TextSecondary/TextPrimary.
//
// It also asserts the detail viewport's scroll position (YOffset) survives
// the re-theme: recoloring must not jump the user back to the top of a long
// detail body.
func TestApplyTheme_RecolorsOpenDetailScreen(t *testing.T) {
	manyLines := make([]string, 30)
	for i := range manyLines {
		manyLines[i] = fmt.Sprintf("Key%d: value%d", i, i)
	}
	r := row{
		cells:  []string{"web-1", "production", "AWS::EC2::Instance", "i-0abc123456789"},
		title:  "web-1 (AWS::EC2::Instance)",
		detail: func(_ int) []string { return manyLines },
	}
	mm := buildDetailTestModel(t, []row{r})
	mm = openDetailOnFirstRow(t, mm)
	require.True(t, mm.(Model).detailOpen, "detail screen must be open")

	// Scroll down so a recolorize-induced scroll reset would be observable.
	m := mm.(Model)
	m.detailViewport.ScrollDown(3)
	mm = m
	offsetBefore := mm.(Model).detailViewport.YOffset
	require.Greater(t, offsetBefore, 0, "precondition: viewport must have scrolled")

	before := mm.(Model).detailViewport.View()

	base := mm.(Model).th
	retheme := *base
	retheme.Palette.TextSecondary = lipgloss.AdaptiveColor{Light: "#FF00FF", Dark: "#FF00FF"}
	retheme.Palette.TextPrimary = lipgloss.AdaptiveColor{Light: "#00FFFF", Dark: "#00FFFF"}
	retheme.Styles = theme.NewStyles(retheme.Palette)

	m2 := mm.(Model).ApplyTheme(&retheme)

	assert.True(t, m2.detailOpen, "detail screen must remain open across ApplyTheme")
	after := m2.detailViewport.View()
	assert.NotEqual(t, before, after,
		"detail viewport content must be re-colorized for the new theme, not left stale")
	assert.Equal(t, offsetBefore, m2.detailViewport.YOffset,
		"re-theming the open detail screen must not reset scroll position")
}

