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

// A CloudCreate that reuses the native id of an already-ingested unmanaged
// resource is skipped: discovery only ingests unknown native ids, so fresh
// CloudProperties expectations for a known id could never converge through
// the discovery path. After the skip, a discovery trigger must still
// converge cleanly.
func TestCloudCreate_RepeatedNativeIDIsSkipped(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 30*time.Second)
		defer h.Cleanup()
		h.ResetAgentState(t)
		config := PropertyTestConfig{ResourceCount: 10, StackCount: 1}
		model := NewStateModel(config.StackCount, config.ResourceCount)
		h.SetupStacks(t, model, config)

		create := Operation{Kind: OpCloudCreate, NativeID: "cloud-1",
			ResourceType: "Test::Generic::Resource",
			Properties:   `{"Name":"cp-1","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`}
		h.ExecuteOperation(t, &create, model)
		h.executeTriggerDiscovery(t, model)
		require.True(t, model.UnmanagedResources["cloud-1"].PresentInInventory)
		ingested := model.UnmanagedResources["cloud-1"].CloudProperties

		recreate := Operation{Kind: OpCloudCreate, NativeID: "cloud-1",
			ResourceType: "Test::Generic::Resource",
			Properties:   `{"Name":"cp-other","Value":"v2","SetTags":[],"EntityTags":[],"OrderedItems":[]}`}
		h.ExecuteOperation(t, &recreate, model)

		require.Equal(t, ingested, model.UnmanagedResources["cloud-1"].CloudProperties,
			"a repeated CloudCreate must not rewrite the tracked cloud properties")
		require.True(t, h.waitForUnmanagedInventoryExpectations(t, model, 10*time.Second),
			"the unmanaged inventory must still converge after the skipped re-create")
	})
}
