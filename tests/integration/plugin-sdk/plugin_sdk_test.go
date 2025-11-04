// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package pluginsdk_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/tests/integration/plugin-sdk/framework"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getPluginPath returns the path to the plugin being tested
// This is determined by the PLUGIN_NAME environment variable
func getPluginPath(t *testing.T) string {
	pluginName := os.Getenv("PLUGIN_NAME")
	if pluginName == "" {
		t.Fatal("PLUGIN_NAME environment variable must be set (e.g., 'aws', 'azure')")
	}

	// Path relative to this test file: ../../../plugins/{pluginName}
	pluginPath := filepath.Join("..", "..", "..", "plugins", pluginName)

	// Verify the plugin directory exists
	if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
		t.Fatalf("Plugin directory does not exist: %s", pluginPath)
	}

	return pluginPath
}

// TestPluginSDK_CRUD runs the CRUD lifecycle test for all resources in the plugin's testdata
func TestPluginSDK_CRUD(t *testing.T) {
	pluginPath := getPluginPath(t)

	// Discover all test cases in the plugin's testdata directory
	testCases, err := framework.DiscoverTestData(pluginPath)
	require.NoError(t, err, "Failed to discover test data")

	t.Logf("Discovered %d test case(s) for plugin at %s", len(testCases), pluginPath)

	// Create test harness once for all test cases
	harness := framework.NewTestHarness(t)
	defer harness.Cleanup()

	// Start the agent once
	err = harness.StartAgent()
	require.NoError(t, err, "Failed to start formae agent")

	// Run each test case as a subtest
	for _, tc := range testCases {
		tc := tc // Capture range variable
		t.Run(tc.Name, func(t *testing.T) {
			runCRUDTest(t, harness, tc)
		})
	}
}

// compareProperties compares expected properties against actual properties from inventory
func compareProperties(t *testing.T, expectedProperties map[string]any, actualResource map[string]any, context string) {
	if actualProperties, ok := actualResource["Properties"].(map[string]any); ok {
		t.Logf("Comparing expected properties with actual properties (%s)...", context)
		for key, expectedValue := range expectedProperties {
			actualValue, exists := actualProperties[key]
			assert.True(t, exists, "Property %s should exist in actual resource (%s)", key, context)
			if exists {
				assert.Equal(t, expectedValue, actualValue,
					"Property %s should match expected value (%s)", key, context)
			}
		}
		t.Logf("All expected properties matched (%s)!", context)
	} else {
		t.Errorf("Could not extract Properties from inventory resource (%s)", context)
	}
}

// runCRUDTest runs the CRUD lifecycle test for a single resource
func runCRUDTest(t *testing.T, harness *framework.TestHarness, tc framework.TestCase) {
	t.Logf("Testing resource: %s (file: %s)", tc.Name, tc.PKLFile)

	// Step 1: Eval the PKL file to get expected state
	t.Log("Step 1: Evaluating PKL file to get expected state...")
	expectedOutput, err := harness.Eval(tc.PKLFile)
	require.NoError(t, err, "Eval command should succeed")

	// Parse eval output to get expected properties
	var evalResult framework.InventoryResponse
	err = json.Unmarshal([]byte(expectedOutput), &evalResult)
	require.NoError(t, err, "Should be able to parse eval output")
	require.NotEmpty(t, evalResult.Resources, "Eval should return at least one resource")

	expectedResource := evalResult.Resources[0]
	actualResourceType := expectedResource["Type"].(string)
	expectedProperties := expectedResource["Properties"].(map[string]any)
	t.Logf("Expected resource type: %s", actualResourceType)
	t.Logf("Expected properties: %+v", expectedProperties)

	// Step 2: Apply the forma to create the resource
	t.Log("Step 2: Applying forma to create resource...")
	applyCommandID, err := harness.Apply(tc.PKLFile)
	require.NoError(t, err, "Apply command should succeed")
	assert.NotEmpty(t, applyCommandID, "Apply should return a command ID")

	// Step 3: Poll for apply command to complete successfully
	t.Log("Step 3: Polling for apply command completion...")
	applyStatus, err := harness.PollStatus(applyCommandID, 5*time.Minute)
	require.NoError(t, err, "Apply command should complete successfully")
	assert.Equal(t, "Success", applyStatus, "Apply command should reach Success state")

	// Step 4: Verify resource exists in inventory with correct properties
	t.Log("Step 4: Verifying resource in inventory...")
	inventory, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	require.NoError(t, err, "Inventory command should succeed")
	assert.NotNil(t, inventory, "Inventory should return a response")
	assert.Len(t, inventory.Resources, 1, "Inventory should contain exactly 1 resource")

	actualResource := inventory.Resources[0]
	t.Logf("Found resource in inventory with type: %s", actualResource["Type"])

	// Verify the resource type matches
	assert.Equal(t, actualResourceType, actualResource["Type"], "Resource type should match")

	// Compare properties using helper
	compareProperties(t, expectedProperties, actualResource, "after create")

	// Step 5: Force synchronization to read actual state from cloud
	t.Log("Step 5: Forcing synchronization...")
	err = harness.Sync()
	require.NoError(t, err, "Sync command should succeed")

	// Step 6: Wait for synchronization to complete
	t.Log("Step 6: Waiting for synchronization to complete...")
	err = harness.WaitForSyncCompletion(30 * time.Second)
	require.NoError(t, err, "Synchronization should complete successfully")

	// Step 7: Verify inventory still matches expected state (idempotency check)
	t.Log("Step 7: Verifying resource state unchanged after sync (idempotency)...")
	inventoryAfterSync, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	require.NoError(t, err, "Inventory command should succeed after sync")
	assert.Len(t, inventoryAfterSync.Resources, 1, "Inventory should still contain exactly 1 resource")

	resourceAfterSync := inventoryAfterSync.Resources[0]

	// Compare properties using helper - verifies idempotency
	compareProperties(t, expectedProperties, resourceAfterSync, "after sync")
	t.Log("Idempotency verified!")

	// Step 8: Destroy the resource
	t.Log("Step 8: Destroying resource...")
	destroyCommandID, err := harness.Destroy(tc.PKLFile)
	require.NoError(t, err, "Destroy command should succeed")
	assert.NotEmpty(t, destroyCommandID, "Destroy should return a command ID")

	// Step 9: Poll for destroy command to complete successfully
	t.Log("Step 9: Polling for destroy command completion...")
	destroyStatus, err := harness.PollStatus(destroyCommandID, 5*time.Minute)
	require.NoError(t, err, "Destroy command should complete successfully")
	assert.Equal(t, "Success", destroyStatus, "Destroy command should reach Success state")

	// Step 10: Verify resource no longer exists in inventory
	t.Log("Step 10: Verifying resource removed from inventory...")
	inventoryAfterDestroy, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	require.NoError(t, err, "Inventory command should succeed after destroy")
	assert.Len(t, inventoryAfterDestroy.Resources, 0, "Inventory should be empty after destroy")

	t.Logf("CRUD test completed successfully for %s!", tc.Name)
}
