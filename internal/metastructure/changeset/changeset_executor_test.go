// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package changeset

import (
	"testing"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
)

func TestChangesetExecutor_EmptyChangesetFinishesImmediately(t *testing.T) {
	emptyChangeset := Changeset{
		CommandID:      "test-command-empty",
		Pipeline:       &ResourceUpdatePipeline{},
		trackedUpdates: make(map[string]bool),
	}

	executor, sender, err := newChangesetExecutorForTest(t)
	assert.NoError(t, err, "Failed to spawn changeset executor")
	executor.SendMessage(sender, Start{Changeset: emptyChangeset})

	executor.ShouldNotSend().
		Message(resource_update.ResourceUpdate{}).
		Once().
		Assert()
}

// newChangesetExecutorForTest spawns a ChangesetExecutor for testing purposes
func newChangesetExecutorForTest(t *testing.T) (*unit.TestActor, gen.PID, error) {
	sender := gen.PID{Node: "test", ID: 100}

	executor, err := unit.Spawn(t, NewChangesetExecutor, unit.WithArgs(sender), unit.WithLogLevel(gen.LogLevelDebug))
	if err != nil {
		return nil, gen.PID{}, err
	}

	return executor, sender, nil
}