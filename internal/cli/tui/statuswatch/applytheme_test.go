//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func TestApplyTheme_SwapsThemeAndRebuildsSpinner(t *testing.T) {
	quiet := theme.New("quiet")
	rich := theme.New("rich")
	m := New(quiet, nil, Options{})

	m2 := m.ApplyTheme(rich)

	if m2.th.Name != "rich" {
		t.Errorf("model theme = %q, want rich", m2.th.Name)
	}
	if m2.multi.th.Name != "rich" {
		t.Errorf("multiView theme = %q, want rich (must be threaded)", m2.multi.th.Name)
	}
	if m2.detail.th.Name != "rich" {
		t.Errorf("detailModel theme = %q, want rich (must be threaded)", m2.detail.th.Name)
	}
	// Spinner frames are rebuilt from the new theme.
	wantFrames := rich.Spinner.Frames
	if len(wantFrames) > 0 && m2.spinner.Spinner.Frames[0] != wantFrames[0] {
		t.Errorf("spinner frame[0] = %q, want %q (rebuilt)", m2.spinner.Spinner.Frames[0], wantFrames[0])
	}
}

func TestUpdate_ApplyThemeMsgApplies(t *testing.T) {
	m := New(theme.New("quiet"), nil, Options{})
	updated, _ := m.Update(theme.ApplyThemeMsg{Theme: theme.New("rich")})
	if got := updated.(Model).th.Name; got != "rich" {
		t.Errorf("after ApplyThemeMsg, theme = %q, want rich", got)
	}
}
