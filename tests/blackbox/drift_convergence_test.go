// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/stretchr/testify/require"
)

// Repeating an external write can leave inventory unchanged. A no-op sync has
// no durable command, but the requested observed state must still converge.
func TestDriftConvergence_RepeatedExternalWrite(t *testing.T) {
	for _, managed := range []bool{true, false} {
		name := "unmanaged"
		if managed {
			name = "managed"
		}
		t.Run(name, func(t *testing.T) {
			testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
				h := NewTestHarness(t, 30*time.Second)
				defer h.Cleanup()
				model := NewStateModel(1, 10)
				h.SetupStacks(t, model, PropertyTestConfig{ResourceCount: 10, StackCount: 1})
				props := `{"Name":"external","Value":"repeated","SetTags":[],"EntityTags":[],"OrderedItems":[]}`
				op := Operation{Kind: OpCloudModify, CloudTargetManaged: managed, Properties: props}
				query := "managed:true"
				var nativeID string
				if managed {
					inventory, err := h.client.ExtractResources("managed:true")
					require.NoError(t, err)
					require.NotNil(t, inventory)
					require.Len(t, inventory.Resources, 1)
					model.SetNativeID(0, 0, inventory.Resources[0].NativeID)
					_, _, _, _, _, nativeID, _ = model.FindDriftEligibleResource(0)
					require.NotEmpty(t, nativeID)
				} else {
					query = "managed:false"
					nativeID = "repeated-cloud-write"
					create := Operation{Kind: OpCloudCreate, NativeID: nativeID, ResourceType: "Test::Generic::Resource", Properties: `{"Name":"external","Value":"before","SetTags":[],"EntityTags":[],"OrderedItems":[]}`}
					h.ExecuteOperation(t, &create, model)
					h.executeTriggerDiscovery(t, model)
					// A prior sync can observe the external write before the helper
					// captures its receipt baseline. The later no-op must still
					// advance this resource's expected inventory state.
					h.putCloudStateWithRetry(t, nativeID, "Test::Generic::Resource", props)
					h.forceSyncAndAwait(t, nil, time.Second)
					require.True(t, h.waitForAbsorbedInventory(t, query, nativeID, props, false, 10*time.Second))
					op.NativeID = nativeID
				}
				h.ExecuteOperation(t, &op, model)
				require.True(t, h.waitForAbsorbedInventory(t, query, nativeID, props, false, time.Second))
				require.False(t, h.waitForAbsorbedInventory(t, query, nativeID, `{"Name":"wrong"}`, false, 100*time.Millisecond), "convergence must still reject different properties")
				h.ExecuteOperation(t, &op, model)
				require.True(t, h.waitForAbsorbedInventory(t, query, nativeID, props, false, time.Second))
				if !managed {
					require.JSONEq(t, props, model.UnmanagedResources[nativeID].InventoryProperties)
				}
			})
		})
	}
}
