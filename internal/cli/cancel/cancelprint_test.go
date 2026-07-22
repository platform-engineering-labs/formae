// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cancel

import (
	"bytes"
	"os"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	tuitest.PinRendering()
	os.Exit(m.Run())
}

// makeCancelFixture builds a representative CancelCommandResponse for golden tests.
// It includes two canceled commands, one with a force-canceled resource.
func makeCancelFixture() *apimodel.CancelCommandResponse {
	return &apimodel.CancelCommandResponse{
		CommandIDs: []string{"cmd-abc123", "cmd-def456"},
		Forced:     true,
		ResourceUpdateStates: map[string]apimodel.CancelResourceState{
			"formae://res-aaa#": {
				State:         "Success",
				ForceCanceled: false,
				CommandID:     "cmd-abc123",
			},
			"formae://res-bbb#": {
				State:         "InProgress",
				ForceCanceled: true,
				CommandID:     "cmd-abc123",
			},
			"formae://res-ccc#": {
				State:         "Canceled",
				ForceCanceled: false,
				CommandID:     "cmd-def456",
			},
		},
	}
}

// ── Machine golden pin ─────────────────────────────────────────────────────

// TestMachineGolden_CancelCommandResponse pins the exact bytes produced by
// MachineReadablePrinter over a fixed fixture. This golden MUST NOT change when
// the human-path rendering is modified.
func TestMachineGolden_CancelCommandResponse(t *testing.T) {
	fixture := makeCancelFixture()
	var buf bytes.Buffer
	p := printer.NewMachineReadablePrinter[apimodel.CancelCommandResponse](&buf, "json")
	err := p.Print(fixture)
	require.NoError(t, err)
	tuitest.RequireGolden(t, buf.Bytes())
}

// ── renderCancelResult golden + piped tests ────────────────────────────────

// TestRenderCancelResult_Forced_Styled pins the styled (TrueColor profile) output
// of renderCancelResult with a force-canceled response.
func TestRenderCancelResult_Forced_Styled(t *testing.T) {
	th := theme.New("formae")
	resp := makeCancelFixture()
	out := renderCancelResult(th, resp, 100)
	tuitest.RequireGolden(t, []byte(out))
}

// TestRenderCancelResult_Forced_Piped asserts that the force-canceled output,
// once ANSI escape sequences are stripped, contains expected plain-text content.
func TestRenderCancelResult_Forced_Piped(t *testing.T) {
	th := theme.New("formae")
	resp := makeCancelFixture()
	out := renderCancelResult(th, resp, 100)
	plain := stripANSI(out)

	// Command IDs must appear
	assert.Contains(t, plain, "cmd-abc123", "must contain first command ID")
	assert.Contains(t, plain, "cmd-def456", "must contain second command ID")

	// Header for force-cancel
	assert.Contains(t, plain, "force-canceled", "must mention force-canceled in header")

	// Abandoned resource URI must appear
	assert.Contains(t, plain, "formae://res-bbb#", "must list the force-canceled resource URI")

	// Non-force-canceled resource must NOT appear in abandoned list
	assert.NotContains(t, plain, "formae://res-aaa#", "non-force-canceled resource must not be listed")

	// Warning footer lines
	assert.Contains(t, plain, "Force-cancel abandons in-progress work", "must contain force-cancel warning")
	assert.Contains(t, plain, "synchronizer", "must contain synchronizer mention")

	// Status hint
	assert.Contains(t, plain, "formae status", "must contain status hint")
}

// TestRenderCancelResult_Normal_Piped tests the non-force cancel output.
func TestRenderCancelResult_Normal_Piped(t *testing.T) {
	th := theme.New("formae")
	resp := &apimodel.CancelCommandResponse{
		CommandIDs: []string{"cmd-abc123"},
		Forced:     false,
		ResourceUpdateStates: map[string]apimodel.CancelResourceState{
			"formae://res-aaa#": {State: "Canceled", ForceCanceled: false, CommandID: "cmd-abc123"},
		},
	}
	out := renderCancelResult(th, resp, 100)
	plain := stripANSI(out)

	assert.Contains(t, plain, "cmd-abc123", "must contain command ID")
	assert.Contains(t, plain, "being canceled", "must use 'being canceled' phrasing for non-force")

	// Force-cancel warning must NOT appear
	assert.NotContains(t, plain, "Force-cancel abandons", "non-force output must not contain force-cancel warning")

	// Status hint
	assert.Contains(t, plain, "formae status", "must contain status hint")
}

// TestRenderCancelResult_Empty tests the empty response (no commands canceled).
func TestRenderCancelResult_Empty(t *testing.T) {
	th := theme.New("formae")
	resp := &apimodel.CancelCommandResponse{
		CommandIDs: nil,
	}
	out := renderCancelResult(th, resp, 100)
	plain := stripANSI(out)

	assert.Contains(t, plain, "No commands to cancel", "empty response must say no commands")
}
