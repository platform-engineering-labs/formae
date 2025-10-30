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
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
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

func TestChangesetExecutor_SingleResourceUpdate(t *testing.T) {
	t.Skip("This needs to be written to a workflow test")

	var (
		commandID      = "test-command-single"
		resourceUpdate = newTestResourceUpdate("test-vpc", nil, "AWS::EC2::VPC")
		updateUri      = createOperationURI(resourceUpdate.URI(), resourceUpdate.Operation)
		changeset      = Changeset{
			CommandID: commandID,
			Pipeline: &ResourceUpdatePipeline{
				ResourceUpdateGroups: map[pkgmodel.FormaeURI]*ResourceUpdateGroup{
					updateUri: {
						Updates: []*resource_update.ResourceUpdate{&resourceUpdate},
					},
				},
			},
			trackedUpdates: make(map[string]bool),
		}
	)

	executor, sender, err := newChangesetExecutorForTest(t)
	assert.NoError(t, err, "Failed to spawn changeset executor")

	// Start the changeset + verify resource updater is started
	executor.SendMessage(sender, Start{Changeset: changeset})
	executor.ShouldLog().
		Level(gen.LogLevelDebug).
		Containing("Starting resource updater").
		Once().
		Assert()
	executor.ShouldCall().
		To(newFakeSupervisorPID(actornames.ResourceUpdaterSupervisor)).
		Request(resource_update.EnsureResourceUpdater{
			ResourceURI: resourceUpdate.URI(),
			Operation:   string(resourceUpdate.Operation),
			CommandID:   commandID,
		}).
		Once().
		Assert()

	executor.ShouldSend().
		To(newFakeUpdaterPID(resourceUpdate.URI(), string(resourceUpdate.Operation), commandID)).
		Message(resource_update.StartResourceUpdate{
			ResourceUpdate: resourceUpdate,
			CommandID:      commandID,
		}).
		Once().
		Assert()

	// Finish resource update + verify changeset completes
	executor.SendMessage(sender, resource_update.ResourceUpdateFinished{
		Uri:   resourceUpdate.URI(),
		State: resource_update.ResourceUpdateStateSuccess,
	})

	executor.ShouldSend().
		To(sender).
		Message(ChangesetCompleted{
			CommandID: commandID,
			State:     ChangeSetStateFinishedSuccessfully,
		}).
		Once().
		Assert()

	executor.ShouldLog().
		Level(gen.LogLevelDebug).
		Containing("Changeset execution finished").
		Once().
		Assert()
}

func TestChangesetExecutor_DependentResources(t *testing.T) {
	t.Skip("This needs to be written to a workflow test")

	commandID := "test-command-deps"
	vpcUpdate := newTestResourceUpdate("test-vpc", []pkgmodel.FormaeURI{}, "AWS::EC2::VPC")
	subnetUpdate := newTestResourceUpdate(
		"test-subnet",
		[]pkgmodel.FormaeURI{
			vpcUpdate.URI(),
		},
		"AWS::EC2::Subnet",
	)

	vpcUpdateUri := createOperationURI(vpcUpdate.URI(), vpcUpdate.Operation)
	subnetUpdateUri := createOperationURI(subnetUpdate.URI(), subnetUpdate.Operation)

	// Set up the pipeline with dependency relationship
	vpcGroup := &ResourceUpdateGroup{
		URI:              vpcUpdateUri,
		Updates:          []*resource_update.ResourceUpdate{&vpcUpdate},
		DownstreamGroups: []*ResourceUpdateGroup{},
		UpstreamGroups:   []*ResourceUpdateGroup{},
	}

	subnetGroup := &ResourceUpdateGroup{
		URI:              subnetUpdateUri,
		Updates:          []*resource_update.ResourceUpdate{&subnetUpdate},
		DownstreamGroups: []*ResourceUpdateGroup{},
		UpstreamGroups:   []*ResourceUpdateGroup{vpcGroup},
	}
	vpcGroup.DownstreamGroups = []*ResourceUpdateGroup{subnetGroup}

	changeset := Changeset{
		CommandID: commandID,
		Pipeline: &ResourceUpdatePipeline{
			ResourceUpdateGroups: map[pkgmodel.FormaeURI]*ResourceUpdateGroup{
				vpcUpdateUri:    vpcGroup,
				subnetUpdateUri: subnetGroup,
			},
		},
		trackedUpdates: make(map[string]bool),
	}

	executor, sender, err := newChangesetExecutorForTest(t)
	assert.NoError(t, err, "Failed to spawn changeset executor")

	// Start the vpc update and ensure subnet hasn't started
	executor.SendMessage(sender, Start{Changeset: changeset})
	executor.ShouldSend().
		To(newFakeUpdaterPID(vpcUpdate.URI(), string(vpcUpdate.Operation), commandID)).
		Message(resource_update.StartResourceUpdate{
			ResourceUpdate: vpcUpdate,
			CommandID:      commandID,
		}).
		Once().
		Assert()
	executor.ShouldNotSend().
		To(newFakeUpdaterPID(subnetUpdate.URI(), string(subnetUpdate.Operation), commandID)).
		Message(resource_update.StartResourceUpdate{
			ResourceUpdate: subnetUpdate,
			CommandID:      commandID,
		}).
		Assert()

	// Ensure the resolve cache has started
	executor.ShouldCall().
		To(newFakeSupervisorPID(actornames.ChangesetSupervisor)).
		Request(EnsureResolveCache{
			CommandID: commandID,
		}).
		Once().
		Assert()

	// Ensure finishing the vpc update begins the subnet update
	executor.SendMessage(sender, resource_update.ResourceUpdateFinished{
		Uri:   vpcUpdate.URI(),
		State: resource_update.ResourceUpdateStateSuccess,
	})
	executor.ShouldSend().
		To(newFakeUpdaterPID(subnetUpdate.URI(), string(subnetUpdate.Operation), commandID)).
		Message(resource_update.StartResourceUpdate{
			ResourceUpdate: subnetUpdate,
			CommandID:      commandID,
		}).
		Once().
		Assert()

	// Ensure Subnet finishes successfully, resolve cache terminates
	executor.SendMessage(sender, resource_update.ResourceUpdateFinished{
		Uri:   subnetUpdate.URI(),
		State: resource_update.ResourceUpdateStateSuccess,
	})

	executor.ShouldSend().
		To(newFakeResolveCachePID(commandID)).
		Message(Shutdown{}).
		Once().
		Assert()

	executor.ShouldLog().
		Level(gen.LogLevelDebug).
		Containing("Changeset execution finished").
		Once().
		Assert()
}

