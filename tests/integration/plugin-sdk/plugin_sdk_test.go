// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build plugin_sdk

package pluginsdk_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/integration/plugin-sdk/framework"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isResolvable checks if a value is a resolvable reference ($res: true)
func isResolvable(value any) bool {
	m, ok := value.(map[string]any)
	if !ok {
		return false
	}
	res, exists := m["$res"]
	if !exists {
		return false
	}
	resBool, ok := res.(bool)
	return ok && resBool
}

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

// TestPluginSDK_Lifecycle runs the resource lifecycle test for all resources in the plugin's testdata
func TestPluginSDK_Lifecycle(t *testing.T) {
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
			runLifecycleTest(t, harness, tc)
		})
	}
}

// compareTags compares two tag slices ignoring order
// Tags are expected to be []any where each element is map[string]any with "Key" and "Value"
func compareTags(t *testing.T, expected, actual any, context string) bool {
	expectedTags, ok := expected.([]any)
	if !ok {
		t.Errorf("Expected tags is not a slice (%s)", context)
		return false
	}
	actualTags, ok := actual.([]any)
	if !ok {
		t.Errorf("Actual tags is not a slice (%s)", context)
		return false
	}

	if len(expectedTags) != len(actualTags) {
		t.Errorf("Tag count mismatch: expected %d, got %d (%s)", len(expectedTags), len(actualTags), context)
		return false
	}

	// Build a map of expected tags for easy lookup
	expectedMap := make(map[string]string)
	for _, tag := range expectedTags {
		tagMap, ok := tag.(map[string]any)
		if !ok {
			t.Errorf("Expected tag is not a map (%s)", context)
			return false
		}
		key, _ := tagMap["Key"].(string)
		value, _ := tagMap["Value"].(string)
		expectedMap[key] = value
	}

	// Check each actual tag exists in expected
	for _, tag := range actualTags {
		tagMap, ok := tag.(map[string]any)
		if !ok {
			t.Errorf("Actual tag is not a map (%s)", context)
			return false
		}
		key, _ := tagMap["Key"].(string)
		value, _ := tagMap["Value"].(string)
		expectedValue, exists := expectedMap[key]
		if !exists {
			t.Errorf("Unexpected tag key %q in actual tags (%s)", key, context)
			return false
		}
		if expectedValue != value {
			t.Errorf("Tag %q value mismatch: expected %q, got %q (%s)", key, expectedValue, value, context)
			return false
		}
	}

	return true
}

// findTargetResource finds the resource matching the test case's ResourceType from a list of resources.
// Returns the matching resource, or falls back to the last resource if no match found (dependencies are listed first).
func findTargetResource(resources []map[string]any, resourceType string) map[string]any {
	targetTypeNorm := strings.ToLower(strings.ReplaceAll(resourceType, "-", ""))
	for _, res := range resources {
		if resType, ok := res["Type"].(string); ok {
			// Normalize the type (e.g., "Provider::Service::ResourceName" -> "resourcename")
			typeParts := strings.Split(resType, "::")
			typeName := strings.ToLower(typeParts[len(typeParts)-1])
			if typeName == targetTypeNorm {
				return res
			}
		}
	}
	// Fallback to last resource if no match found
	return resources[len(resources)-1]
}

