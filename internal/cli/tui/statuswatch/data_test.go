// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func TestMapUpdateState(t *testing.T) {
	cases := map[string]components.State{
		apimodel.ResourceUpdateStateSuccess:    components.StateDone,
		apimodel.ResourceUpdateStateFailed:     components.StateFailed,
		apimodel.ResourceUpdateStateRejected:   components.StateFailed,
		apimodel.ResourceUpdateStateInProgress: components.StateInProgress,
		apimodel.ResourceUpdateStateCanceled:   components.StateSkipped,
		apimodel.ResourceUpdateStatePending:    components.StatePending,
		apimodel.ResourceUpdateStateNotStarted: components.StatePending,
		apimodel.ResourceUpdateStateUnknown:    components.StatePending,
	}
	for in, want := range cases {
		assert.Equal(t, want, mapUpdateState(in), in)
	}
}

func TestCommandCounts_SpansAllUpdateKinds(t *testing.T) {
	c := apimodel.Command{
		TargetUpdates:   []apimodel.TargetUpdate{{State: "Success"}},
		StackUpdates:    []apimodel.StackUpdate{{State: "Success"}},
		PolicyUpdates:   []apimodel.PolicyUpdate{{State: "Failed"}},
		ResourceUpdates: []apimodel.ResourceUpdate{{State: "InProgress"}, {State: "Pending"}, {State: "Canceled"}},
	}
	counts := commandCounts(c)
	assert.Equal(t, 2, counts[components.StateDone])
	assert.Equal(t, 1, counts[components.StateFailed])
	assert.Equal(t, 1, counts[components.StateInProgress])
	assert.Equal(t, 1, counts[components.StatePending])
	assert.Equal(t, 1, counts[components.StateSkipped])

	done, total := doneOf(counts)
	assert.Equal(t, 3, done) // done + skipped
	assert.Equal(t, 6, total)
	assert.InDelta(t, 0.5, progressFraction(counts), 0.001)
}

func TestCommandHealth(t *testing.T) {
	failing := map[components.State]int{components.StateFailed: 1}
	clean := map[components.State]int{components.StateDone: 2}
	cases := []struct {
		name     string
		cmdState string
		counts   map[components.State]int
		want     health
	}{
		{"running healthy", "InProgress", clean, healthRunning},
		{"running failing", "InProgress", failing, healthRunningFailing},
		{"canceling counts as running", "Canceling", clean, healthRunning},
		{"finished ok", "Success", clean, healthFinishedOK},
		{"finished failed", "Failed", failing, healthFinishedFailed},
		{"failed command without failed updates", "Failed", clean, healthFinishedFailed},
		{"canceled command", "Canceled", clean, healthFinishedFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, commandHealth(apimodel.Command{State: tc.cmdState}, tc.counts))
		})
	}
}

func TestCommandDuration(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	start := now.Add(-90 * time.Second)
	running := apimodel.Command{State: "InProgress", StartTs: start}
	assert.Equal(t, 90*time.Second, commandDuration(running, now))

	done := apimodel.Command{State: "Success", StartTs: start, EndTs: start.Add(30 * time.Second)}
	assert.Equal(t, 30*time.Second, commandDuration(done, now))
}

func TestStateLabel_CancelStates(t *testing.T) {
	assert.Equal(t, "finishing", stateLabel("Canceling", "InProgress"))
	assert.Equal(t, "canceled", stateLabel("Canceling", "Canceled"))
	assert.Equal(t, "canceled", stateLabel("Canceled", "Canceled"))
	assert.Equal(t, "", stateLabel("InProgress", "InProgress"))
	assert.Equal(t, "", stateLabel("Canceling", "Success"))
}

func TestFilterUserCommands(t *testing.T) {
	cmds := []apimodel.Command{
		{CommandID: "a", Command: "apply", Mode: "reconcile"}, // user (pre-Source rows)
		{CommandID: "b", Command: "sync", Mode: "none"},       // internal by type
		{CommandID: "c", Command: "apply", Mode: "reconcile", Source: "auto-reconciler"},
		{CommandID: "d", Command: "destroy", Mode: "patch", Source: "stack-expirer"},
		{CommandID: "e", Command: "destroy", Mode: "patch", Source: "user"},
	}
	got := filterUserCommands(cmds)
	ids := make([]string, 0, len(got))
	for _, c := range got {
		ids = append(ids, c.CommandID)
	}
	assert.Equal(t, []string{"a", "e"}, ids)
}
