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

// Discovery lists child resources by filtering their ParentId against the
// parent's current Name. Renaming a parent out-of-band BEFORE its children
// were ever ingested therefore orphans them: the child list runs with the
// new name, the children still carry the old one, and no discovery pass can
// reach them. The model must predict exactly that — parent ingested, orphaned
// children never ingested — and inventory must still converge.
func TestCloudModify_RenamedParentOrphansUndiscoveredChildren(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 30*time.Second)
		defer h.Cleanup()
		h.ResetAgentState(t)
		config := PropertyTestConfig{ResourceCount: 10, StackCount: 1}
		model := NewStateModel(config.StackCount, config.ResourceCount)
		h.SetupStacks(t, model, config)

		create := Operation{Kind: OpCloudCreate, NativeID: "cloud-1",
			ResourceType: "Test::Generic::Resource",
			Properties:   `{"Name":"cp-parent","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`,
			CloudChildren: []CloudChildResource{
				{NativeID: "cloud-1-child-0", ResourceType: "Test::Generic::ChildResource",
					Properties: `{"Name":"cp-child","ParentId":"cp-parent","Value":"v1"}`},
				{NativeID: "cloud-1-gc-0", ResourceType: "Test::Generic::GrandchildResource",
					Properties: `{"Name":"cp-gc","ParentId":"cp-child","Value":"v1"}`},
			}}
		h.ExecuteOperation(t, &create, model)

		modify := Operation{Kind: OpCloudModify, CloudTargetManaged: false, NativeID: "cloud-1",
			Properties: `{"Name":"cp-renamed","Value":"v2","SetTags":[],"EntityTags":[],"OrderedItems":[]}`}
		h.ExecuteOperation(t, &modify, model)

		h.executeTriggerDiscovery(t, model)

		require.True(t, model.UnmanagedResources["cloud-1"].PresentInInventory,
			"the renamed parent itself is top-level listable and must ingest")
		require.False(t, model.UnmanagedResources["cloud-1-child-0"].PresentInInventory,
			"a child pointing at the parent's old name is unreachable for discovery")
		require.False(t, model.UnmanagedResources["cloud-1-gc-0"].PresentInInventory,
			"a grandchild under an unreachable child is transitively unreachable")
		require.True(t, h.waitForUnmanagedInventoryExpectations(t, model, 10*time.Second),
			"inventory must converge on the parent alone")
	})
}