// compareProperties compares expected properties against actual properties from inventory
func compareProperties(t *testing.T, expectedProperties map[string]any, actualResource map[string]any, context string) {
	if actualProperties, ok := actualResource["Properties"].(map[string]any); ok {
		t.Logf("Comparing expected properties with actual properties (%s)...", context)
		for key, expectedValue := range expectedProperties {
			actualValue, exists := actualProperties[key]
			assert.True(t, exists, "Property %s should exist in actual resource (%s)", key, context)
			if exists {
				// Validate resolvable properties - they should be resolved at apply time
				if isResolvable(expectedValue) {
					t.Logf("Validating resolvable property %s (resolved at runtime)", key)

					// Verify the actual value is also a resolvable
					if !isResolvable(actualValue) {
						t.Errorf("Expected resolvable but got non-resolvable for property %s (%s)", key, context)
						continue
					}

					// Both are resolvables - now validate the resolved value and metadata
					expectedMap := expectedValue.(map[string]any)
					actualMap := actualValue.(map[string]any)

					// Check that $value exists and is non-empty in the actual resource
					resolvedValue, hasValue := actualMap["$value"]
					if !hasValue {
						t.Errorf("Resolvable property %s missing $value in actual resource (%s)", key, context)
						continue
					}
					if resolvedValue == "" {
						t.Errorf("Resolvable property %s has empty $value in actual resource (%s)", key, context)
						continue
					}
					t.Logf("Resolvable property %s resolved to: %v", key, resolvedValue)

					// Validate that key resolvable metadata fields match
					for _, field := range []string{"$label", "$type", "$stack", "$property"} {
						expectedFieldValue, expectedHasField := expectedMap[field]
						actualFieldValue, actualHasField := actualMap[field]

						// Only validate if both have the field (some fields may be optional)
						if expectedHasField && actualHasField && expectedFieldValue != actualFieldValue {
							t.Errorf("Resolvable property %s field %s mismatch: expected %v, got %v (%s)",
								key, field, expectedFieldValue, actualFieldValue, context)
						}
					}

					continue
				}
				// Use order-independent comparison for Tags
				if key == "Tags" {
					compareTags(t, expectedValue, actualValue, context)
				} else {
					assert.Equal(t, expectedValue, actualValue,
						"Property %s should match expected value (%s)", key, context)
				}
			}
		}
		t.Logf("All expected properties matched (%s)!", context)
	} else {
		t.Errorf("Could not extract Properties from inventory resource (%s)", context)
	}
}

