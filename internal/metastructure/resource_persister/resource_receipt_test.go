// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_persister

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	pkgresource "github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

type receiptTestFixture struct {
	persister    *unit.TestActor
	sender       gen.PID
	datastore    datastore.Datastore
	resource     pkgmodel.Resource
	prior        pkgmodel.Resource
	applyCommand *forma_command.FormaCommand
}

func newReceiptTestFixture(t *testing.T, managed bool) receiptTestFixture {
	t.Helper()

	persister, sender, ds, err := newResourcePersisterForTest(t)
	require.NoError(t, err)
	for _, label := range []string{"receipt-target", "other-target"} {
		_, err = ds.CreateTarget(&pkgmodel.Target{Label: label, Namespace: "test"})
		require.NoError(t, err)
	}

	stack := "$unmanaged"
	if managed {
		stack = "receipt-stack"
	}
	res := pkgmodel.Resource{
		Ksuid:              util.NewID(),
		NativeID:           "receipt-native-id",
		Stack:              stack,
		Type:               "Test::Receipt",
		Label:              "receipt-resource",
		Target:             "receipt-target",
		Managed:            managed,
		Properties:         json.RawMessage(`{"configured":"value"}`),
		ReadOnlyProperties: json.RawMessage(`{"observed":"before"}`),
	}
	apply := receiptTestCommand("receipt-apply", pkgmodel.CommandApply, forma_command.SourceUser, res)
	apply.State = forma_command.CommandStateSuccess
	apply.ResourceUpdates[0].Operation = resource_update.OperationCreate
	apply.ResourceUpdates[0].State = resource_update.ResourceUpdateStateSuccess
	require.NoError(t, ds.StoreFormaCommand(apply, apply.ID))

	create := receiptReadUpdate(res, pkgmodel.Resource{}, resource_update.FormaCommandSourceUser)
	create.Operation = resource_update.OperationCreate
	create.ProgressResult[0].Operation = pkgresource.OperationCreate
	result := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         apply.ID,
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   pkgresource.OperationCreate,
		ResourceUpdate:    create,
	})
	require.NoError(t, result.Error)
	require.NotEmpty(t, result.Response.(resource_update.PersistResourceUpdateResult).Version)

	prior, err := ds.LoadResource(res.URI())
	require.NoError(t, err)
	require.NotNil(t, prior)
	require.NotEmpty(t, prior.Version)

	return receiptTestFixture{
		persister:    persister,
		sender:       sender,
		datastore:    ds,
		resource:     res,
		prior:        *prior,
		applyCommand: apply,
	}
}

func receiptTestCommand(id string, command pkgmodel.Command, source forma_command.Source, res pkgmodel.Resource) *forma_command.FormaCommand {
	now := util.TimeNow().Add(-time.Minute)
	return &forma_command.FormaCommand{
		ID:         id,
		Command:    command,
		Source:     source,
		State:      forma_command.CommandStateInProgress,
		StartTs:    now,
		ModifiedTs: now,
		ResourceUpdates: []resource_update.ResourceUpdate{{
			DesiredState: res,
			Operation:    resource_update.OperationRead,
			State:        resource_update.ResourceUpdateStateInProgress,
			StackLabel:   res.Stack,
		}},
	}
}

func receiptReadUpdate(desired, prior pkgmodel.Resource, source resource_update.FormaCommandSource) resource_update.ResourceUpdate {
	return resource_update.ResourceUpdate{
		DesiredState:   desired,
		PriorState:     prior,
		ResourceTarget: pkgmodel.Target{Label: desired.Target, Namespace: "test"},
		Operation:      resource_update.OperationRead,
		State:          resource_update.ResourceUpdateStateSuccess,
		Source:         source,
		StackLabel:     desired.Stack,
		ProgressResult: []plugin.TrackedProgress{{
			ProgressResult: pkgresource.ProgressResult{
				Operation:          pkgresource.OperationRead,
				OperationStatus:    pkgresource.OperationStatusSuccess,
				NativeID:           desired.NativeID,
				ResourceProperties: desired.Properties,
			},
			ResourceType: desired.Type,
			StartTs:      util.TimeNow(),
			ModifiedTs:   util.TimeNow(),
			Attempts:     1,
		}},
	}
}

