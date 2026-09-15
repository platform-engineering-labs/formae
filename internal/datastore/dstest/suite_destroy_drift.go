// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/drift"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func RunExplicitDestroyDrift(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	for _, state := range []forma_command.CommandState{forma_command.CommandStateSuccess, forma_command.CommandStateFailed, forma_command.CommandStateCanceled} {
		t.Run("ExplicitDestroyDrift_"+string(state), func(t *testing.T) {
			ds := newDS(t)
			defer func(cleanup func() error) { _ = cleanup() }(ds.CleanUpFn)
			label := "destroy-" + util.NewID()
			_, err := ds.CreateStack(&pkgmodel.Stack{Label: label}, "setup")
			require.NoError(t, err)
			stack, err := ds.GetStackByLabel(label)
			require.NoError(t, err)
			create := resourceUpdate(label, util.NewID(), "removed", `{"foo":"v1"}`, resource_update.OperationCreate, resource_update.FormaCommandSourceUser)
			create.DesiredState.Managed = true
			keep := resourceUpdate(label, util.NewID(), "remaining", `{"foo":"v1"}`, resource_update.OperationCreate, resource_update.FormaCommandSourceUser)
			keep.DesiredState.Managed = true
			initial := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, -10*time.Minute, []resource_update.ResourceUpdate{create, keep})
			initial.Stacks = []forma_command.CommandStack{{ID: stack.ID, Label: label}}
			require.NoError(t, ds.StoreFormaCommand(initial, initial.ID))
			_, err = ds.StoreResource(&create.DesiredState, initial.ID)
			require.NoError(t, err)
			_, err = ds.StoreResource(&keep.DesiredState, initial.ID)
			require.NoError(t, err)
			deletion := create
			deletion.Operation = resource_update.OperationDelete
			destroy := destroyBuilder(state, -5*time.Minute, []resource_update.ResourceUpdate{deletion})
			destroy.Stacks = initial.Stacks
			if state == forma_command.CommandStateFailed {
				failedDelete := keep
				failedDelete.Operation = resource_update.OperationDelete
				failedDelete.State = resource_update.ResourceUpdateStateFailed
				destroy.ResourceUpdates = append(destroy.ResourceUpdates, failedDelete)
			}
			require.NoError(t, ds.StoreFormaCommand(destroy, destroy.ID))
			_, err = ds.DeleteResource(&create.DesiredState, destroy.ID)
			require.NoError(t, err)
			normalized := func() []datastore.ResourceModification {
				t.Helper()
				byStack := map[string][]datastore.ResourceModification{}
				e := drift.LoadModificationsAndWitnesses(ds.Datastore, label, byStack, map[string]json.RawMessage{}, map[string]pkgmodel.OwnedMembers{})
				require.NoError(t, e)
				return byStack[label]
			}
			raw, err := ds.GetResourceModificationsSinceLastReconcile(label)
			require.NoError(t, err)
			require.NotEmpty(t, raw, "raw history retains the deletion")
			if state == forma_command.CommandStateCanceled {
				require.NotEmpty(t, normalized(), "canceled destroy is not accepted desired intent")
				return
			}
			require.Empty(t, normalized(), "explicit partial destroy is already desired absence")
			// Mixed failed destroy: the absent resource is settled, a still-live resource
			// deleted only in desired intent must still be confronted when it drifts.
			patched := keep.DesiredState
			patched.Properties = []byte(`{"foo":"v2"}`)
			patch := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModePatch, -4*time.Minute, nil)
			patch.Stacks = initial.Stacks
			require.NoError(t, ds.StoreFormaCommand(patch, patch.ID))
			_, err = ds.StoreResource(&patched, patch.ID)
			require.NoError(t, err)
			mods := normalized()
			require.Len(t, mods, 1)
			require.Equal(t, keep.DesiredState.Ksuid, mods[0].Ksuid, "live drift survives the other resource's destroy")
			// A later failed create is durable desired intent despite no new live row.
			failedCreate := reconcileBuilder(forma_command.CommandStateFailed, pkgmodel.FormaApplyModeReconcile, -3*time.Minute, []resource_update.ResourceUpdate{create})
			failedCreate.Stacks = initial.Stacks
			require.NoError(t, ds.StoreFormaCommand(failedCreate, failedCreate.ID))
			snapshots, err := ds.GetResourcesAtLastReconcile(label)
			require.NoError(t, err)
			expectedDesired := 2
			if state == forma_command.CommandStateFailed {
				expectedDesired = 1
			}
			require.Len(t, snapshots, expectedDesired, "failed create declaration is not erased by historical destroy")
			// Remove both resources explicitly and then remove the stack.
			keep.Operation = resource_update.OperationDelete
			final := destroyBuilder(forma_command.CommandStateSuccess, -2*time.Minute, []resource_update.ResourceUpdate{deletion, keep})
			final.Stacks = initial.Stacks
			require.NoError(t, ds.StoreFormaCommand(final, final.ID))
			_, err = ds.DeleteResource(&keep.DesiredState, final.ID)
			require.NoError(t, err)
			_, err = ds.DeleteStack(label, final.ID)
			require.NoError(t, err)
			require.Empty(t, normalized(), "deleted stack does not require obsolete drift review")
			_, err = ds.CreateStack(&pkgmodel.Stack{Label: label}, "new-incarnation")
			require.NoError(t, err)
			current, err := ds.GetStackByLabel(label)
			require.NoError(t, err)
			require.NotEqual(t, stack.ID, current.ID)
			require.Empty(t, normalized(), "new incarnation has no inherited drift")
			live := resourceUpdate(label, util.NewID(), "new", `{"foo":"v3"}`, resource_update.OperationCreate, resource_update.FormaCommandSourceUser)
			live.DesiredState.Managed = true
			newPatch := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModePatch, -time.Minute, []resource_update.ResourceUpdate{live})
			newPatch.Stacks = []forma_command.CommandStack{{ID: current.ID, Label: label}}
			require.NoError(t, ds.StoreFormaCommand(newPatch, newPatch.ID))
			_, err = ds.StoreResource(&live.DesiredState, newPatch.ID)
			require.NoError(t, err)
			mods = normalized()
			require.Len(t, mods, 1)
			require.Equal(t, live.DesiredState.Ksuid, mods[0].Ksuid, "new incarnation patch remains reviewable")
		})
	}
}
