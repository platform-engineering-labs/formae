// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/drift"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestDestroyedDriftCandidatesRemainGuarded(t *testing.T) {
	for _, mutation := range []string{"resource-identity", "stack-label", "command-history"} {
		t.Run(mutation, func(t *testing.T) {
			m, writer, f, _ := scopedFixture(t)
			resource, err := m.Datastore.LoadResourceById("a")
			require.NoError(t, err)
			storeDesired(t, m.Datastore, *resource, resource_update.OperationCreate, forma_command.CommandStateSuccess)
			stack, err := m.Datastore.GetStackByLabel("a")
			require.NoError(t, err)
			destroy := &forma_command.FormaCommand{ID: util.NewID(), Command: pkgmodel.CommandDestroy, State: forma_command.CommandStateSuccess, Source: forma_command.SourceUser, StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: "a"}}, ResourceUpdates: []resource_update.ResourceUpdate{{Operation: resource_update.OperationDelete, Source: resource_update.FormaCommandSourceUser, StackLabel: "a", DesiredState: *resource}}}
			require.NoError(t, m.Datastore.StoreFormaCommand(destroy, destroy.ID))
			_, err = m.Datastore.DeleteResource(resource, destroy.ID)
			require.NoError(t, err)
			_, err = m.Datastore.DeleteStack("a", destroy.ID)
			require.NoError(t, err)
			plan, err := m.prepareGuardedApply(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
			require.NoError(t, err)
			switch mutation {
			case "resource-identity":
				moved := *resource
				moved.Stack = "b"
				_, err = writer.StoreResource(&moved, "concurrent-move")
			case "stack-label":
				_, err = writer.CreateStack(&pkgmodel.Stack{Label: "a"}, "concurrent-recreate")
			case "command-history":
				destroy.State = forma_command.CommandStateCanceled
				err = writer.StoreFormaCommand(destroy, destroy.ID)
			}
			require.NoError(t, err)
			require.ErrorIs(t, admitScopedPlan(t, m, plan), datastore.ErrStaleAdmission, "filtered historical drift still contributes guarded dependencies")
		})
	}
}

// A resource can disappear before cleanup destroys the remaining stack. Its
// physical tombstone still belongs to sync, not the later explicit destroy.
func TestRetiredStackSyncDeletionDoesNotBlockRecreation(t *testing.T) {
	m, writer, f, _ := scopedFixture(t)
	ds := m.Datastore
	remaining, err := ds.LoadResourceById("a")
	require.NoError(t, err)
	storeDesired(t, ds, *remaining, resource_update.OperationCreate, forma_command.CommandStateSuccess)
	stack, err := ds.GetStackByLabel("a")
	require.NoError(t, err)
	missing := *remaining
	missing.Ksuid = "missing-before-destroy"
	missing.Label = "missing-before-destroy"
	failed := &forma_command.FormaCommand{ID: util.NewID(), Command: pkgmodel.CommandApply, State: forma_command.CommandStateFailed, Source: forma_command.SourceUser, StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: "a"}}, ResourceUpdates: []resource_update.ResourceUpdate{{Operation: resource_update.OperationCreate, Source: resource_update.FormaCommandSourceUser, StackLabel: "a", DesiredState: missing}}}
	require.NoError(t, ds.StoreFormaCommand(failed, failed.ID))
	_, err = ds.StoreResource(&missing, failed.ID)
	require.NoError(t, err)
	sync := &forma_command.FormaCommand{ID: util.NewID(), Command: pkgmodel.CommandSync, State: forma_command.CommandStateSuccess, Source: forma_command.SourceSynchronizer, StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: "a"}}}
	require.NoError(t, ds.StoreFormaCommand(sync, sync.ID))
	_, err = ds.DeleteResource(&missing, sync.ID)
	require.NoError(t, err)
	mods, err := drift.LoadModifications(ds, "a")
	require.NoError(t, err)
	require.NotEmpty(t, mods, "sync deletion on the live incarnation remains actionable")
	var deleted bool
	for _, mod := range mods {
		if mod.Ksuid == missing.Ksuid && mod.Operation == "delete" {
			deleted = true
		}
	}
	require.True(t, deleted)

	destroy := &forma_command.FormaCommand{ID: util.NewID(), Command: pkgmodel.CommandDestroy, State: forma_command.CommandStateSuccess, Source: forma_command.SourceUser, StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: "a"}}, ResourceUpdates: []resource_update.ResourceUpdate{{Operation: resource_update.OperationDelete, Source: resource_update.FormaCommandSourceUser, StackLabel: "a", DesiredState: *remaining}}}
	require.NoError(t, ds.StoreFormaCommand(destroy, destroy.ID))
	_, err = ds.DeleteResource(remaining, destroy.ID)
	require.NoError(t, err)
	_, err = ds.DeleteStack("a", destroy.ID)
	require.NoError(t, err)
	desired, err := ds.GetResourcesAtLastReconcile("a")
	require.NoError(t, err)
	require.Empty(t, desired)
	observation, err := ds.(datastore.ResourceObservationReader).GetResourceObservation(missing.Ksuid)
	require.NoError(t, err)
	require.Equal(t, sync.ID, observation.CommandID, "destroy did not rewrite the earlier sync tombstone")

	options := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}
	plan, err := m.prepareGuardedApply(f, options, "client", "subject", "")
	require.NoError(t, err, "fresh reconcile cannot confront a retired incarnation")
	require.True(t, plan.Command.HasChanges())
	mods, err = drift.LoadModifications(ds, "a")
	require.NoError(t, err)
	require.Empty(t, mods)
	_, err = writer.CreateStack(&pkgmodel.Stack{Label: "a"}, "concurrent-recreate")
	require.NoError(t, err)
	require.ErrorIs(t, admitScopedPlan(t, m, plan), datastore.ErrStaleAdmission, "certified absence must protect concurrent recreation")
	current, err := ds.GetStackByLabel("a")
	require.NoError(t, err)
	require.NotEqual(t, stack.ID, current.ID)
	mods, err = drift.LoadModifications(ds, "a")
	require.NoError(t, err)
	require.Empty(t, mods, "recreated label inherits no old sync deletion")
	_, err = m.prepareGuardedApply(f, options, "client", "subject", "")
	require.NoError(t, err)
}
