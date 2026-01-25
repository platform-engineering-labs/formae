// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// filterTestCases filters test cases based on the FORMAE_TEST_FILTER environment variable.
// The filter matches against test case names (e.g., "AWS::s3-bucket") or file paths.
// Multiple filters can be comma-separated (e.g., "s3-bucket,ec2-instance").
// If no filter is set, all test cases are returned.
func filterTestCases(t *testing.T, testCases []TestCase) []TestCase {
	filter := os.Getenv("FORMAE_TEST_FILTER")
	if filter == "" {
		return testCases
	}

	// Split filter into multiple patterns (comma-separated)
	patterns := strings.Split(filter, ",")
	for i := range patterns {
		patterns[i] = strings.TrimSpace(patterns[i])
	}

	var filtered []TestCase
	for _, tc := range testCases {
		for _, pattern := range patterns {
			if pattern == "" {
				continue
			}
			// Match against test name, resource type, or PKL file path
			if strings.Contains(strings.ToLower(tc.Name), strings.ToLower(pattern)) ||
				strings.Contains(strings.ToLower(tc.ResourceType), strings.ToLower(pattern)) ||
				strings.Contains(strings.ToLower(tc.PKLFile), strings.ToLower(pattern)) {
				filtered = append(filtered, tc)
				break // Don't add the same test case twice
			}
		}
	}

	if len(filtered) == 0 {
		t.Fatalf("FORMAE_TEST_FILTER=%q matched no test cases. Available: %v", filter, testCaseNames(testCases))
	}

	t.Logf("Filter %q matched %d of %d test case(s)", filter, len(filtered), len(testCases))
	return filtered
}

// testCaseNames returns a slice of test case names for error messages.
func testCaseNames(testCases []TestCase) []string {
	names := make([]string, len(testCases))
	for i, tc := range testCases {
		names[i] = tc.Name
	}
	return names
}

// RunCRUDTests discovers test cases from the testdata directory and runs
// the standard CRUD lifecycle test for each resource type.
//
// For each test case:
//   - Evaluates the PKL file to get expected state
//   - Waits for the plugin to register
//   - Creates the resource via formae apply
//   - Reads and verifies the resource via inventory
//   - Extracts and verifies the resource (if extractable)
//   - Forces synchronization to read actual state from cloud
//   - Verifies idempotency (resource state unchanged after sync)
//   - Updates the resource (if update file exists)
//   - Replaces the resource (if replace file exists)
//   - Deletes the resource via formae destroy
//
// Environment variables:
//   - FORMAE_BINARY: Path to the formae binary (required)
//   - FORMAE_TEST_FILTER: Filter test cases by name, resource type, or file path (optional).
//     Supports comma-separated patterns for multiple filters.
//     Examples: "s3-bucket", "ec2-instance,vpc", "testdata/network"
//
// This function should be called from a plugin's conformance_test.go:
//
//	func TestPluginConformance(t *testing.T) {
//	    conformance.RunCRUDTests(t)
//	}
func RunCRUDTests(t *testing.T) {
	// Skip if not running conformance tests
	if os.Getenv("FORMAE_BINARY") == "" {
		t.Skip("Skipping conformance test: FORMAE_BINARY not set. Run 'make conformance-test' instead.")
	}

	// Find plugin directory (where the test is running from)
	pluginDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Discover test cases from testdata directory
	testCases, err := DiscoverTestData(pluginDir)
	if err != nil {
		t.Fatalf("failed to discover test cases: %v", err)
	}

	// Apply filter if FORMAE_TEST_FILTER is set
	testCases = filterTestCases(t, testCases)

	t.Logf("Discovered %d test case(s)", len(testCases))

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			runCRUDTest(t, tc)
		})
	}
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

// compareTags compares two tag slices ignoring order
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

