// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RunStoreResourceReadOnlyRefreshPreservesAttribution verifies that updating
// provider-observed fields in place does not transfer ownership of the physical
// resource version to the transient command which performed the read.
func RunStoreResourceReadOnlyRefreshPreservesAttribution(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StoreResource_ReadOnlyRefreshPreservesAttribution", func(t *testing.T) {
		t.Run("apply write remains the witness after refresh command deletion", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			resource := pkgmodel.Resource{
				Ksuid:              util.NewID(),
				NativeID:           "native-written",
				Stack:              "stack-written",
				Type:               "Test::Resource",
				Label:              "written",
				Target:             "target-written",
				Managed:            true,
				Properties:         json.RawMessage(`{"configured":"value"}`),
				ReadOnlyProperties: json.RawMessage(`{"observed":"before"}`),
			}
			apply := successfulResourceCommand(resource, pkgmodel.CommandApply, forma_command.SourceUser, types.OperationCreate, -2*time.Minute)
			require.NoError(t, ds.StoreFormaCommand(apply, apply.ID))
			writtenVersion, err := ds.StoreResource(&resource, apply.ID)
			require.NoError(t, err)

			refresh := successfulResourceCommand(resource, pkgmodel.CommandSync, forma_command.SourceSynchronizer, types.OperationRead, -time.Minute)
			require.NoError(t, ds.StoreFormaCommand(refresh, refresh.ID))
			refreshed := resource
			refreshed.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
			refreshedVersion, err := ds.StoreResource(&refreshed, refresh.ID)
			require.NoError(t, err)
			require.Equal(t, writtenVersion, refreshedVersion, "a read-only refresh must reuse the physical version")

			require.NoError(t, ds.DeleteFormaCommand(refresh, refresh.ID))
			loaded, err := ds.LoadResource(resource.URI())
			require.NoError(t, err)
			require.NotNil(t, loaded)
			require.Equal(t, strings.TrimPrefix(refreshedVersion, resource.Ksuid+"_"), loaded.Version)
			require.JSONEq(t, `{"observed":"after"}`, string(loaded.ReadOnlyProperties), "the latest observation must remain fresh")

			witness, err := ds.GetPropertiesAtLastWrite(resource.Ksuid)
			require.NoError(t, err)
			require.JSONEq(t, `{"configured":"value"}`, string(witness), "the apply command must remain the owner of the reused version")
		})

		t.Run("config drift remains externally attributed after a later refresh", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			_, err := ds.CreateStack(&pkgmodel.Stack{Label: "stack-drifted"}, "stack-setup")
			require.NoError(t, err)
			stack, err := ds.GetStackByLabel("stack-drifted")
			require.NoError(t, err)

			resource := pkgmodel.Resource{
				Ksuid:              util.NewID(),
				NativeID:           "native-drifted",
				Stack:              "stack-drifted",
				Type:               "Test::Resource",
				Label:              "drifted",
				Target:             "target-drifted",
				Managed:            true,
				Properties:         json.RawMessage(`{"configured":"before"}`),
				ReadOnlyProperties: json.RawMessage(`{"observed":"before"}`),
			}
			baseline := successfulResourceCommand(resource, pkgmodel.CommandApply, forma_command.SourceUser, types.OperationCreate, -3*time.Minute)
			baseline.Stacks = []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}}
			require.NoError(t, ds.StoreFormaCommand(baseline, baseline.ID))
			baselineVersion, err := ds.StoreResource(&resource, baseline.ID)
			require.NoError(t, err)
			baseline.ResourceUpdates[0].Version = baselineVersion
			require.NoError(t, ds.StoreFormaCommand(baseline, baseline.ID))

			drift := successfulResourceCommand(resource, pkgmodel.CommandSync, forma_command.SourceSynchronizer, types.OperationRead, -2*time.Minute)
			require.NoError(t, ds.StoreFormaCommand(drift, drift.ID))
			drifted := resource
			drifted.Properties = json.RawMessage(`{"configured":"outside"}`)
			driftVersion, err := ds.StoreResource(&drifted, drift.ID)
			require.NoError(t, err)
			require.NotEqual(t, baselineVersion, driftVersion, "a config change must create a physical version")

			refresh := successfulResourceCommand(drifted, pkgmodel.CommandSync, forma_command.SourceSynchronizer, types.OperationRead, -time.Minute)
			require.NoError(t, ds.StoreFormaCommand(refresh, refresh.ID))
			refreshed := drifted
			refreshed.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
			refreshedVersion, err := ds.StoreResource(&refreshed, refresh.ID)
			require.NoError(t, err)
			require.Equal(t, driftVersion, refreshedVersion, "a read-only refresh must reuse the drift version")
			require.NoError(t, ds.DeleteFormaCommand(refresh, refresh.ID))

			loaded, err := ds.LoadResource(resource.URI())
			require.NoError(t, err)
			require.NotNil(t, loaded)
			require.JSONEq(t, `{"configured":"outside"}`, string(loaded.Properties))
			require.JSONEq(t, `{"observed":"after"}`, string(loaded.ReadOnlyProperties))

			observation, err := ds.(datastore.ResourceObservationReader).GetResourceObservation(resource.Ksuid)
			require.NoError(t, err)
			require.NotNil(t, observation)
			onlyExternal, err := ds.(datastore.ExternalChangeReader).HasOnlyExternalChanges(resource.Ksuid, baseline.ID, observation.Version)
			require.NoError(t, err)

			modifications, err := ds.GetResourceModificationsSinceLastReconcile(resource.Stack)
			require.NoError(t, err)
			require.Len(t, modifications, 1, "the refresh must not detach config drift from command history")
			require.Equal(t, "update", modifications[0].Operation)
			require.JSONEq(t, `{"configured":"before"}`, string(modifications[0].OldProperties))
			require.JSONEq(t, `{"configured":"outside"}`, string(modifications[0].Properties))
			require.True(t, onlyExternal, "the config-drift sync command must remain the owner of the reused version")
		})

		t.Run("delete after read-only divergence keeps the preceding live version", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			_, err := ds.CreateStack(&pkgmodel.Stack{Label: "stack-deleted"}, "stack-setup")
			require.NoError(t, err)
			resource := pkgmodel.Resource{
				Ksuid:              util.NewID(),
				NativeID:           "native-deleted",
				Stack:              "stack-deleted",
				Type:               "Test::Resource",
				Label:              "deleted",
				Target:             "target-deleted",
				Managed:            true,
				Properties:         json.RawMessage(`{"configured":"value"}`),
				ReadOnlyProperties: json.RawMessage(`{"observed":"before"}`),
			}
			live := successfulResourceCommand(resource, pkgmodel.CommandApply, forma_command.SourceUser, types.OperationCreate, -2*time.Minute)
			require.NoError(t, ds.StoreFormaCommand(live, live.ID))
			liveVersion, err := ds.StoreResource(&resource, live.ID)
			require.NoError(t, err)

			deleting := resource
			deleting.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
			deletion := successfulResourceCommand(deleting, pkgmodel.CommandSync, forma_command.SourceSynchronizer, types.OperationDelete, -time.Minute)
			require.NoError(t, ds.StoreFormaCommand(deletion, deletion.ID))
			deletedVersion, err := ds.DeleteResource(&deleting, deletion.ID)
			require.NoError(t, err)
			require.NotEqual(t, liveVersion, deletedVersion, "a delete must create a tombstone version even when only read-only data diverged")

			observation, err := ds.(datastore.ResourceObservationReader).GetResourceObservation(resource.Ksuid)
			require.NoError(t, err)
			require.NotNil(t, observation)
			require.Equal(t, "delete", observation.Operation)
			require.True(t, observation.ConfirmedDeletion)
			require.NotNil(t, observation.PreviousLiveResource)
			require.Equal(t, strings.TrimPrefix(liveVersion, resource.Ksuid+"_"), observation.PreviousLiveResource.Version)
			require.JSONEq(t, `{"configured":"value"}`, string(observation.PreviousLiveResource.Properties))
		})

		t.Run("read-only refresh without an incarnation argument keeps the physical stamp", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			resource := pkgmodel.Resource{
				Ksuid:              util.NewID(),
				NativeID:           "native-incarnation",
				Stack:              "stack-incarnation",
				Type:               "Test::Resource",
				Label:              "incarnation",
				Target:             "target-incarnation",
				Managed:            true,
				Properties:         json.RawMessage(`{"configured":"value"}`),
				ReadOnlyProperties: json.RawMessage(`{"observed":"before"}`),
			}
			owner := successfulResourceCommand(resource, pkgmodel.CommandSync, forma_command.SourceSynchronizer, types.OperationRead, -2*time.Minute)
			require.NoError(t, ds.StoreFormaCommand(owner, owner.ID))
			_, err := ds.StoreResource(&resource, owner.ID, "incarnation-original")
			require.NoError(t, err)

			refreshed := resource
			refreshed.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
			refresh := successfulResourceCommand(refreshed, pkgmodel.CommandSync, forma_command.SourceSynchronizer, types.OperationRead, -time.Minute)
			require.NoError(t, ds.StoreFormaCommand(refresh, refresh.ID))
			_, err = ds.StoreResource(&refreshed, refresh.ID)
			require.NoError(t, err)
			rejected := refreshed
			rejected.ReadOnlyProperties = json.RawMessage(`{"observed":"rejected"}`)
			_, err = ds.StoreResource(&rejected, refresh.ID, "incarnation-different")
			require.ErrorIs(t, err, datastore.ErrResourceWriteRejected, "an explicit different incarnation must still be rejected")

			observation, err := ds.(datastore.ResourceObservationReader).GetResourceObservation(resource.Ksuid)
			require.NoError(t, err)
			require.NotNil(t, observation)
			require.Equal(t, "incarnation-original", observation.TargetIncarnationID, "an omitted guard argument must not erase the stored incarnation")
		})

		t.Run("user apply read-back remains attributed to the apply", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			resource := pkgmodel.Resource{
				Ksuid:              util.NewID(),
				NativeID:           "native-user-readback",
				Stack:              "stack-user-readback",
				Type:               "Test::Resource",
				Label:              "user-readback",
				Target:             "target-user-readback",
				Managed:            true,
				Properties:         json.RawMessage(`{"configured":"original"}`),
				ReadOnlyProperties: json.RawMessage(`{"observed":"before"}`),
			}
			created := successfulResourceCommand(resource, pkgmodel.CommandApply, forma_command.SourceUser, types.OperationCreate, -3*time.Minute)
			require.NoError(t, ds.StoreFormaCommand(created, created.ID))
			_, err := ds.StoreResource(&resource, created.ID)
			require.NoError(t, err)

			drifted := resource
			drifted.Properties = json.RawMessage(`{"configured":"value"}`)
			observed := successfulResourceCommand(drifted, pkgmodel.CommandSync, forma_command.SourceSynchronizer, types.OperationRead, -2*time.Minute)
			require.NoError(t, ds.StoreFormaCommand(observed, observed.ID))
			_, err = ds.StoreResource(&drifted, observed.ID)
			require.NoError(t, err)

			readBack := drifted
			readBack.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
			apply := successfulResourceCommand(readBack, pkgmodel.CommandApply, forma_command.SourceUser, types.OperationUpdate, -time.Minute)
			apply.ResourceUpdates[0].DesiredState.PatchDocument = json.RawMessage(`[{"op":"replace","path":"/configured","value":"value"}]`)
			require.NoError(t, ds.StoreFormaCommand(apply, apply.ID))
			storedUpdates, err := ds.LoadResourceUpdates(apply.ID)
			require.NoError(t, err)
			require.Len(t, storedUpdates, 1)
			require.JSONEq(t, string(apply.ResourceUpdates[0].DesiredState.PatchDocument), string(storedUpdates[0].DesiredState.PatchDocument))
			_, err = ds.StoreResource(&readBack, apply.ID)
			require.NoError(t, err)
			observation, err := ds.(datastore.ResourceObservationReader).GetResourceObservation(resource.Ksuid)
			require.NoError(t, err)
			require.NotNil(t, observation)
			require.Equal(t, apply.ID, observation.CommandID)

			witness, err := ds.GetPropertiesAtLastWrite(resource.Ksuid)
			require.NoError(t, err)
			require.JSONEq(t, `{"configured":"value"}`, string(witness), "a user apply read-back must retain its incoming command attribution")
		})

		for _, tc := range []struct {
			name    string
			command *forma_command.FormaCommand
		}{
			{name: "missing command", command: &forma_command.FormaCommand{ID: util.NewID()}},
			{name: "source-less sync", command: successfulResourceCommand(pkgmodel.Resource{}, pkgmodel.CommandSync, "", types.OperationRead, -time.Minute)},
			{name: "unknown-source sync", command: successfulResourceCommand(pkgmodel.Resource{}, pkgmodel.CommandSync, forma_command.Source("unknown"), types.OperationRead, -time.Minute)},
		} {
			t.Run(tc.name+" keeps incoming attribution", func(t *testing.T) {
				td := newDS(t)
				ds := td.Datastore
				defer td.CleanUpFn() //nolint:errcheck

				resource := pkgmodel.Resource{
					Ksuid:              util.NewID(),
					NativeID:           "native-conservative",
					Stack:              "stack-conservative",
					Type:               "Test::Resource",
					Label:              "conservative",
					Target:             "target-conservative",
					Managed:            true,
					Properties:         json.RawMessage(`{"configured":"value"}`),
					ReadOnlyProperties: json.RawMessage(`{"observed":"before"}`),
				}
				owner := successfulResourceCommand(resource, pkgmodel.CommandApply, forma_command.SourceUser, types.OperationCreate, -2*time.Minute)
				require.NoError(t, ds.StoreFormaCommand(owner, owner.ID))
				_, err := ds.StoreResource(&resource, owner.ID)
				require.NoError(t, err)

				incoming := tc.command
				incoming.ID = util.NewID()
				if tc.name != "missing command" {
					incoming.ResourceUpdates[0].DesiredState = resource
					require.NoError(t, ds.StoreFormaCommand(incoming, incoming.ID))
				}
				refreshed := resource
				refreshed.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
				_, err = ds.StoreResource(&refreshed, incoming.ID)
				require.NoError(t, err)

				observation, err := ds.(datastore.ResourceObservationReader).GetResourceObservation(resource.Ksuid)
				require.NoError(t, err)
				require.NotNil(t, observation)
				require.Equal(t, incoming.ID, observation.CommandID, "only a persisted sync+synchronizer command may preserve prior attribution")
			})
		}

		for _, tc := range []struct {
			name           string
			initialManaged bool
			mutate         func(*pkgmodel.Resource)
		}{
			{name: "identity change", initialManaged: true, mutate: func(resource *pkgmodel.Resource) { resource.Ksuid = util.NewID() }},
			{name: "target change", initialManaged: true, mutate: func(resource *pkgmodel.Resource) { resource.Target = "target-after" }},
			{name: "management change", mutate: func(resource *pkgmodel.Resource) { resource.Managed = true }},
		} {
			t.Run(tc.name+" remains attributed to its command", func(t *testing.T) {
				td := newDS(t)
				ds := td.Datastore
				defer td.CleanUpFn() //nolint:errcheck

				resource := pkgmodel.Resource{
					Ksuid:              util.NewID(),
					NativeID:           "native-event",
					Stack:              "stack-event",
					Type:               "Test::Resource",
					Label:              "event",
					Target:             "target-before",
					Managed:            tc.initialManaged,
					Properties:         json.RawMessage(`{"configured":"value"}`),
					ReadOnlyProperties: json.RawMessage(`{"observed":"before"}`),
				}
				observed := successfulResourceCommand(resource, pkgmodel.CommandSync, forma_command.SourceSynchronizer, types.OperationRead, -2*time.Minute)
				require.NoError(t, ds.StoreFormaCommand(observed, observed.ID))
				_, err := ds.StoreResource(&resource, observed.ID)
				require.NoError(t, err)

				changed := resource
				tc.mutate(&changed)
				changed.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
				event := successfulResourceCommand(changed, pkgmodel.CommandApply, forma_command.SourceUser, types.OperationCreate, -time.Minute)
				require.NoError(t, ds.StoreFormaCommand(event, event.ID))
				_, err = ds.StoreResource(&changed, event.ID)
				require.NoError(t, err)

				witness, err := ds.GetPropertiesAtLastWrite(changed.Ksuid)
				require.NoError(t, err)
				require.JSONEq(t, `{"configured":"value"}`, string(witness), "the metadata change must remain attributed to the apply command")
			})
		}
	})
}

func successfulResourceCommand(resource pkgmodel.Resource, command pkgmodel.Command, source forma_command.Source, operation types.OperationType, age time.Duration) *forma_command.FormaCommand {
	now := util.TimeNow().Add(age)
	return &forma_command.FormaCommand{
		ID:         util.NewID(),
		Command:    command,
		Source:     source,
		Config:     config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		State:      forma_command.CommandStateSuccess,
		StartTs:    now,
		ModifiedTs: now,
		ResourceUpdates: []resource_update.ResourceUpdate{{
			Operation:    operation,
			State:        resource_update.ResourceUpdateStateSuccess,
			DesiredState: resource,
		}},
	}
}
