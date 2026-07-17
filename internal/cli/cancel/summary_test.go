// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cancel

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// TestKsuidFromURI covers the three URI forms defined in the spec.
func TestKsuidFromURI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare id passes through", "2abc", "2abc"},
		{"formae URI with hash suffix", "formae://2abc#", "2abc"},
		{"formae URI with property path", "formae://x#/prop", "x"},
		{"formae URI no hash", "formae://myksuid", "myksuid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ksuidFromURI(tc.input))
		})
	}
}

// TestCancelExpectations verifies each bucket maps correctly from CancelResourceState.
func TestCancelExpectations(t *testing.T) {
	resp := &apimodel.CancelCommandResponse{
		CommandIDs: []string{"cmd-a", "cmd-b"},
		ResourceUpdateStates: map[string]apimodel.CancelResourceState{
			"formae://r1#": {State: "Success", ForceCanceled: false, CommandID: "cmd-a"},
			"formae://r2#": {State: "Failed", ForceCanceled: false, CommandID: "cmd-a"},
			"formae://r3#": {State: "InProgress", ForceCanceled: false, CommandID: "cmd-a"},
			"formae://r4#": {State: "InProgress", ForceCanceled: true, CommandID: "cmd-b"},
			"formae://r5#": {State: "Canceled", ForceCanceled: true, CommandID: "cmd-b"},
			"formae://r6#": {State: "Canceled", ForceCanceled: false, CommandID: "cmd-b"},
		},
	}

	exps := cancelExpectations(resp)
	require.NotNil(t, exps)

	expA := exps["cmd-a"]
	assert.Equal(t, 1, expA.Completed, "cmd-a: r1 Success → Completed")
	assert.Equal(t, 1, expA.Failed, "cmd-a: r2 Failed → Failed")
	assert.Equal(t, 1, expA.WillFinish, "cmd-a: r3 InProgress !ForceCanceled → WillFinish")
	assert.Equal(t, 0, expA.Abandoned, "cmd-a: no abandoned")
	assert.Equal(t, 0, expA.WillCancel, "cmd-a: no WillCancel")

	expB := exps["cmd-b"]
	assert.Equal(t, 0, expB.Completed, "cmd-b: no success")
	assert.Equal(t, 0, expB.Failed, "cmd-b: no failed")
	assert.Equal(t, 0, expB.WillFinish, "cmd-b: no WillFinish")
	assert.Equal(t, 2, expB.Abandoned, "cmd-b: r4 InProgress+Force + r5 Canceled+Force → Abandoned")
	assert.Equal(t, 1, expB.WillCancel, "cmd-b: r6 Canceled !ForceCanceled → WillCancel")
}

func TestCancelExpectations_EmptyResponse(t *testing.T) {
	resp := &apimodel.CancelCommandResponse{
		CommandIDs:           []string{},
		ResourceUpdateStates: map[string]apimodel.CancelResourceState{},
	}
	exps := cancelExpectations(resp)
	assert.Empty(t, exps)
}

// TestRenderCancelSummary_SingleCommand tests the single-command no-force variant.
func TestRenderCancelSummary_SingleCommand(t *testing.T) {
	th := theme.New("formae")
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	start := now.Add(-42 * time.Second)

	cmds := []apimodel.Command{
		{
			CommandID: "cmd-abc123",
			Command:   "apply",
			Mode:      "reconcile",
			State:     "InProgress",
			StartTs:   start,
			ResourceUpdates: makeUpdates(8, "Success", 2, "InProgress", 5, "Pending"),
		},
	}
	exps := map[string]expectation{
		"cmd-abc123": {Completed: 8, WillFinish: 2, WillCancel: 5},
	}

	out := renderCancelSummary(th, cmds, exps, false, now)

	// Strip ANSI for content assertions
	plain := stripANSI(out)

	assert.Contains(t, plain, "Canceling 1 command")
	assert.Contains(t, plain, "cmd-abc123")
	assert.Contains(t, plain, "apply")
	assert.Contains(t, plain, "reconcile")
	assert.Contains(t, plain, "8 updates already completed")
	assert.Contains(t, plain, "2 updates currently in progress")
	assert.Contains(t, plain, "will finish before stopping")
	assert.Contains(t, plain, "5 updates pending")
	assert.Contains(t, plain, "will be canceled")
	assert.Contains(t, plain, "formae status command --query='id:cmd-abc123' --watch")
}

// TestRenderCancelSummary_MultiCommand tests the multi-command no-force variant.
func TestRenderCancelSummary_MultiCommand(t *testing.T) {
	th := theme.New("formae")
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	start := now.Add(-42 * time.Second)

	cmds := []apimodel.Command{
		{
			CommandID:       "cmd-abc123",
			Command:         "apply",
			Mode:            "reconcile",
			State:           "InProgress",
			StartTs:         start,
			ResourceUpdates: makeUpdates(8, "Success", 2, "InProgress", 5, "Pending"),
		},
		{
			CommandID:       "cmd-def456",
			Command:         "apply",
			Mode:            "patch",
			State:           "InProgress",
			StartTs:         start,
			ResourceUpdates: makeUpdates(2, "Success", 1, "InProgress", 3, "Pending"),
		},
	}
	exps := map[string]expectation{
		"cmd-abc123": {Completed: 8, WillFinish: 2, WillCancel: 5},
		"cmd-def456": {Completed: 2, WillFinish: 1, WillCancel: 3},
	}

	out := renderCancelSummary(th, cmds, exps, false, now)
	plain := stripANSI(out)

	assert.Contains(t, plain, "Canceling 2 commands")
	assert.Contains(t, plain, "cmd-abc123")
	assert.Contains(t, plain, "cmd-def456")
	assert.Contains(t, plain, "reconcile")
	assert.Contains(t, plain, "patch")
	// Watch hints per command
	assert.Contains(t, plain, "formae status command --query='id:cmd-abc123'")
	assert.Contains(t, plain, "formae status command --query='id:cmd-def456'")
}

// TestRenderCancelSummary_Force tests that force variant uses "will be abandoned" phrasing.
func TestRenderCancelSummary_Force(t *testing.T) {
	th := theme.New("formae")
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	start := now.Add(-10 * time.Second)

	cmds := []apimodel.Command{
		{
			CommandID:       "cmd-abc123",
			Command:         "apply",
			Mode:            "reconcile",
			State:           "InProgress",
			StartTs:         start,
			ResourceUpdates: makeUpdates(3, "Success", 2, "InProgress", 1, "Pending"),
		},
	}
	exps := map[string]expectation{
		"cmd-abc123": {Completed: 3, Abandoned: 2, WillCancel: 1},
	}

	out := renderCancelSummary(th, cmds, exps, true, now)
	plain := stripANSI(out)

	assert.Contains(t, plain, "abandoned")
	// Should NOT say "will finish" in force mode
	assert.NotContains(t, plain, "will finish")
}

// makeUpdates builds a ResourceUpdates slice: pairs of (count, state).
func makeUpdates(pairs ...interface{}) []apimodel.ResourceUpdate {
	var updates []apimodel.ResourceUpdate
	for i := 0; i+1 < len(pairs); i += 2 {
		count := pairs[i].(int)
		state := pairs[i+1].(string)
		for j := 0; j < count; j++ {
			updates = append(updates, apimodel.ResourceUpdate{State: state})
		}
	}
	return updates
}

// stripANSI removes ANSI escape sequences for plain-text assertions.
func stripANSI(s string) string {
	// Quick replacement approach for tests.
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// skip until 'm'
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}
