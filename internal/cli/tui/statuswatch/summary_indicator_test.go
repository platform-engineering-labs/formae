//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func TestSummaryOutcome(t *testing.T) {
	cases := []struct {
		name    string
		healths []health
		want    summaryOutcome
	}{
		{"empty list is neutral/running", nil, outcomeRunning},
		{"all settled ok is success", []health{healthFinishedOK, healthFinishedOK}, outcomeSuccess},
		{"any settled failure is failed", []health{healthFinishedOK, healthFinishedFailed}, outcomeFailed},
		{"running-failing surfaces as failed", []health{healthRunningFailing, healthFinishedOK}, outcomeFailed},
		{"still running and healthy is running", []health{healthRunning, healthFinishedOK}, outcomeRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]row, len(tc.healths))
			for i, h := range tc.healths {
				rows[i] = row{health: h}
			}
			v := multiView{rows: rows}
			assert.Equal(t, tc.want, v.summaryOutcome())
		})
	}
}

// indicatorLine returns the rendered line carrying the top-right "↻ live"
// indicator, so a color assertion about it can't be satisfied by a colored data
// row elsewhere in the view.
func indicatorLine(view string) string {
	for _, ln := range strings.Split(view, "\n") {
		if strings.Contains(plain(ln), "live") {
			return ln
		}
	}
	return ""
}

func renderSummaryView(t *testing.T, th *theme.Theme, cmds []apimodel.Command) string {
	t.Helper()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	m := New(th, &fakeClient{}, Options{MaxResults: 10, PollInterval: time.Hour, Now: func() time.Time { return now }})
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 110, Height: 24})
	mm, _ = mm.Update(commandsMsg{commands: cmds})
	return mm.(Model).View()
}

// TestView_SummaryIndicator_SeverityTheme locks item: in the rich (severity)
// theme the top-right summary indicator is red on failure and green on success.
func TestView_SummaryIndicator_SeverityTheme(t *testing.T) {
	now := time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC)
	const (
		richErrorRed  = "38;2;255;102;102" // rich Error.Dark #FF6666 (softened)
		richDoneGreen = "38;2;73;222;128"  // rich Done.Dark
		richBlue      = "38;2;96;165;250"  // rich PrimaryAccent.Dark #60A5FA
	)

	// Two+ commands keep the summary (multi) view; a single one auto-drills into
	// detail, where the outcome indicator does not apply.
	failed := indicatorLine(renderSummaryView(t, theme.New("rich"),
		[]apimodel.Command{cmdFix("c1", "apply", "Success", now, 0), cmdFix("c2", "apply", "Failed", now, 1)}))
	assert.Contains(t, failed, richErrorRed, "rich summary indicator must be red when a command failed")
	assert.NotContains(t, failed, richBlue, "a failed indicator must not stay blue")

	ok := indicatorLine(renderSummaryView(t, theme.New("rich"),
		[]apimodel.Command{cmdFix("c1", "apply", "Success", now, 0), cmdFix("c2", "apply", "Success", now, 0)}))
	assert.Contains(t, ok, richDoneGreen, "rich summary indicator must be green on full success")
}

// TestView_SummaryIndicator_BrandThemeStaysBlue locks that the outcome coloring
// is gated to severity themes: a brand theme (quiet) keeps the neutral blue
// indicator even when a command failed.
func TestView_SummaryIndicator_BrandThemeStaysBlue(t *testing.T) {
	now := time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC)
	const quietBlue = "38;2;129;209;219" // quiet PrimaryAccent.Dark #81D1DB

	ln := indicatorLine(renderSummaryView(t, theme.New("quiet"),
		[]apimodel.Command{cmdFix("c1", "apply", "Success", now, 0), cmdFix("c2", "apply", "Failed", now, 1)}))
	assert.Contains(t, ln, quietBlue, "quiet (brand) summary indicator stays blue regardless of outcome")
}