// runLifecycleTest runs the resource lifecycle test for a single resource
func runLifecycleTest(t *testing.T, harness *framework.TestHarness, tc framework.TestCase) {
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

	// Find the target resource by matching the test case's ResourceType to the resource Type
	expectedResource := findTargetResource(evalResult.Resources, tc.ResourceType)

	actualResourceType := expectedResource["Type"].(string)
	expectedProperties := expectedResource["Properties"].(map[string]any)
	t.Logf("Expected resource type: %s", actualResourceType)
	t.Logf("Expected properties: %+v", expectedProperties)

	// Step 2: Wait for plugin to be registered before any commands
	t.Log("Step 2: Waiting for plugin to be registered...")
	namespace := strings.Split(actualResourceType, "::")[0]
	err = harness.WaitForPluginRegistered(namespace, 30*time.Second)
	require.NoError(t, err, "Plugin should register within timeout")

	// Step 3: Apply the forma to create the resource
	t.Log("Step 3: Applying forma to create resource...")
	applyCommandID, err := harness.Apply(tc.PKLFile)
	require.NoError(t, err, "Apply command should succeed")
	assert.NotEmpty(t, applyCommandID, "Apply should return a command ID")

	// Step 4: Poll for apply command to complete successfully
	t.Log("Step 4: Polling for apply command completion...")
	applyStatus, err := harness.PollStatus(applyCommandID, 5*time.Minute)
	require.NoError(t, err, "Apply command should complete successfully")
	assert.Equal(t, "Success", applyStatus, "Apply command should reach Success state")

	// Step 5: Verify resource exists in inventory with correct properties
	t.Log("Step 5: Verifying resource in inventory...")
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

	// Check if resource is extractable before running Step 5
	// Query the resource plugin to get the actual extractable value
	shouldSkipExtract := false
	descriptor, err := harness.GetResourceDescriptor(actualResourceType)
	if err != nil {
		// Can't determine extractability without the descriptor, skip extract validation
		// The extracted PKL files require proper PklProject context which isn't available in temp dir
		t.Logf("Skipping extract validation: failed to get resource descriptor for %s: %v", actualResourceType, err)
		shouldSkipExtract = true
	} else if !descriptor.Extractable {
		t.Logf("Skipping extract validation: resource type %s has extractable=false", actualResourceType)
		shouldSkipExtract = true
	}

	// Step 6: Extract resource and verify it matches original
	if !shouldSkipExtract {
		t.Log("Step 6: Extracting resource to PKL and verifying...")

		// Create temp directory for extracted files
		extractDir, err := os.MkdirTemp("", "formae-extract-test-*")
		require.NoError(t, err, "Should be able to create temp directory for extraction")
		t.Logf("Created extract directory: %s", extractDir)

		// Extract the resource
		extractFile := filepath.Join(extractDir, "extracted.pkl")
		err = harness.Extract(fmt.Sprintf("type:%s", actualResourceType), extractFile)
		require.NoError(t, err, "Extract command should succeed")

		// Eval the extracted PKL file
		extractedOutput, err := harness.Eval(extractFile)
		require.NoError(t, err, "Eval of extracted file should succeed")

		// Parse extracted eval output
		var extractedResult framework.InventoryResponse
		err = json.Unmarshal([]byte(extractedOutput), &extractedResult)
		require.NoError(t, err, "Should be able to parse extracted eval output")
		require.NotEmpty(t, extractedResult.Resources, "Extracted eval should return at least one resource")

		// Get the extracted resource properties
		extractedResource := extractedResult.Resources[0]
		extractedProperties := extractedResource["Properties"].(map[string]any)

		// Compare properties from extracted file with original
		t.Log("Comparing extracted properties with original expected properties...")
		for key, expectedValue := range expectedProperties {
			actualValue, exists := extractedProperties[key]
			assert.True(t, exists, "Property %s should exist in extracted resource", key)
			if exists {
				assert.Equal(t, expectedValue, actualValue, "Property %s should match in extracted resource", key)
			}
		}
		t.Log("Extract validation completed!")
	}

	// Step 7: Force synchronization to read actual state from cloud
	t.Log("Step 7: Forcing synchronization...")
	err = harness.Sync()
	require.NoError(t, err, "Sync command should succeed")

	// Step 8: Wait for synchronization to complete
	t.Log("Step 8: Waiting for synchronization to complete...")
	err = harness.WaitForSyncCompletion(60 * time.Second)
	require.NoError(t, err, "Synchronization should complete successfully")

	// Step 9: Verify inventory still matches expected state (idempotency check)
	t.Log("Step 9: Verifying resource state unchanged after sync (idempotency)...")
	inventoryAfterSync, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	require.NoError(t, err, "Inventory command should succeed after sync")
	assert.Len(t, inventoryAfterSync.Resources, 1, "Inventory should still contain exactly 1 resource")

	resourceAfterSync := inventoryAfterSync.Resources[0]

	// Compare properties using helper - verifies idempotency
	compareProperties(t, expectedProperties, resourceAfterSync, "after sync")
	t.Log("Idempotency verified!")

	// Step 10: Update test (if update file exists)
	if tc.UpdateFile != "" {
		t.Log("Step 10: Running update test using update file...")

		// Store current NativeID to verify it doesn't change during update
		inventoryBeforeUpdate, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		require.NoError(t, err, "Inventory command should succeed before update")
		require.Len(t, inventoryBeforeUpdate.Resources, 1, "Inventory should contain exactly 1 resource before update")
		oldNativeID := inventoryBeforeUpdate.Resources[0]["NativeID"].(string)
		t.Logf("Current NativeID before update: %s", oldNativeID)

		// Eval update file for expected state
		updateExpected, err := harness.Eval(tc.UpdateFile)
		require.NoError(t, err, "Update file eval should succeed")

		// Parse to get expected properties
		var updateEvalResult framework.InventoryResponse
		err = json.Unmarshal([]byte(updateExpected), &updateEvalResult)
		require.NoError(t, err, "Should be able to parse update eval output")
		require.NotEmpty(t, updateEvalResult.Resources, "Update eval should return at least one resource")

		// Find the target resource
		updateExpectedResource := findTargetResource(updateEvalResult.Resources, tc.ResourceType)

		updateExpectedProperties := updateExpectedResource["Properties"].(map[string]any)
		t.Logf("Expected properties after update: %+v", updateExpectedProperties)

		// Apply with patch mode
		updateCmdID, err := harness.ApplyWithMode(tc.UpdateFile, "patch")
		require.NoError(t, err, "Update apply should succeed")
		assert.NotEmpty(t, updateCmdID, "Update apply should return a command ID")

		// Poll for completion
		t.Log("Step 11: Polling for update command completion...")
		updateStatus, err := harness.PollStatus(updateCmdID, 5*time.Minute)
		require.NoError(t, err, "Update command should complete successfully")
		assert.Equal(t, "Success", updateStatus, "Update command should reach Success state")

		// Verify inventory matches updated state
		t.Log("Step 12: Verifying resource properties after update...")
		inventoryAfterUpdate, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		require.NoError(t, err, "Inventory command should succeed after update")
		assert.Len(t, inventoryAfterUpdate.Resources, 1, "Inventory should still contain exactly 1 resource")

		resourceAfterUpdate := inventoryAfterUpdate.Resources[0]

		// Verify NativeID did NOT change (proves resource was updated, not replaced)
		newNativeID := resourceAfterUpdate["NativeID"].(string)
		t.Logf("NativeID after update: %s", newNativeID)
		assert.Equal(t, oldNativeID, newNativeID,
			"NativeID should NOT change during update (old: %s, new: %s) - if it changed, a createOnly property was modified",
			oldNativeID, newNativeID)

		compareProperties(t, updateExpectedProperties, resourceAfterUpdate, "after update")
		t.Log("Update test completed successfully!")
	} else {
		t.Log("No update file found, skipping update test")
	}

	// Step 13: Replace test (if replace file exists)
	if tc.ReplaceFile != "" {
		t.Log("Step 13: Running replace test using replace file...")

		// Store current NativeID for comparison
		inventoryBeforeReplace, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		require.NoError(t, err, "Inventory command should succeed before replace")
		require.Len(t, inventoryBeforeReplace.Resources, 1, "Inventory should contain exactly 1 resource before replace")
		oldNativeID := inventoryBeforeReplace.Resources[0]["NativeID"].(string)
		t.Logf("Current NativeID before replace: %s", oldNativeID)

		// Eval replace file
		replaceExpected, err := harness.Eval(tc.ReplaceFile)
		require.NoError(t, err, "Replace file eval should succeed")

		// Parse to get expected properties
		var replaceEvalResult framework.InventoryResponse
		err = json.Unmarshal([]byte(replaceExpected), &replaceEvalResult)
		require.NoError(t, err, "Should be able to parse replace eval output")
		require.NotEmpty(t, replaceEvalResult.Resources, "Replace eval should return at least one resource")

		// Find the target resource
		replaceExpectedResource := findTargetResource(replaceEvalResult.Resources, tc.ResourceType)

		replaceExpectedProperties := replaceExpectedResource["Properties"].(map[string]any)
		t.Logf("Expected properties after replace: %+v", replaceExpectedProperties)

		// Apply with patch mode (createOnly change triggers delete+create)
		replaceCmdID, err := harness.ApplyWithMode(tc.ReplaceFile, "patch")
		require.NoError(t, err, "Replace apply should succeed")
		assert.NotEmpty(t, replaceCmdID, "Replace apply should return a command ID")

		// Poll for completion
		t.Log("Step 14: Polling for replace command completion...")
		replaceStatus, err := harness.PollStatus(replaceCmdID, 5*time.Minute)
		require.NoError(t, err, "Replace command should complete successfully")
		assert.Equal(t, "Success", replaceStatus, "Replace command should reach Success state")

		// Verify NativeID changed (proves resource was replaced)
		t.Log("Step 15: Verifying resource was replaced (NativeID changed)...")
		inventoryAfterReplace, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		require.NoError(t, err, "Inventory command should succeed after replace")
		require.Len(t, inventoryAfterReplace.Resources, 1, "Inventory should still contain exactly 1 resource after replace")

		newNativeID := inventoryAfterReplace.Resources[0]["NativeID"].(string)
		t.Logf("New NativeID after replace: %s", newNativeID)

		assert.NotEqual(t, oldNativeID, newNativeID,
			"NativeID should change after replace (old: %s, new: %s)", oldNativeID, newNativeID)

		// Verify properties match expected state
		resourceAfterReplace := inventoryAfterReplace.Resources[0]
		compareProperties(t, replaceExpectedProperties, resourceAfterReplace, "after replace")

		t.Log("Replace test completed successfully - resource was recreated with new NativeID!")
	} else {
		t.Log("No replace file found, skipping replace test")
	}

	// Step 16: Destroy the resource
	t.Log("Step 16: Destroying resource...")
	destroyCommandID, err := harness.Destroy(tc.PKLFile)
	require.NoError(t, err, "Destroy command should succeed")
	assert.NotEmpty(t, destroyCommandID, "Destroy should return a command ID")

	// Step 17: Poll for destroy command to complete successfully
	t.Log("Step 17: Polling for destroy command completion...")
	destroyStatus, err := harness.PollStatus(destroyCommandID, 5*time.Minute)
	require.NoError(t, err, "Destroy command should complete successfully")
	assert.Equal(t, "Success", destroyStatus, "Destroy command should reach Success state")

	// Step 18: Verify resource no longer exists in inventory
	t.Log("Step 18: Verifying resource removed from inventory...")
	inventoryAfterDestroy, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	require.NoError(t, err, "Inventory command should succeed after destroy")
	assert.Len(t, inventoryAfterDestroy.Resources, 0, "Inventory should be empty after destroy")

	t.Logf("Resource lifecycle test completed successfully for %s!", tc.Name)
}