func callReceiptRead(t *testing.T, fixture receiptTestFixture, commandID string, logicalOperation resource_update.OperationType, update resource_update.ResourceUpdate) string {
	t.Helper()
	result := fixture.persister.Call(fixture.sender, resource_update.PersistResourceUpdate{
		CommandID:         commandID,
		ResourceOperation: logicalOperation,
		PluginOperation:   pkgresource.OperationRead,
		ResourceUpdate:    update,
	})
	require.NoError(t, result.Error)
	response, ok := result.Response.(resource_update.PersistResourceUpdateResult)
	require.True(t, ok, "expected PersistResourceUpdateResult, got %T", result.Response)
	require.Empty(t, response.Error)
	return response.Version
}

func storeReceiptReadCommand(t *testing.T, fixture receiptTestFixture, id string, source forma_command.Source, desired pkgmodel.Resource) *forma_command.FormaCommand {
	t.Helper()
	command := receiptTestCommand(id, pkgmodel.CommandSync, source, desired)
	require.NoError(t, fixture.datastore.StoreFormaCommand(command, command.ID))
	return command
}

func priorReceipt(prior pkgmodel.Resource) string {
	return fmt.Sprintf("%s_%s", prior.Ksuid, prior.Version)
}

func TestResourcePersister_SuppressesFreshSameVersionBackgroundReadReceipt(t *testing.T) {
	fixture := newReceiptTestFixture(t, true)
	desired := fixture.prior
	desired.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
	syncCommand := storeReceiptReadCommand(t, fixture, "receipt-refresh", forma_command.SourceSynchronizer, desired)
	update := receiptReadUpdate(desired, fixture.prior, resource_update.FormaCommandSourceSynchronize)

	receipt := callReceiptRead(t, fixture, syncCommand.ID, resource_update.OperationRead, update)
	require.Empty(t, receipt, "a fresh background read that reused the prior physical version must not add a history event")

	current, err := fixture.datastore.LoadResource(fixture.resource.URI())
	require.NoError(t, err)
	require.NotNil(t, current)
	require.Equal(t, fixture.prior.Version, current.Version, "the observation refresh must reuse the physical version")
	require.JSONEq(t, `{"observed":"after"}`, string(current.ReadOnlyProperties), "the fresh observation must still be persisted")

	// Empty sync receipts are pruned by the command finalizer. Simulate that
	// final step and prove Task 1 kept the reused resource row owned by its
	// original apply command.
	require.NoError(t, fixture.datastore.DeleteFormaCommand(syncCommand, syncCommand.ID))
	observation, err := fixture.datastore.(datastore.ResourceObservationReader).GetResourceObservation(fixture.resource.Ksuid)
	require.NoError(t, err)
	require.NotNil(t, observation)
	require.Equal(t, fixture.applyCommand.ID, observation.CommandID)
	require.JSONEq(t, `{"observed":"after"}`, string(observation.Resource.ReadOnlyProperties))
}

func TestResourcePersister_BackgroundReadKeepsReceiptForPhysicalTargetChange(t *testing.T) {
	fixture := newReceiptTestFixture(t, true)
	desired := fixture.prior
	desired.Target = "other-target"
	desired.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
	syncCommand := storeReceiptReadCommand(t, fixture, "receipt-target-change", forma_command.SourceSynchronizer, desired)
	update := receiptReadUpdate(desired, fixture.prior, resource_update.FormaCommandSourceSynchronize)

	receipt := callReceiptRead(t, fixture, syncCommand.ID, resource_update.OperationRead, update)
	require.Equal(t, priorReceipt(fixture.prior), receipt,
		"a target change can reuse the physical version but must keep its action-bearing receipt")

	observation, err := fixture.datastore.(datastore.ResourceObservationReader).GetResourceObservation(fixture.resource.Ksuid)
	require.NoError(t, err)
	require.NotNil(t, observation)
	require.Equal(t, "other-target", observation.Target)
	require.Equal(t, syncCommand.ID, observation.CommandID,
		"Task 1 deliberately retains incoming ownership for a physical target change")
}