// compareProperties compares expected properties against actual properties from inventory
func compareProperties(t *testing.T, expectedProperties map[string]any, actualResource map[string]any, context string) {
	actualProperties, ok := actualResource["Properties"].(map[string]any)
	if !ok {
		t.Errorf("Could not extract Properties from inventory resource (%s)", context)
		return
	}

	t.Logf("Comparing expected properties with actual properties (%s)...", context)
	for key, expectedValue := range expectedProperties {
		actualValue, exists := actualProperties[key]
		if !exists {
			t.Errorf("Property %s should exist in actual resource (%s)", key, context)
			continue
		}

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
			if fmt.Sprintf("%v", expectedValue) != fmt.Sprintf("%v", actualValue) {
				t.Errorf("Property %s should match expected value (%s): expected %v, got %v",
					key, context, expectedValue, actualValue)
			}
		}
	}
	t.Logf("All expected properties matched (%s)!", context)
}

// runCRUDTest runs the full CRUD lifecycle for a single test case.
// This matches the structure of formae-internal's runLifecycleTest exactly.
func runCRUDTest(t *testing.T, tc TestCase) {
	t.Logf("Testing resource: %s (file: %s)", tc.Name, tc.PKLFile)

	// Create test harness
	harness := NewTestHarness(t)
	defer harness.Cleanup()

	// Start the formae agent
	if err := harness.StartAgent(); err != nil {
		t.Fatalf("failed to start agent: %v", err)
	}

	// === Step 1: Eval the PKL file to get expected state ===
	t.Log("Step 1: Evaluating PKL file to get expected state...")
	expectedOutput, err := harness.Eval(tc.PKLFile)
	if err != nil {
		t.Fatalf("Eval command failed: %v", err)
	}

	// Parse eval output to get expected properties
	var evalResult InventoryResponse
	if err := json.Unmarshal([]byte(expectedOutput), &evalResult); err != nil {
		t.Fatalf("Failed to parse eval output: %v", err)
	}
	if len(evalResult.Resources) == 0 {
		t.Fatal("Eval should return at least one resource")
	}

	// Find the target resource by matching the test case's ResourceType to the resource Type
	expectedResource := findTargetResource(evalResult.Resources, tc.ResourceType)

	actualResourceType, ok := expectedResource["Type"].(string)
	if !ok {
		t.Fatal("Expected resource should have Type field")
	}
	expectedProperties, ok := expectedResource["Properties"].(map[string]any)
	if !ok {
		t.Fatal("Expected resource should have Properties field")
	}
	t.Logf("Expected resource type: %s", actualResourceType)

	// === Step 2: Wait for plugin to be registered before any commands ===
	t.Log("Step 2: Waiting for plugin to be registered...")
	namespace := strings.Split(actualResourceType, "::")[0]
	if err := harness.WaitForPluginRegistered(namespace, 30*time.Second); err != nil {
		t.Fatalf("Plugin should register within timeout: %v", err)
	}

	// === Step 3: Apply the forma to create the resource ===
	t.Log("Step 3: Applying forma to create resource...")
	applyCommandID, err := harness.Apply(tc.PKLFile)
	if err != nil {
		t.Fatalf("Apply command failed: %v", err)
	}
	if applyCommandID == "" {
		t.Fatal("Apply should return a command ID")
	}

	// === Step 4: Poll for apply command to complete successfully ===
	t.Log("Step 4: Polling for apply command completion...")
	applyStatus, err := harness.PollStatus(applyCommandID, 5*time.Minute)
	if err != nil {
		t.Fatalf("Apply command should complete successfully: %v", err)
	}
	if applyStatus != "Success" {
		t.Fatalf("Apply command should reach Success state, got: %s", applyStatus)
	}

	// === Step 5: Verify resource exists in inventory with correct properties ===
	t.Log("Step 5: Verifying resource in inventory...")
	inventory, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	if err != nil {
		t.Fatalf("Inventory command failed: %v", err)
	}
	if inventory == nil {
		t.Fatal("Inventory should return a response")
	}
	if len(inventory.Resources) != 1 {
		t.Fatalf("Inventory should contain exactly 1 resource, got %d", len(inventory.Resources))
	}

	actualResource := inventory.Resources[0]
	t.Logf("Found resource in inventory with type: %s", actualResource["Type"])

	// Verify the resource type matches
	if actualResource["Type"] != actualResourceType {
		t.Errorf("Resource type should match: expected %s, got %s", actualResourceType, actualResource["Type"])
	}

	// Compare properties using helper
	compareProperties(t, expectedProperties, actualResource, "after create")

	// === Step 6: Extract resource and verify it matches original (if extractable) ===
	shouldSkipExtract := false
	descriptor, err := harness.GetResourceDescriptor(actualResourceType)
	if err != nil {
		t.Logf("Skipping extract validation: failed to get resource descriptor for %s: %v", actualResourceType, err)
		shouldSkipExtract = true
	} else if !descriptor.Extractable {
		t.Logf("Skipping extract validation: resource type %s has extractable=false", actualResourceType)
		shouldSkipExtract = true
	}

	if !shouldSkipExtract {
		t.Log("Step 6: Extracting resource to PKL and verifying...")

		// Create temp directory for extracted files
		extractDir, err := os.MkdirTemp("", "formae-extract-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp directory for extraction: %v", err)
		}
		defer os.RemoveAll(extractDir)
		t.Logf("Created extract directory: %s", extractDir)

		// Extract the resource
		extractFile := filepath.Join(extractDir, "extracted.pkl")
		if err := harness.Extract(fmt.Sprintf("type:%s", actualResourceType), extractFile); err != nil {
			t.Fatalf("Extract command failed: %v", err)
		}

		// Eval the extracted PKL file
		extractedOutput, err := harness.Eval(extractFile)
		if err != nil {
			t.Fatalf("Eval of extracted file failed: %v", err)
		}

		// Parse extracted eval output
		var extractedResult InventoryResponse
		if err := json.Unmarshal([]byte(extractedOutput), &extractedResult); err != nil {
			t.Fatalf("Failed to parse extracted eval output: %v", err)
		}
		if len(extractedResult.Resources) == 0 {
			t.Fatal("Extracted eval should return at least one resource")
		}

		// Get the extracted resource properties
		extractedResource := extractedResult.Resources[0]
		extractedProperties, ok := extractedResource["Properties"].(map[string]any)
		if !ok {
			t.Fatal("Extracted resource should have Properties field")
		}

		// Compare properties from extracted file with original
		t.Log("Comparing extracted properties with original expected properties...")
		for key, expectedValue := range expectedProperties {
			actualValue, exists := extractedProperties[key]
			if !exists {
				t.Errorf("Property %s should exist in extracted resource", key)
				continue
			}
			if fmt.Sprintf("%v", expectedValue) != fmt.Sprintf("%v", actualValue) {
				t.Errorf("Property %s should match in extracted resource: expected %v, got %v",
					key, expectedValue, actualValue)
			}
		}
		t.Log("Extract validation completed!")
	} else {
		t.Log("Step 6: Skipping extract validation")
	}

	// === Step 7: Force synchronization to read actual state from cloud ===
	t.Log("Step 7: Forcing synchronization...")
	if err := harness.Sync(); err != nil {
		t.Fatalf("Sync command failed: %v", err)
	}

	// === Step 8: Wait for synchronization to complete ===
	t.Log("Step 8: Waiting for synchronization to complete...")
	if err := harness.WaitForSyncCompletion(60 * time.Second); err != nil {
		t.Fatalf("Synchronization should complete successfully: %v", err)
	}

	// === Step 9: Verify inventory still matches expected state (idempotency check) ===
	t.Log("Step 9: Verifying resource state unchanged after sync (idempotency)...")
	inventoryAfterSync, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	if err != nil {
		t.Fatalf("Inventory command failed after sync: %v", err)
	}
	if len(inventoryAfterSync.Resources) != 1 {
		t.Fatalf("Inventory should still contain exactly 1 resource after sync, got %d", len(inventoryAfterSync.Resources))
	}

	resourceAfterSync := inventoryAfterSync.Resources[0]

	// Compare properties using helper - verifies idempotency
	compareProperties(t, expectedProperties, resourceAfterSync, "after sync")
	t.Log("Idempotency verified!")

	// === Step 10: Update test (if update file exists) ===
	if tc.UpdateFile != "" {
		t.Log("Step 10: Running update test using update file...")

		// Store current NativeID to verify it doesn't change during update
		inventoryBeforeUpdate, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		if err != nil {
			t.Fatalf("Inventory command failed before update: %v", err)
		}
		if len(inventoryBeforeUpdate.Resources) != 1 {
			t.Fatal("Inventory should contain exactly 1 resource before update")
		}
		oldNativeID, ok := inventoryBeforeUpdate.Resources[0]["NativeID"].(string)
		if !ok {
			t.Fatal("Resource should have NativeID field")
		}
		t.Logf("Current NativeID before update: %s", oldNativeID)

		// Eval update file for expected state
		updateExpected, err := harness.Eval(tc.UpdateFile)
		if err != nil {
			t.Fatalf("Update file eval failed: %v", err)
		}

		// Parse to get expected properties
		var updateEvalResult InventoryResponse
		if err := json.Unmarshal([]byte(updateExpected), &updateEvalResult); err != nil {
			t.Fatalf("Failed to parse update eval output: %v", err)
		}
		if len(updateEvalResult.Resources) == 0 {
			t.Fatal("Update eval should return at least one resource")
		}

		// Find the target resource
		updateExpectedResource := findTargetResource(updateEvalResult.Resources, tc.ResourceType)
		updateExpectedProperties, ok := updateExpectedResource["Properties"].(map[string]any)
		if !ok {
			t.Fatal("Update expected resource should have Properties field")
		}

		// Apply with patch mode
		updateCmdID, err := harness.ApplyWithMode(tc.UpdateFile, "patch")
		if err != nil {
			t.Fatalf("Update apply failed: %v", err)
		}
		if updateCmdID == "" {
			t.Fatal("Update apply should return a command ID")
		}

		// === Step 11: Poll for update command completion ===
		t.Log("Step 11: Polling for update command completion...")
		updateStatus, err := harness.PollStatus(updateCmdID, 5*time.Minute)
		if err != nil {
			t.Fatalf("Update command should complete successfully: %v", err)
		}
		if updateStatus != "Success" {
			t.Fatalf("Update command should reach Success state, got: %s", updateStatus)
		}

		// === Step 12: Verify resource properties after update ===
		t.Log("Step 12: Verifying resource properties after update...")
		inventoryAfterUpdate, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		if err != nil {
			t.Fatalf("Inventory command failed after update: %v", err)
		}
		if len(inventoryAfterUpdate.Resources) != 1 {
			t.Fatal("Inventory should still contain exactly 1 resource after update")
		}

		resourceAfterUpdate := inventoryAfterUpdate.Resources[0]

		// Verify NativeID did NOT change (proves resource was updated, not replaced)
		newNativeID, ok := resourceAfterUpdate["NativeID"].(string)
		if !ok {
			t.Fatal("Resource should have NativeID field after update")
		}
		t.Logf("NativeID after update: %s", newNativeID)
		if oldNativeID != newNativeID {
			t.Errorf("NativeID should NOT change during update (old: %s, new: %s) - if it changed, a createOnly property was modified",
				oldNativeID, newNativeID)
		}

		compareProperties(t, updateExpectedProperties, resourceAfterUpdate, "after update")
		t.Log("Update test completed successfully!")
	} else {
		t.Log("No update file found, skipping update test")
	}

	// === Step 13: Replace test (if replace file exists) ===
	if tc.ReplaceFile != "" {
		t.Log("Step 13: Running replace test using replace file...")

		// Store current NativeID for comparison
		inventoryBeforeReplace, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		if err != nil {
			t.Fatalf("Inventory command failed before replace: %v", err)
		}
		if len(inventoryBeforeReplace.Resources) != 1 {
			t.Fatal("Inventory should contain exactly 1 resource before replace")
		}
		oldNativeID, ok := inventoryBeforeReplace.Resources[0]["NativeID"].(string)
		if !ok {
			t.Fatal("Resource should have NativeID field")
		}
		t.Logf("Current NativeID before replace: %s", oldNativeID)

		// Eval replace file
		replaceExpected, err := harness.Eval(tc.ReplaceFile)
		if err != nil {
			t.Fatalf("Replace file eval failed: %v", err)
		}

		// Parse to get expected properties
		var replaceEvalResult InventoryResponse
		if err := json.Unmarshal([]byte(replaceExpected), &replaceEvalResult); err != nil {
			t.Fatalf("Failed to parse replace eval output: %v", err)
		}
		if len(replaceEvalResult.Resources) == 0 {
			t.Fatal("Replace eval should return at least one resource")
		}

		// Find the target resource
		replaceExpectedResource := findTargetResource(replaceEvalResult.Resources, tc.ResourceType)
		replaceExpectedProperties, ok := replaceExpectedResource["Properties"].(map[string]any)
		if !ok {
			t.Fatal("Replace expected resource should have Properties field")
		}

		// Apply with patch mode (createOnly change triggers delete+create)
		replaceCmdID, err := harness.ApplyWithMode(tc.ReplaceFile, "patch")
		if err != nil {
			t.Fatalf("Replace apply failed: %v", err)
		}
		if replaceCmdID == "" {
			t.Fatal("Replace apply should return a command ID")
		}

		// === Step 14: Poll for replace command completion ===
		t.Log("Step 14: Polling for replace command completion...")
		replaceStatus, err := harness.PollStatus(replaceCmdID, 5*time.Minute)
		if err != nil {
			t.Fatalf("Replace command should complete successfully: %v", err)
		}
		if replaceStatus != "Success" {
			t.Fatalf("Replace command should reach Success state, got: %s", replaceStatus)
		}

		// === Step 15: Verify resource was replaced (NativeID changed) ===
		t.Log("Step 15: Verifying resource was replaced (NativeID changed)...")
		inventoryAfterReplace, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		if err != nil {
			t.Fatalf("Inventory command failed after replace: %v", err)
		}
		if len(inventoryAfterReplace.Resources) != 1 {
			t.Fatal("Inventory should still contain exactly 1 resource after replace")
		}

		newNativeID, ok := inventoryAfterReplace.Resources[0]["NativeID"].(string)
		if !ok {
			t.Fatal("Resource should have NativeID field after replace")
		}
		t.Logf("New NativeID after replace: %s", newNativeID)

		if oldNativeID == newNativeID {
			t.Errorf("NativeID should change after replace (old: %s, new: %s)", oldNativeID, newNativeID)
		}

		// Verify properties match expected state
		resourceAfterReplace := inventoryAfterReplace.Resources[0]
		compareProperties(t, replaceExpectedProperties, resourceAfterReplace, "after replace")

		t.Log("Replace test completed successfully - resource was recreated with new NativeID!")
	} else {
		t.Log("No replace file found, skipping replace test")
	}

	// === Step 16: Destroy the resource ===
	t.Log("Step 16: Destroying resource...")
	destroyCommandID, err := harness.Destroy(tc.PKLFile)
	if err != nil {
		t.Fatalf("Destroy command failed: %v", err)
	}
	if destroyCommandID == "" {
		t.Fatal("Destroy should return a command ID")
	}

	// === Step 17: Poll for destroy command to complete successfully ===
	t.Log("Step 17: Polling for destroy command completion...")
	destroyStatus, err := harness.PollStatus(destroyCommandID, 5*time.Minute)
	if err != nil {
		t.Fatalf("Destroy command should complete successfully: %v", err)
	}
	if destroyStatus != "Success" {
		t.Fatalf("Destroy command should reach Success state, got: %s", destroyStatus)
	}

	// === Step 18: Verify resource no longer exists in inventory ===
	t.Log("Step 18: Verifying resource removed from inventory...")
	inventoryAfterDestroy, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	if err != nil {
		t.Fatalf("Inventory command failed after destroy: %v", err)
	}
	if len(inventoryAfterDestroy.Resources) != 0 {
		t.Errorf("Inventory should be empty after destroy, got %d resources", len(inventoryAfterDestroy.Resources))
	}

	t.Logf("Resource lifecycle test completed successfully for %s!", tc.Name)
}