func TestChangesetExecutor_CascadeFailure(t *testing.T) {
	t.Skip("This needs to be written to a workflow test")

	commandID := "test-command-deps-failure"
	vpcUpdate := newTestResourceUpdate("test-vpc", []pkgmodel.FormaeURI{}, "AWS::EC2::VPC")
	subnetUpdate := newTestResourceUpdate(
		"test-subnet",
		[]pkgmodel.FormaeURI{
			vpcUpdate.URI(),
		},
		"AWS::EC2::Subnet",
	)

	vpcUpdateUri := createOperationURI(vpcUpdate.URI(), vpcUpdate.Operation)
	subnetUpdateUri := createOperationURI(subnetUpdate.URI(), subnetUpdate.Operation)

	vpcGroup := &ResourceUpdateGroup{
		URI:              vpcUpdateUri,
		Updates:          []*resource_update.ResourceUpdate{&vpcUpdate},
		DownstreamGroups: []*ResourceUpdateGroup{},
		UpstreamGroups:   []*ResourceUpdateGroup{},
	}

	subnetGroup := &ResourceUpdateGroup{
		URI:              subnetUpdateUri,
		Updates:          []*resource_update.ResourceUpdate{&subnetUpdate},
		DownstreamGroups: []*ResourceUpdateGroup{},
		UpstreamGroups:   []*ResourceUpdateGroup{vpcGroup},
	}
	vpcGroup.DownstreamGroups = []*ResourceUpdateGroup{subnetGroup}

	changeset := Changeset{
		CommandID: commandID,
		Pipeline: &ResourceUpdatePipeline{
			ResourceUpdateGroups: map[pkgmodel.FormaeURI]*ResourceUpdateGroup{
				vpcUpdateUri:    vpcGroup,
				subnetUpdateUri: subnetGroup,
			},
		},
		trackedUpdates: make(map[string]bool),
	}

	executor, sender, err := newChangesetExecutorForTest(t)
	assert.NoError(t, err, "Failed to spawn changeset executor")

	executor.SendMessage(sender, Start{Changeset: changeset})

	executor.ShouldSend().
		To(newFakeUpdaterPID(vpcUpdate.URI(), string(vpcUpdate.Operation), commandID)).
		Message(resource_update.StartResourceUpdate{
			ResourceUpdate: vpcUpdate,
			CommandID:      commandID,
		}).
		Once().
		Assert()

	executor.ShouldNotSend().
		To(newFakeUpdaterPID(subnetUpdate.URI(), string(subnetUpdate.Operation), commandID)).
		Message(resource_update.StartResourceUpdate{
			ResourceUpdate: subnetUpdate,
			CommandID:      commandID,
		}).
		Assert()

	executor.ShouldCall().
		To(newFakeSupervisorPID(actornames.ChangesetSupervisor)).
		Request(EnsureResolveCache{
			CommandID: commandID,
		}).
		Once().
		Assert()

	executor.SendMessage(sender, resource_update.ResourceUpdateFinished{
		Uri:   vpcUpdate.URI(),
		State: resource_update.ResourceUpdateStateFailed,
	})
	executor.ShouldNotSend().
		To(newFakeUpdaterPID(subnetUpdate.URI(), string(subnetUpdate.Operation), commandID)).
		Message(resource_update.StartResourceUpdate{
			ResourceUpdate: subnetUpdate,
			CommandID:      commandID,
		}).
		Assert()

	executor.ShouldSend().
		To(newFakeResolveCachePID(commandID)).
		Message(Shutdown{}).
		Once().
		Assert()

	executor.ShouldLog().
		Level(gen.LogLevelDebug).
		Containing("Changeset execution finished").
		Once().
		Assert()
}