func TestResourcePersister_BackgroundReadKeepsReceiptForManagedStateMismatch(t *testing.T) {
	fixture := newReceiptTestFixture(t, true)
	desired := fixture.prior
	desired.Managed = false
	desired.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
	syncCommand := storeReceiptReadCommand(t, fixture, "receipt-managed-mismatch", forma_command.SourceSynchronizer, desired)
	update := receiptReadUpdate(desired, fixture.prior, resource_update.FormaCommandSourceSynchronize)

	receipt := callReceiptRead(t, fixture, syncCommand.ID, resource_update.OperationRead, update)
	require.Equal(t, priorReceipt(fixture.prior), receipt,
		"an ownership metadata mismatch must retain a receipt even though sync preserves the stored managed state")

	current, err := fixture.datastore.LoadResource(fixture.resource.URI())
	require.NoError(t, err)
	require.True(t, current.Managed, "sync must preserve the current managed state")
}

func TestResourcePersister_BackgroundReadKeepsReceiptForNewLogicalIdentity(t *testing.T) {
	fixture := newReceiptTestFixture(t, false)
	desired := fixture.prior
	desired.Ksuid = util.NewID()
	desired.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
	syncCommand := storeReceiptReadCommand(t, fixture, "receipt-identity-change", forma_command.SourceSynchronizer, desired)
	update := receiptReadUpdate(desired, fixture.prior, resource_update.FormaCommandSourceSynchronize)

	receipt := callReceiptRead(t, fixture, syncCommand.ID, resource_update.OperationRead, update)
	require.Equal(t, priorReceipt(fixture.prior), receipt,
		"adopting the existing unmanaged identity must remain visible even when the datastore reuses its version")
}

func TestResourcePersister_BackgroundNonReadPluginOperationKeepsReceipt(t *testing.T) {
	fixture := newReceiptTestFixture(t, true)
	desired := fixture.prior
	desired.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
	syncCommand := storeReceiptReadCommand(t, fixture, "receipt-plugin-update", forma_command.SourceSynchronizer, desired)
	update := receiptReadUpdate(desired, fixture.prior, resource_update.FormaCommandSourceSynchronize)
	update.ProgressResult[0].Operation = pkgresource.OperationUpdate

	result := fixture.persister.Call(fixture.sender, resource_update.PersistResourceUpdate{
		CommandID:         syncCommand.ID,
		ResourceOperation: resource_update.OperationRead,
		PluginOperation:   pkgresource.OperationUpdate,
		ResourceUpdate:    update,
	})
	require.NoError(t, result.Error)
	receipt := result.Response.(resource_update.PersistResourceUpdateResult).Version
	require.Equal(t, priorReceipt(fixture.prior), receipt,
		"only a plugin Read may suppress an in-place receipt")
}

func TestResourcePersister_BackgroundNonReadLogicalOperationKeepsReceipt(t *testing.T) {
	fixture := newReceiptTestFixture(t, true)
	desired := fixture.prior
	desired.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
	syncCommand := storeReceiptReadCommand(t, fixture, "receipt-logical-create", forma_command.SourceSynchronizer, desired)
	update := receiptReadUpdate(desired, fixture.prior, resource_update.FormaCommandSourceSynchronize)

	receipt := callReceiptRead(t, fixture, syncCommand.ID, resource_update.OperationCreate, update)
	require.Equal(t, priorReceipt(fixture.prior), receipt,
		"only a logical Read may suppress an in-place receipt")
}