// TestPluginSDK_Discovery tests resource discovery for unmanaged resources
func TestPluginSDK_Discovery(t *testing.T) {
	pluginPath := getPluginPath(t)

	// Discover all test cases in the plugin's testdata directory
	testCases, err := framework.DiscoverTestData(pluginPath)
	require.NoError(t, err, "Failed to discover test data")

	t.Logf("Discovered %d test case(s) for discovery testing", len(testCases))

	// Run each test case as a subtest
	for _, tc := range testCases {
		tc := tc // Capture range variable
		t.Run(tc.Name, func(t *testing.T) {
			runDiscoveryTest(t, tc)
		})
	}
}

// runDiscoveryTest runs the discovery test for a single test case
func runDiscoveryTest(t *testing.T, tc framework.TestCase) {
	// Create test harness
	harness := framework.NewTestHarness(t)
	defer harness.Cleanup()

	// Step 1: Evaluate PKL file to get expected state
	t.Log("Step 1: Evaluating PKL file to get expected state...")
	evalOutput, err := harness.Eval(tc.PKLFile)
	require.NoError(t, err, "Eval command should succeed")

	// Parse eval output
	var evalData map[string]any
	err = json.Unmarshal([]byte(evalOutput), &evalData)
	require.NoError(t, err, "Eval output should be valid JSON")

	resources, ok := evalData["Resources"].([]any)
	require.True(t, ok, "Eval output should contain Resources array")
	require.NotEmpty(t, resources, "Eval output should contain at least one resource")

	// Extract all unique resource types from forma for discovery configuration
	// This is needed for child resources that have parent dependencies in the hierarchy
	var allResourceTypes []string
	seenTypes := make(map[string]bool)
	for _, res := range resources {
		resMap := res.(map[string]any)
		if resType, ok := resMap["Type"].(string); ok {
			if !seenTypes[resType] {
				seenTypes[resType] = true
				allResourceTypes = append(allResourceTypes, resType)
			}
		}
	}

	// Get the resource we're testing (last one, which is the main resource after Stack and Target)
	resourceData := resources[len(resources)-1].(map[string]any)
	actualResourceType := resourceData["Type"].(string)
	expectedProperties := resourceData["Properties"].(map[string]any)

	t.Logf("Testing resource type: %s (with %d total types for discovery)", actualResourceType, len(allResourceTypes))

	// Step 2: Configure discovery for all resource types in the forma
	// Note: ConfigureDiscovery must be called before StartAgent()
	// We include all types so parent-child relationships can be established in the hierarchy
	t.Log("Step 2: Configuring discovery for resource types...")
	err = harness.ConfigureDiscovery(allResourceTypes)
	require.NoError(t, err, "Discovery configuration should succeed")

	// Step 3: Create unmanaged resources via external plugin (bypasses formae database)
	// This encapsulates: init ergo node, launch plugin, wait for ready, create resource(s)
	// For multi-resource formas, creates all resources in dependency order
	t.Log("Step 3: Creating unmanaged resource(s) via external plugin...")
	createdResources, err := harness.CreateAllUnmanagedResources(evalOutput)
	require.NoError(t, err, "Creating unmanaged resources should succeed")
	require.NotEmpty(t, createdResources, "Should have created at least one resource")

	// The main resource is the last one (after dependencies)
	mainResource := createdResources[len(createdResources)-1]
	nativeID := mainResource.NativeID
	t.Logf("Created %d unmanaged resource(s), main resource NativeID: %s", len(createdResources), nativeID)

	// Parse target from eval output for cleanup
	targets, ok := evalData["Targets"].([]any)
	require.True(t, ok, "Eval output should contain Targets array")
	require.NotEmpty(t, targets, "Eval output should contain at least one target")
	targetData := targets[0].(map[string]any)

	// Convert target to the model type needed by DeleteUnmanagedResource
	targetJSON, err := json.Marshal(targetData)
	require.NoError(t, err, "Failed to marshal target")
	var target pkgmodel.Target
	err = json.Unmarshal(targetJSON, &target)
	require.NoError(t, err, "Failed to unmarshal target")

	// Register cleanup for ALL created resources (in reverse order - dependents before dependencies)
	harness.RegisterCleanup(func() {
		t.Logf("Cleaning up %d unmanaged resource(s)...", len(createdResources))
		// Delete in reverse order (children before parents)
		for i := len(createdResources) - 1; i >= 0; i-- {
			res := createdResources[i]
			t.Logf("Cleaning up unmanaged resource: type=%s, label=%s, nativeID=%s", res.ResourceType, res.Label, res.NativeID)
			err := harness.DeleteUnmanagedResource(res.ResourceType, res.NativeID, &target)
			if err != nil {
				t.Logf("Warning: failed to delete unmanaged resource %s: %v", res.Label, err)
			}
		}
	})

	// Check if resource is discoverable before continuing (requires plugin to be announced)
	descriptor, err := harness.GetResourceDescriptorFromCoordinator(actualResourceType)
	if err != nil {
		t.Skipf("Skipping discovery test: failed to get resource descriptor for %s: %v", actualResourceType, err)
	}
	if !descriptor.Discoverable {
		t.Skipf("Skipping discovery test: resource type %s has discoverable=false", actualResourceType)
	}

	// Step 4: Stop test harness Ergo node before starting agent
	// This releases the plugin node name so the agent can start its own plugin
	t.Log("Step 4: Stopping test harness Ergo node...")
	harness.StopErgoNode()

	// Step 5: Start agent with discovery configured
	t.Log("Step 5: Starting formae agent with discovery enabled...")
	err = harness.StartAgent()
	require.NoError(t, err, "Failed to start formae agent")

	// Step 6: Register target for discovery (must be done after agent starts)
	t.Log("Step 6: Registering target for discovery...")
	err = harness.RegisterTargetForDiscovery(json.RawMessage(evalOutput))
	require.NoError(t, err, "Failed to register target for discovery")

	// Step 7: Wait for plugin to be registered before triggering discovery
	// Extract namespace from resource type (e.g., "AWS::S3::Bucket" -> "AWS")
	t.Log("Step 7: Waiting for plugin to be registered...")
	namespace := strings.Split(actualResourceType, "::")[0]
	err = harness.WaitForPluginRegistered(namespace, 30*time.Second)
	require.NoError(t, err, "Plugin should register within timeout")

	// Step 8: Trigger discovery
	t.Log("Step 8: Triggering discovery...")
	err = harness.TriggerDiscovery()
	require.NoError(t, err, "Triggering discovery should succeed")

	// Step 9: Wait for resource to appear in inventory
	// Uses inventory polling instead of log file polling for reliability in CI
	// Timeout of 2 minutes should be sufficient for discovery to complete
	t.Log("Step 9: Waiting for resource to appear in inventory...")
	err = harness.WaitForResourceInInventory(actualResourceType, nativeID, false, 2*time.Minute)
	require.NoError(t, err, "Resource should appear in inventory within timeout")

	// Step 10: Query inventory for unmanaged resources (get full response for assertions)
	t.Log("Step 10: Querying inventory for unmanaged resources...")
	inventory, err := harness.Inventory(fmt.Sprintf("type: %s managed: false", actualResourceType))
	require.NoError(t, err, "Inventory query should succeed")

	// Step 11: Verify our specific resource was discovered
	t.Log("Step 11: Verifying discovered resource...")
	assert.NotEmpty(t, inventory.Resources, "Should discover at least one unmanaged resource")

	if len(inventory.Resources) == 0 {
		t.Fatal("No unmanaged resources discovered")
	}

	// Find our specific resource by NativeID in the discovered resources
	var discoveredResource map[string]any
	for _, res := range inventory.Resources {
		if resNativeID, ok := res["NativeID"].(string); ok {
			if resNativeID == nativeID {
				discoveredResource = res
				break
			}
		}
	}

	if discoveredResource == nil {
		t.Fatalf("Did not find our resource with NativeID %s in discovered resources", nativeID)
	}

	// Step 12: Verify the discovered resource properties match what we created
	t.Log("Step 12: Verifying discovered resource properties...")

	// Verify resource is on the unmanaged stack
	stack, ok := discoveredResource["Stack"].(string)
	require.True(t, ok, "Discovered resource should have Stack field")
	assert.Equal(t, "$unmanaged", stack, "Discovered resource should be on $unmanaged stack")

	// Verify resource type matches
	discoveredType, ok := discoveredResource["Type"].(string)
	require.True(t, ok, "Discovered resource should have Type field")
	assert.Equal(t, actualResourceType, discoveredType, "Discovered resource type should match")

	// Compare properties (excluding metadata tags since we stripped them)
	discoveredProps, ok := discoveredResource["Properties"].(map[string]any)
	require.True(t, ok, "Discovered resource should have Properties")

	// For properties comparison, we expect the properties we created (without formae tags)
	// but discovery might add some discovered metadata, so we only verify key properties
	for key, expectedValue := range expectedProperties {
		// Skip Tags comparison as we intentionally stripped formae tags
		if key == "Tags" {
			continue
		}

		// Skip resolvable properties - when discovered, these get reconstructed to point
		// to discovered resources (in $unmanaged stack with native IDs as labels),
		// which won't match the original forma's resolvables
		if isResolvable(expectedValue) {
			t.Logf("Skipping resolvable property %s in discovery comparison", key)
			continue
		}

		actualValue, exists := discoveredProps[key]
		assert.True(t, exists, "Property %s should exist in discovered resource", key)
		assert.Equal(t, expectedValue, actualValue, "Property %s should match", key)
	}

	t.Log("Discovery test completed successfully!")
	t.Log("Successfully discovered unmanaged resource and verified its properties")
}
