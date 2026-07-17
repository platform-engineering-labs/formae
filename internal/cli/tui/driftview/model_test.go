// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package driftview

import (
	"os"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func TestMain(m *testing.M) {
	tuitest.PinRendering()
	os.Exit(m.Run())
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// makeFixtureError builds a two-stack rejection with an update row, a
// create-semantic row (no OldProperties), a delete row, and a second-stack
// update row.
func makeFixtureError() *apimodel.FormaReconcileRejectedError {
	return &apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"production": {
				ModifiedResources: []apimodel.ResourceModification{
					{
						Stack:     "production",
						Type:      "AWS::SSM::Parameter",
						Label:     "web-config",
						Operation: "update",
						PatchDocument: []byte(`[
							{"op":"replace","path":"/Value","value":"/api/v2"},
							{"op":"replace","path":"/Description","value":"API endpoint v2"}
						]`),
						Properties:    []byte(`{"Value":"/api/v2","Description":"API endpoint v2"}`),
						OldProperties: []byte(`{"Value":"/api/v1","Description":"API endpoint"}`),
					},
					{
						Stack:     "production",
						Type:      "AWS::S3::Bucket",
						Label:     "monitoring-bucket",
						Operation: "update",
						// No PatchDocument, no OldProperties → create semantic
					},
					{
						Stack:     "production",
						Type:      "AWS::ElastiCache::Cluster",
						Label:     "old-cache",
						Operation: "delete",
					},
				},
			},
			"staging": {
				ModifiedResources: []apimodel.ResourceModification{
					{
						Stack:         "staging",
						Type:          "AWS::ApiGateway::RestApi",
						Label:         "api-gateway",
						Operation:     "update",
						PatchDocument: []byte(`[{"op":"replace","path":"/StageName","value":"v2"}]`),
						Properties:    []byte(`{"StageName":"v2"}`),
						OldProperties: []byte(`{"StageName":"v1"}`),
					},
				},
			},
		},
	}
}

func makeModel(width, height int) Model {
	th := theme.New("formae")
	rejected := makeFixtureError()
	m := New(th, rejected, Options{})
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return mm.(Model)
}

