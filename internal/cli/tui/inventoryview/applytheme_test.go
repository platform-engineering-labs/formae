//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"testing"

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