func TestResourcePersister_NonRefreshReadsKeepTheirReceipts(t *testing.T) {
	t.Run("discovery read-only change", func(t *testing.T) {
		fixture := newReceiptTestFixture(t, false)
		desired := fixture.prior
		desired.ReadOnlyProperties = json.RawMessage(`{"observed":"after"}`)
		command := storeReceiptReadCommand(t, fixture, "receipt-discovery", forma_command.SourceDiscovery, desired)
		update := receiptReadUpdate(desired, fixture.prior, resource_update.FormaCommandSourceDiscovery)

		receipt := callReceiptRead(t, fixture, command.ID, resource_update.OperationRead, update)
		require.Equal(t, priorReceipt(fixture.prior), receipt)

		observation, err := fixture.datastore.(datastore.ResourceObservationReader).GetResourceObservation(fixture.resource.Ksuid)
		require.NoError(t, err)
		require.Equal(t, command.ID, observation.CommandID)
	})

	t.Run("configuration change", func(t *testing.T) {
		fixture := newReceiptTestFixture(t, true)
		desired := fixture.prior
		desired.Properties = json.RawMessage(`{"configured":"outside"}`)
		command := storeReceiptReadCommand(t, fixture, "receipt-config-change", forma_command.SourceSynchronizer, desired)
		update := receiptReadUpdate(desired, fixture.prior, resource_update.FormaCommandSourceSynchronize)

		receipt := callReceiptRead(t, fixture, command.ID, resource_update.OperationRead, update)
		require.NotEmpty(t, receipt)
		require.NotEqual(t, priorReceipt(fixture.prior), receipt)
	})

	t.Run("not found deletion", func(t *testing.T) {
		fixture := newReceiptTestFixture(t, true)
		command := storeReceiptReadCommand(t, fixture, "receipt-delete", forma_command.SourceSynchronizer, fixture.prior)
		update := receiptReadUpdate(fixture.prior, fixture.prior, resource_update.FormaCommandSourceSynchronize)
		update.ProgressResult[0].ErrorCode = pkgresource.OperationErrorCodeNotFound

		receipt := callReceiptRead(t, fixture, command.ID, resource_update.OperationRead, update)
		require.NotEmpty(t, receipt)
		require.NotEqual(t, priorReceipt(fixture.prior), receipt)

		observation, err := fixture.datastore.(datastore.ResourceObservationReader).GetResourceObservation(fixture.resource.Ksuid)
		require.NoError(t, err)
		require.Equal(t, string(resource_update.OperationDelete), observation.Operation)
	})

	t.Run("filter eviction", func(t *testing.T) {
		fixture := newReceiptTestFixture(t, false)
		desired := fixture.prior
		desired.ReadOnlyProperties = json.RawMessage(`{"observed":"after","Filtered":"yes"}`)
		command := storeReceiptReadCommand(t, fixture, "receipt-filter-eviction", forma_command.SourceSynchronizer, desired)
		update := receiptReadUpdate(desired, fixture.prior, resource_update.FormaCommandSourceSynchronize)
		update.MatchFilters = []pkgmodel.MatchFilter{{
			ResourceTypes: []string{desired.Type},
			Conditions:    []pkgmodel.FilterCondition{{PropertyPath: "$.Filtered", PropertyValue: "yes"}},
		}}

		receipt := callReceiptRead(t, fixture, command.ID, resource_update.OperationRead, update)
		require.NotEmpty(t, receipt)
		require.NotEqual(t, priorReceipt(fixture.prior), receipt)

		observation, err := fixture.datastore.(datastore.ResourceObservationReader).GetResourceObservation(fixture.resource.Ksuid)
		require.NoError(t, err)
		require.Equal(t, string(resource_update.OperationDelete), observation.Operation)
	})

	t.Run("new discovery identity", func(t *testing.T) {
		fixture := newReceiptTestFixture(t, false)
		desired := fixture.prior
		desired.Ksuid = util.NewID()
		desired.NativeID = "new-native-id"
		command := storeReceiptReadCommand(t, fixture, "receipt-new-discovery", forma_command.SourceDiscovery, desired)
		update := receiptReadUpdate(desired, pkgmodel.Resource{}, resource_update.FormaCommandSourceDiscovery)

		receipt := callReceiptRead(t, fixture, command.ID, resource_update.OperationRead, update)
		require.NotEmpty(t, receipt)
		require.NotEqual(t, priorReceipt(fixture.prior), receipt)
	})
}