// press sends a single key to the model and returns the updated model.
func press(t *testing.T, m Model, key string) Model {
	t.Helper()
	var k tea.KeyMsg
	switch key {
	case "enter":
		k = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		k = tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		k = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "backspace":
		k = tea.KeyMsg{Type: tea.KeyBackspace}
	case "up":
		k = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		k = tea.KeyMsg{Type: tea.KeyDown}
	default:
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	var mm tea.Model = m
	mm, _ = mm.Update(k)
	return mm.(Model)
}

// TestDriftView_GoldenMain checks the initial list screen at 100x32.
func TestDriftView_GoldenMain(t *testing.T) {
	m := makeModel(100, 32)
	tuitest.RequireGolden(t, []byte(m.View()))
}

// TestDriftView_SelectionToggles exercises space/a/n selection semantics per
// row class: update and create rows toggle, delete rows never select.
func TestDriftView_SelectionToggles(t *testing.T) {
	m := makeModel(100, 32)

	// All selectable rows pre-selected: web-config, monitoring-bucket, api-gateway.
	require.Equal(t, 3, m.selectedCount())

	// Cursor 0 = production/web-config (update row): space toggles off.
	m = press(t, m, " ")
	assert.False(t, m.selected["production/web-config"])
	assert.Equal(t, 2, m.selectedCount())
	// Selection and delta visibility are independent: space must not expand.
	assert.False(t, m.expanded["production/web-config"], "space must not toggle delta expansion")

	// Cursor 1 = production/monitoring-bucket (create row): space toggles off.
	m = press(t, m, "j")
	m = press(t, m, " ")
	assert.False(t, m.selected["production/monitoring-bucket"])
	assert.Equal(t, 1, m.selectedCount())

	// Cursor 2 = production/old-cache (delete row): space is a no-op.
	m = press(t, m, "j")
	m = press(t, m, " ")
	assert.False(t, m.selected["production/old-cache"])
	assert.Equal(t, 1, m.selectedCount())

	// a selects all selectable rows.
	m = press(t, m, "a")
	assert.Equal(t, 3, m.selectedCount())
	assert.False(t, m.selected["production/old-cache"])

	// n deselects all.
	m = press(t, m, "n")
	assert.Equal(t, 0, m.selectedCount())
}

// TestDriftView_GoldenExpandedDelta expands the first update row and checks
// the change-delta lines against a golden file.
func TestDriftView_GoldenExpandedDelta(t *testing.T) {
	m := makeModel(100, 32)
	m = press(t, m, "enter") // cursor 0 = web-config (update row)
	require.True(t, m.expanded["production/web-config"])
	tuitest.RequireGolden(t, []byte(m.View()))
}

// TestDriftView_GoldenFilePrompt opens the extract file prompt and checks the
// golden, then confirms that typing and enter produce a DecisionExtract.
func TestDriftView_GoldenFilePrompt(t *testing.T) {
	m := makeModel(100, 32)
	m = press(t, m, "e")
	require.Equal(t, screenFilePrompt, m.screen)
	tuitest.RequireGolden(t, []byte(m.View()))

	// Typing appends to the path; enter confirms with the typed path.
	m = press(t, m, "2")
	m = press(t, m, "enter")
	dec, ok := m.Decision().(DecisionExtract)
	require.True(t, ok, "expected DecisionExtract, got %T", m.Decision())
	assert.Equal(t, "./extracted-drift.pkl2", dec.Path)
	assert.Len(t, dec.Selected, 3)
}

// TestDriftView_GoldenRevertConfirm opens the revert confirmation screen.
func TestDriftView_GoldenRevertConfirm(t *testing.T) {
	m := makeModel(100, 32)
	m = press(t, m, "r")
	require.Equal(t, screenRevertConfirm, m.screen)
	tuitest.RequireGolden(t, []byte(m.View()))
}

// TestDriftView_DecisionExtractCarriesSelected verifies the extract decision
// carries exactly the selected refs.
func TestDriftView_DecisionExtractCarriesSelected(t *testing.T) {
	m := makeModel(100, 32)
	m = press(t, m, "n") // deselect all
	require.Equal(t, 0, m.selectedCount())
	m = press(t, m, " ") // cursor 0 = web-config: re-select
	require.Equal(t, 1, m.selectedCount())

	m = press(t, m, "e")
	require.Equal(t, screenFilePrompt, m.screen)
	m = press(t, m, "enter")

	dec, ok := m.Decision().(DecisionExtract)
	require.True(t, ok, "expected DecisionExtract, got %T", m.Decision())
	require.Len(t, dec.Selected, 1)
	assert.Equal(t, ResourceRef{
		Stack:     "production",
		Type:      "AWS::SSM::Parameter",
		Label:     "web-config",
		Operation: "update",
	}, dec.Selected[0])
}

// TestDriftView_NoticeRendered checks the optional notice line appears.
func TestDriftView_NoticeRendered(t *testing.T) {
	th := theme.New("formae")
	notice := "Drift changed while you were reviewing."
	m := New(th, makeFixtureError(), Options{Notice: notice})
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	m = mm.(Model)

	assert.Contains(t, plain(m.View()), notice)
}

// TestDriftView_ViewLineCountMatchesHeight asserts the exact-fill contract at
// several terminal sizes.
func TestDriftView_ViewLineCountMatchesHeight(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {100, 32}, {120, 40}}
	for _, sz := range sizes {
		m := makeModel(sz.w, sz.h)
		got := strings.Count(m.View(), "\n") + 1
		assert.Equal(t, sz.h, got, "size %dx%d", sz.w, sz.h)
	}
}

// TestDriftView_RenderingIntegrity checks no broken escape fragments survive
// ANSI stripping and no line exceeds the terminal width.
func TestDriftView_RenderingIntegrity(t *testing.T) {
	garbage := regexp.MustCompile(`\[[0-9;]+[A-Za-z]`)
	m := makeModel(100, 32)
	for i, line := range strings.Split(m.View(), "\n") {
		stripped := plain(line)
		assert.False(t, garbage.MatchString(stripped), "line %d has escape garbage: %q", i, stripped)
		assert.LessOrEqual(t, lipgloss.Width(line), 100, "line %d exceeds width", i)
	}
}
