//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package simview

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestResolutionAcceptanceAndWarningsVisible(t *testing.T) {
	sim := &apimodel.Simulation{ChangesRequired: true, Warnings: []string{"Unmanaged resources lose visibility"}, Command: apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "accept", ResourceLabel: "keep-cloud"}, {Operation: "accept_delete", ResourceLabel: "gone"}, {Operation: "update", ResourceLabel: "provider-change"}}}}
	th := theme.New("formae")
	plain := ansiEscape.ReplaceAllString(RenderSimulationPlain(th, sim, 120), "")
	require.Contains(t, plain, "1 accept")
	require.Contains(t, plain, "1 accept deletion")
	require.Contains(t, plain, "1 update")
	require.NotContains(t, plain, "create")
	require.Contains(t, plain, sim.Warnings[0])
	model := New(th, sim, Options{})
	next, _ := model.Update(tea.WindowSizeMsg{Width: 180, Height: 45})
	require.Contains(t, next.View(), sim.Warnings[0])
	require.Contains(t, next.(Model).planSummary(), "record 2 drift acceptance(s)")
}

func TestAcceptDeletionExplainsNoProviderDelete(t *testing.T) {
	text := RenderSimulationPlain(theme.New("formae"), &apimodel.Simulation{Command: apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "accept_delete", ResourceLabel: "gone"}}}}, 120)
	require.Contains(t, text, "Record the confirmed deletion as desired intent; no provider delete.")
}

func TestWithdrawalExplainsUnconfirmedCloudState(t *testing.T) {
	sim := &apimodel.Simulation{ChangesRequired: true, Command: apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "withdraw", ResourceLabel: "failed-create"}}}}
	th := theme.New("formae")
	text := RenderSimulationPlain(th, sim, 120)
	require.Contains(t, text, "Withdraw desired intent; cloud state remains unconfirmed. No provider operation.")
	model := New(th, sim, Options{})
	require.Contains(t, model.planSummary(), "withdraw 1 desired declaration(s)")
	require.NotContains(t, model.planSummary(), "drift acceptance")
}