func TestChangesetExecutor_HashesAllResourcesOnCompletion(t *testing.T) {
	t.Skip("This needs to be written to a workflow test")

	commandID := "test-command-hashing"
	resourceUpdate := newTestResourceUpdate("test-secret", nil, "AWS::SecretsManager::Secret")
	updateUri := createOperationURI(resourceUpdate.URI(), resourceUpdate.Operation)

	changeset := Changeset{
		CommandID: commandID,
		Pipeline: &ResourceUpdatePipeline{
			ResourceUpdateGroups: map[pkgmodel.FormaeURI]*ResourceUpdateGroup{
				updateUri: {
					Updates: []*resource_update.ResourceUpdate{&resourceUpdate},
				},
			},
		},
		trackedUpdates: make(map[string]bool),
	}

	executor, sender, err := newChangesetExecutorForTest(t)
	assert.NoError(t, err, "Failed to spawn changeset executor")

	executor.SendMessage(sender, Start{Changeset: changeset})

	executor.ShouldCall().
		To(newFakeSupervisorPID(actornames.ResourceUpdaterSupervisor)).
		Request(resource_update.EnsureResourceUpdater{
			ResourceURI: resourceUpdate.URI(),
			Operation:   string(resourceUpdate.Operation),
			CommandID:   commandID,
		}).
		Once().
		Assert()

	// Complete with success
	executor.SendMessage(sender, resource_update.ResourceUpdateFinished{
		Uri:   resourceUpdate.URI(),
		State: resource_update.ResourceUpdateStateSuccess,
	})

	// Verify hashing is called on completion
	executor.ShouldCall().
		To(gen.ProcessID{Name: gen.Atom("FormaCommandPersister"), Node: "test@localhost"}).
		Request(forma_persister.MarkFormaCommandAsComplete{
			CommandID: commandID,
		}).
		Once().
		Assert()

	executor.ShouldLog().
		Level(gen.LogLevelDebug).
		Containing("Changeset execution finished").
		Once().
		Assert()
}

func TestChangesetExecutor_HashesAllResourcesOnFailure(t *testing.T) {
	t.Skip("This needs to be written to a workflow test")

	commandID := "test-command-hashing-failure"
	resourceUpdate := newTestResourceUpdate("test-secret", nil, "AWS::SecretsManager::Secret")
	updateUri := createOperationURI(resourceUpdate.URI(), resourceUpdate.Operation)

	changeset := Changeset{
		CommandID: commandID,
		Pipeline: &ResourceUpdatePipeline{
			ResourceUpdateGroups: map[pkgmodel.FormaeURI]*ResourceUpdateGroup{
				updateUri: {
					Updates: []*resource_update.ResourceUpdate{&resourceUpdate},
				},
			},
		},
		trackedUpdates: make(map[string]bool),
	}

	executor, sender, err := newChangesetExecutorForTest(t)
	assert.NoError(t, err, "Failed to spawn changeset executor")

	executor.SendMessage(sender, Start{Changeset: changeset})

	// Complete with failure
	executor.SendMessage(sender, resource_update.ResourceUpdateFinished{
		Uri:   resourceUpdate.URI(),
		State: resource_update.ResourceUpdateStateFailed,
	})

	// Verify hashing is called, despite failure
	executor.ShouldCall().
		To(gen.ProcessID{Name: gen.Atom("FormaCommandPersister"), Node: "test@localhost"}).
		Request(forma_persister.MarkFormaCommandAsComplete{
			CommandID: commandID,
		}).
		Once().
		Assert()

	executor.ShouldLog().
		Level(gen.LogLevelDebug).
		Containing("Changeset execution finished").
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
