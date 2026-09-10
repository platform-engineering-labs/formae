//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package dstest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func RunPendingDeletion(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("PendingDeletion_physical_target_incarnation_observation", func(t *testing.T) {
		ds := newDS(t)
		defer func(cleanup func() error) { _ = cleanup() }(ds.CleanUpFn)
		label := "observation-" + mksuid.New().String()
		_, err := ds.CreateStack(&pkgmodel.Stack{Label: label}, "setup")
		require.NoError(t, err)
		created := &pkgmodel.Target{Label: label}
		_, err = ds.CreateTarget(created)
		require.NoError(t, err)
		target, err := ds.LoadTarget(label)
		require.NoError(t, err)
		require.NotEmpty(t, target.Health.IncarnationID)
		require.NotEmpty(t, created.ExecutionIncarnation, "write must return the committed incarnation before dependent execution")
		require.Equal(t, target.Health.IncarnationID, created.ExecutionIncarnation)
		r := &pkgmodel.Resource{Ksuid: mksuid.New().String(), Stack: label, Target: label, Label: "r", Type: "Test::Resource", Managed: true, Properties: json.RawMessage(`{}`)}
		_, err = ds.StoreResource(r, "setup", target.Health.IncarnationID)
		require.NoError(t, err)
		observation, err := ds.Datastore.(datastore.ResourceObservationReader).GetResourceObservation(r.Ksuid)
		require.NoError(t, err)
		require.NotNil(t, observation)
		require.Equal(t, target.Health.IncarnationID, observation.TargetIncarnationID)
		ru := resourceUpdate(label, r.Ksuid, r.Label, `{}`, resource_update.OperationCreate, resource_update.FormaCommandSourceUser)
		ru.DesiredState.Target = label
		ru.ResourceTarget = pkgmodel.Target{Label: label}
		command := reconcileBuilder(forma_command.CommandStateInProgress, pkgmodel.FormaApplyModeReconcile, 0, []resource_update.ResourceUpdate{ru})
		require.NoError(t, ds.StoreFormaCommand(command, command.ID))
		writer := ds.Datastore.(datastore.CommandTargetIdentityWriter)
		refs := []datastore.ResourceUpdateRef{{KSUID: r.Ksuid, Operation: ru.Operation}}
		require.NoError(t, writer.PinCommandTargetIncarnation(command.ID, label, target.Health.IncarnationID, refs))
		updates, err := ds.LoadResourceUpdates(command.ID)
		require.NoError(t, err)
		require.Len(t, updates, 1)
		require.Equal(t, target.Health.IncarnationID, updates[0].ResourceTarget.ExecutionIncarnation)
		require.NoError(t, ds.UpdateResourceUpdateState(command.ID, r.Ksuid, ru.Operation, resource_update.ResourceUpdateStateInProgress, time.Now()))
		loaded, err := ds.GetFormaCommandByCommandID(command.ID)
		require.NoError(t, err)
		require.NoError(t, ds.StoreFormaCommand(loaded, loaded.ID))
		updates, err = ds.LoadResourceUpdates(command.ID)
		require.NoError(t, err)
		require.Equal(t, target.Health.IncarnationID, updates[0].ResourceTarget.ExecutionIncarnation, "lifecycle/save must preserve durable execution provenance")
		_, err = ds.DeleteTarget(label)
		require.NoError(t, err)
		replacement := &pkgmodel.Target{Label: label}
		_, err = ds.CreateTarget(replacement)
		require.NoError(t, err)
		require.NotEqual(t, target.Health.IncarnationID, replacement.ExecutionIncarnation)
		require.NoError(t, writer.PinCommandTargetIncarnation(command.ID, label, replacement.ExecutionIncarnation, refs))
		require.ErrorContains(t, writer.PinCommandTargetIncarnation(command.ID, label, target.Health.IncarnationID, refs), "incarnation changed")
		updates, err = ds.LoadResourceUpdates(command.ID)
		require.NoError(t, err)
		require.Equal(t, replacement.ExecutionIncarnation, updates[0].ResourceTarget.ExecutionIncarnation, "late old completion cannot replace new pin")

	})

	t.Run("PendingDeletion_last_resource_cleanup_and_recreation", func(t *testing.T) {
		ds := newDS(t)
		defer func(cleanup func() error) { _ = cleanup() }(ds.CleanUpFn)
		label := "pending-" + mksuid.New().String()
		_, err := ds.CreateStack(&pkgmodel.Stack{Label: label}, "setup")
		require.NoError(t, err)
		stack, err := ds.GetStackByLabel(label)
		require.NoError(t, err)
		baseline := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, -10*time.Minute, nil)
		baseline.Stacks = []forma_command.CommandStack{{ID: stack.ID, Label: label}}
		require.NoError(t, ds.StoreFormaCommand(baseline, baseline.ID))
		r := pkgmodel.Resource{Ksuid: mksuid.New().String(), NativeID: mksuid.New().String(), Stack: label, Type: "Test::Resource", Label: "last", Target: "default", Managed: true, Properties: json.RawMessage(`{"name":"last"}`)}
		_, err = ds.StoreResource(&r, baseline.ID)
		require.NoError(t, err)
		patch := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModePatch, -time.Minute, nil)
		patch.Stacks = baseline.Stacks
		require.NoError(t, ds.StoreFormaCommand(patch, patch.ID))
		deletedKey, err := ds.DeleteResource(&r, patch.ID)
		require.NoError(t, err)
		mods, err := ds.GetResourceModificationsSinceLastReconcile(label)
		require.NoError(t, err)
		require.Len(t, mods, 1, "confirmed last-resource deletion remains actionable with no live inventory")
		require.Equal(t, "delete", mods[0].Operation)
		_, err = ds.DeleteStack(label, patch.ID)
		require.NoError(t, err)
		mods, err = ds.GetResourceModificationsSinceLastReconcile(label)
		require.NoError(t, err)
		require.Len(t, mods, 1, "automatic empty-stack cleanup preserves the evidenced incarnation")
		_, err = ds.CreateStack(&pkgmodel.Stack{Label: label}, "recreated")
		require.NoError(t, err)
		mods, err = ds.GetResourceModificationsSinceLastReconcile(label)
		require.NoError(t, err)
		require.Empty(t, mods, "reused label does not inherit unaccepted old pending drift")
		reader, ok := ds.Datastore.(datastore.ResourceObservationReader)
		require.True(t, ok, "pinned observation reader required")
		observation, err := reader.GetResourceObservation(r.Ksuid)
		require.NoError(t, err)
		require.NotNil(t, observation)
		require.Equal(t, strings.TrimPrefix(deletedKey, r.Ksuid+"_"), observation.Version)
		require.Equal(t, string(r.URI()), observation.URI)
		require.Equal(t, "delete", observation.Operation)
		require.True(t, observation.ConfirmedDeletion)
		require.NotNil(t, observation.PreviousLiveResource)
		require.NotEqual(t, observation.Version, observation.PreviousLiveResource.Version)
		require.JSONEq(t, string(r.Properties), string(observation.PreviousLiveResource.Properties))
		require.Equal(t, stack.ID, observation.StackID)
		accepted := resourceUpdate(label, r.Ksuid, r.Label, `{}`, resource_update.OperationAcceptDelete, resource_update.FormaCommandSourceUser)
		accepted.Version = observation.Version
		acceptCommand := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, 0, []resource_update.ResourceUpdate{accepted})
		acceptCommand.Stacks = baseline.Stacks
		require.NoError(t, ds.StoreFormaCommand(acceptCommand, acceptCommand.ID))
		updates, err := ds.LoadResourceUpdates(acceptCommand.ID)
		require.NoError(t, err)
		require.Len(t, updates, 1)
		require.Equal(t, observation.Version, updates[0].Version)
		mods, err = ds.GetResourceModificationsSinceLastReconcile(label)
		require.NoError(t, err)
		require.Empty(t, mods, "a reused label cannot inherit the previous incarnation's pending deletion")
	})
	t.Run("PendingDeletion_patch_only_incarnation", func(t *testing.T) {
		ds := newDS(t)
		defer func(cleanup func() error) { _ = cleanup() }(ds.CleanUpFn)
		label := "patch-only-" + mksuid.New().String()
		_, err := ds.CreateStack(&pkgmodel.Stack{Label: label}, "setup")
		require.NoError(t, err)
		stack, err := ds.GetStackByLabel(label)
		require.NoError(t, err)
		create := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModePatch, -2*time.Minute, nil)
		create.Stacks = []forma_command.CommandStack{{ID: stack.ID, Label: label}}
		require.NoError(t, ds.StoreFormaCommand(create, create.ID))
		r := pkgmodel.Resource{Ksuid: mksuid.New().String(), NativeID: mksuid.New().String(), Stack: label, Type: "Test::Resource", Label: "patch-only", Target: "default", Managed: true, Properties: json.RawMessage(`{"x":1}`)}
		_, err = ds.StoreResource(&r, create.ID)
		require.NoError(t, err)
		mods, err := ds.GetResourceModificationsSinceLastReconcile(label)
		require.NoError(t, err)
		require.Len(t, mods, 1, "evidenced patch-only drift does not need a fabricated reconcile boundary")
		create.State = forma_command.CommandStateCanceled
		require.NoError(t, ds.StoreFormaCommand(create, create.ID))
		mods, err = ds.GetResourceModificationsSinceLastReconcile(label)
		require.NoError(t, err)
		require.Len(t, mods, 1, "cancellation does not erase actual committed resource effects")

		deletion := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModePatch, -time.Minute, nil)
		deletion.State = forma_command.CommandStateCanceled
		deletion.Stacks = create.Stacks
		require.NoError(t, ds.StoreFormaCommand(deletion, deletion.ID))
		_, err = ds.DeleteResource(&r, deletion.ID)
		require.NoError(t, err)
		_, err = ds.DeleteStack(label, deletion.ID)
		require.NoError(t, err)
		mods, err = ds.GetResourceModificationsSinceLastReconcile(label)
		require.NoError(t, err)
		require.Len(t, mods, 2, "historical create and deletion remain distinct candidate operations")
		observation, err := ds.Datastore.(datastore.ResourceObservationReader).GetResourceObservation(r.Ksuid)
		require.NoError(t, err)
		require.True(t, observation.ConfirmedDeletion)
		require.NotNil(t, observation.PreviousLiveResource)
		require.NotEqual(t, observation.Version, observation.PreviousLiveResource.Version)
		require.JSONEq(t, string(r.Properties), string(observation.PreviousLiveResource.Properties))
		require.Equal(t, stack.ID, observation.StackID)
		_, err = ds.CreateStack(&pkgmodel.Stack{Label: label}, "reuse")
		require.NoError(t, err)
		mods, err = ds.GetResourceModificationsSinceLastReconcile(label)
		require.NoError(t, err)
		require.Empty(t, mods)
	})

}