// RunDiscoveryTests tests that the plugin can discover resources created out-of-band.
//
// This test:
//   - Creates a resource directly via the plugin (bypassing formae)
//   - Runs formae discovery
//   - Verifies the resource appears as unmanaged
//   - Cleans up the resource
//
// Environment variables:
//   - FORMAE_BINARY: Path to the formae binary (required)
//   - FORMAE_TEST_FILTER: Filter test cases by name, resource type, or file path (optional).
//     Supports comma-separated patterns for multiple filters.
//     Examples: "s3-bucket", "ec2-instance,vpc", "testdata/network"
//
// This function should be called from a plugin's conformance_test.go:
//
//	func TestPluginDiscovery(t *testing.T) {
//	    conformance.RunDiscoveryTests(t)
//	}
func RunDiscoveryTests(t *testing.T) {
	// Skip if not running conformance tests
	if os.Getenv("FORMAE_BINARY") == "" {
		t.Skip("Skipping conformance test: FORMAE_BINARY not set. Run 'make conformance-test' instead.")
	}

	// Find plugin directory
	pluginDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Discover test cases
	testCases, err := DiscoverTestData(pluginDir)
	if err != nil {
		t.Fatalf("failed to discover test cases: %v", err)
	}

	if len(testCases) == 0 {
		t.Skip("No test cases found")
	}

	// Apply filter if FORMAE_TEST_FILTER is set
	testCases = filterTestCases(t, testCases)

	t.Logf("Discovered %d test case(s) for discovery testing", len(testCases))

	// Run discovery test for each test case
	for _, tc := range testCases {
		t.Run("Discovery/"+tc.Name, func(t *testing.T) {
			runDiscoveryTest(t, tc)
		})
	}
}

