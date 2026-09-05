// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/stretchr/testify/require"
)

// A policy-only operation must preserve each resource's properties and identity,
// including a renamed parent and the reference from its child. Rebuilding from
// defaults silently changes Name/Value and drops collections during chaos runs.
func TestSetTTLPolicyPreservesResources(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		stacks, resources, stack int
		ids                      []int
	}{
		{"flat", 1, 2, 0, []int{0, 1}},
		{"tree", 1, 10, 0, []int{0, 1, 5}},
		{"cross_stack", 2, 10, 1, []int{0, 1, 5, 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
				h := NewTestHarness(t, 10*time.Second)
				defer h.Cleanup()
				model := NewStateModel(tc.stacks, tc.resources)
				h.SetupStacks(t, model, PropertyTestConfig{StackCount: tc.stacks, ResourceCount: tc.resources})
				apply := Operation{Kind: OpApply, ApplyMode: "reconcile", StackIndex: tc.stack,
					ResourceIDs: tc.ids, RenameSlotIndex: 0, RenameNewLabel: "renamed-parent",
					Properties:      `{"Name":"NAME","Value":"v2","SetTags":["keep"],"EntityTags":[{"Key":"env","Value":"test"}],"OrderedItems":["first","second"]}`,
					ChildProperties: `{"Name":"NAME","ParentId":"PARENT_ID","Value":"child-v2"}`}
				h.ExecuteOperation(t, &apply, model)
				require.Len(t, model.AcceptedCommands, 1, "setup apply must be accepted")
				command := h.WaitForCommandDone(model.AcceptedCommands[0].CommandID, 30*time.Second)
				require.Equal(t, "Success", command.State, "setup apply must fully succeed")
				h.DrainPendingCommands(t, model, 30*time.Second)
				require.Equal(t, 1, h.RenamesAccepted)
				before, _, err := h.extractManagedAndUnmanagedInventory()
				require.NoError(t, err)
				require.Len(t, before, len(tc.ids)+tc.stacks-1)

				policy := Operation{Kind: OpSetTTLPolicy, StackIndex: tc.stack, TTLExpired: false}
				h.ExecuteOperation(t, &policy, model)
				h.DrainPendingCommands(t, model, 30*time.Second)
				stacks, err := h.client.ListStacks()
				require.NoError(t, err)
				policyFound := false
				for _, stack := range stacks {
					if stack.Label == model.Stack(tc.stack).Label {
						require.Len(t, stack.Policies, 1)
						var persisted struct {
							Type, OnDependents string
							TTLSeconds         int
						}
						require.NoError(t, json.Unmarshal(stack.Policies[0], &persisted))
						require.Equal(t, "ttl", persisted.Type)
						require.Equal(t, 86400, persisted.TTLSeconds)
						require.Equal(t, "cascade", persisted.OnDependents)
						policyFound = true
					}
				}
				require.True(t, policyFound, "TTL policy must persist, not merely leave resources unchanged")
				after, _, err := h.extractManagedAndUnmanagedInventory()
				require.NoError(t, err)
				require.Len(t, after, len(before))
				for _, original := range before {
					found := false
					for _, current := range after {
						if current.Label != original.Label {
							continue
						}
						found = true
						require.Equal(t, original.NativeID, current.NativeID, original.Label)
						require.Equal(t, original.Ksuid, current.Ksuid, original.Label)
						require.JSONEq(t, string(original.Properties), string(current.Properties), original.Label)
					}
					require.True(t, found, "missing resource %s", original.Label)
				}
			})
		})
	}
}
