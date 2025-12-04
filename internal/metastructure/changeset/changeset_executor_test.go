// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package changeset

import (
	"fmt"
	"testing"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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

// newFakeUpdaterPID fakes a ProcessID for the resource updater based on KSUID
func newFakeUpdaterPID(uri pkgmodel.FormaeURI, operation string, commandID string) gen.ProcessID {
	return gen.ProcessID{
		Name: actornames.ResourceUpdater(uri, operation, commandID),
		Node: "test@localhost",
	}
}

// newFakeSupervisorPID fakes a ProcessID for the specified supervisor actor
func newFakeSupervisorPID(actorName gen.Atom) gen.ProcessID {
	return gen.ProcessID{
		Name: actorName,
		Node: "test@localhost",
	}
}

// newFakeResolveCachePID fakes a ProcessID for the resolve cache based on the given command ID
func newFakeResolveCachePID(commandID string) gen.ProcessID {
	return gen.ProcessID{
		Name: gen.Atom(fmt.Sprintf("formae://changeset/resolve-cache/%s", commandID)),
		Node: "test@localhost",
	}
}

// newTestResourceUpdate creates a resource update with the given label and dependencies.
func newTestResourceUpdate(label string, dependencies []pkgmodel.FormaeURI, resourceType string) resource_update.ResourceUpdate {
	uri := newTestFormaeURI()

	return resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: label,
			Type:  resourceType,
			Stack: "test-stack",
			Ksuid: uri.KSUID(),
		},
		Operation:            resource_update.OperationCreate,
		State:                resource_update.ResourceUpdateStateNotStarted,
		StartTs:              util.TimeNow(),
		RemainingResolvables: dependencies,
		StackLabel:           "test-stack",
	}
}

func newTestFormaeURI() pkgmodel.FormaeURI {
	return pkgmodel.FormaeURI(fmt.Sprintf("formae://%s#/", util.NewID()))
}