// runDiscoveryTest runs the discovery test for a single test case.
func runDiscoveryTest(t *testing.T, tc TestCase) {
	// Create test harness
	harness := NewTestHarness(t)
	defer harness.Cleanup()

	// Get resource descriptor to find the resource type for discovery config
	// tc.PKLFile is already the absolute path from DiscoverTestData
	pklFile := tc.PKLFile

	// Evaluate to get resource type
	evalOutput, err := harness.Eval(pklFile)
	if err != nil {
		t.Fatalf("failed to eval PKL file: %v", err)
	}

	// Extract the plugin namespace and resource type from the evaluated forma resources
	namespace, resourceType, err := extractNamespaceFromEvalOutput(evalOutput)
	if err != nil {
		t.Fatalf("failed to extract namespace from eval output: %v", err)
	}
	t.Logf("Extracted plugin namespace: %s, resource type: %s", namespace, resourceType)

	t.Log("Step 1: Creating resource out-of-band via plugin...")

	// Create all resources directly via the plugin (bypassing formae)
	// Use CreateAllUnmanagedResources to get all created resources for cleanup
	createdResources, err := harness.CreateAllUnmanagedResources(evalOutput)
	if err != nil {
		t.Fatalf("failed to create unmanaged resource: %v", err)
	}
	if len(createdResources) == 0 {
		t.Fatal("no resources were created")
	}

	// The main resource is the last one (after dependencies)
	nativeID := createdResources[len(createdResources)-1].NativeID
	t.Logf("Created %d resource(s), main resource NativeID: %s", len(createdResources), nativeID)

	// Parse target from eval output for cleanup
	var forma pkgmodel.Forma
	if err := json.Unmarshal([]byte(evalOutput), &forma); err != nil {
		t.Fatalf("failed to parse forma for cleanup: %v", err)
	}
	if len(forma.Targets) == 0 {
		t.Fatal("no targets found in forma for cleanup")
	}
	target := forma.Targets[0]

	// Register cleanup to delete all created resources (in reverse order - dependents before dependencies)
	harness.RegisterCleanup(func() {
		t.Logf("Cleaning up %d unmanaged resource(s)...", len(createdResources))
		for i := len(createdResources) - 1; i >= 0; i-- {
			res := createdResources[i]
			t.Logf("Deleting unmanaged resource: type=%s, label=%s, nativeID=%s", res.ResourceType, res.Label, res.NativeID)
			if err := harness.DeleteUnmanagedResource(res.ResourceType, res.NativeID, &target); err != nil {
				t.Logf("Warning: failed to delete unmanaged resource %s: %v", res.Label, err)
			}
		}
	})

	// Start the agent with discovery enabled
	if err := harness.StartAgent(); err != nil {
		t.Fatalf("failed to start agent: %v", err)
	}

	// Step 2: Register target for discovery
	t.Log("Step 2: Registering target for discovery...")
	if err := harness.RegisterTargetForDiscovery([]byte(evalOutput)); err != nil {
		t.Fatalf("failed to register target: %v", err)
	}

	// Step 3: Wait for plugin to register (using extracted namespace, not directory name)
	t.Log("Step 3: Waiting for plugin to register...")
	if err := harness.WaitForPluginRegistered(namespace, 30*time.Second); err != nil {
		t.Fatalf("plugin did not register: %v", err)
	}

	// Step 4: Check if resource is discoverable before continuing
	t.Log("Step 4: Checking if resource type is discoverable...")
	descriptor, err := harness.GetResourceDescriptorFromCoordinator(resourceType)
	if err != nil {
		t.Skipf("Skipping discovery test: failed to get resource descriptor for %s: %v", resourceType, err)
	}
	if !descriptor.Discoverable {
		t.Skipf("Skipping discovery test: resource type %s has discoverable=false", resourceType)
	}

	// Step 5: Trigger discovery
	t.Log("Step 5: Triggering discovery...")
	if err := harness.TriggerDiscovery(); err != nil {
		t.Fatalf("failed to trigger discovery: %v", err)
	}

	// Step 6: Wait for resource to appear in inventory
	t.Log("Step 6: Waiting for resource in inventory...")
	if err := harness.WaitForResourceInInventory(resourceType, nativeID, false, 2*time.Minute); err != nil {
		t.Fatalf("resource not discovered: %v", err)
	}

	// Step 7: Verify the discovered resource
	t.Log("Step 7: Verifying discovered resource...")
	inventory, err := harness.Inventory(fmt.Sprintf("type: %s managed: false", resourceType))
	if err != nil {
		t.Fatalf("failed to query inventory: %v", err)
	}

	// Find our specific resource by NativeID
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
		t.Fatalf("did not find resource with NativeID %s in discovered resources", nativeID)
	}

	// Verify resource is on the unmanaged stack
	stack, ok := discoveredResource["Stack"].(string)
	if !ok || stack != "$unmanaged" {
		t.Fatalf("discovered resource should be on $unmanaged stack, got: %s", stack)
	}

	// Verify resource type matches
	discoveredType, ok := discoveredResource["Type"].(string)
	if !ok || discoveredType != resourceType {
		t.Fatalf("discovered resource type should be %s, got: %s", resourceType, discoveredType)
	}

	t.Log("Discovery test passed!")
}

// extractNamespaceFromEvalOutput parses the evaluated JSON to extract the plugin namespace
// and resource type from the last cloud resource (e.g., "OVH::Network::FloatingIP" -> namespace="OVH", resourceType="OVH::Network::FloatingIP").
// The last resource is used because it's typically the main resource being tested,
// while earlier resources are dependencies.
func extractNamespaceFromEvalOutput(evalOutput string) (namespace string, resourceType string, err error) {
	var forma pkgmodel.Forma
	if err := json.Unmarshal([]byte(evalOutput), &forma); err != nil {
		return "", "", err
	}

	// Find the last cloud resource (those with "::" in the type)
	for i := len(forma.Resources) - 1; i >= 0; i-- {
		res := forma.Resources[i]
		if strings.Contains(res.Type, "::") {
			parts := strings.Split(res.Type, "::")
			if len(parts) > 0 {
				return parts[0], res.Type, nil
			}
		}
	}

	return "", "", fmt.Errorf("no cloud resource found in eval output")
}
