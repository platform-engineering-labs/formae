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
	"regexp"
	"sort"
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

// findMatchingBrace finds the position of the closing brace matching the opening brace at startPos
func findMatchingBrace(content string, startPos int) int {
	depth := 1
	for i := startPos + 1; i < len(content); i++ {
		if content[i] == '{' {
			depth++
		} else if content[i] == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// createPatchFileWithTag creates a patch PKL file by adding a new tag to the resource
// This function is generic and works with any PKL file structure
func createPatchFileWithTag(t *testing.T, originalPKLFile, patchFilePath string) error {
	// Read the original PKL file
	content, err := os.ReadFile(originalPKLFile)
	if err != nil {
		return fmt.Errorf("failed to read original PKL file: %w", err)
	}

	pklContent := string(content)

	// Find all "new <Type>.<Type> {" patterns
	pattern := regexp.MustCompile(`new\s+(\w+)\.(\w+)\s*\{`)
	matches := pattern.FindAllStringIndex(pklContent, -1)

	if len(matches) == 0 {
		return fmt.Errorf("could not find resource definition in PKL file")
	}

	// Use the last match (the main resource, after Stack and Target)
	lastMatchEnd := matches[len(matches)-1][1]

	// Find the opening brace position (it's at lastMatchEnd - 1)
	openBracePos := lastMatchEnd - 1

	// Find the matching closing brace
	closeBracePos := findMatchingBrace(pklContent, openBracePos)
	if closeBracePos == -1 {
		return fmt.Errorf("could not find matching closing brace for resource")
	}

	// Extract the resource body (between the braces)
	resourceBody := pklContent[openBracePos+1 : closeBracePos]

	// Check if tags property already exists
	tagsPattern := regexp.MustCompile(`tags\s*\{`)
	tagsMatch := tagsPattern.FindStringIndex(resourceBody)

	var modifiedContent string
	if tagsMatch != nil {
		// Tags exist - find the tags block and append to it
		tagsStartInResource := tagsMatch[1] - 1 // Position of '{' in tags block
		tagsCloseInResource := findMatchingBrace(resourceBody, tagsStartInResource)

		if tagsCloseInResource == -1 {
			return fmt.Errorf("could not find matching closing brace for tags block")
		}

		// Insert new tag before the closing brace of tags block
		newTag := `
    new {
      key = "Environment"
      value = "test"
    }
  `
		modifiedResourceBody := resourceBody[:tagsCloseInResource] + newTag + resourceBody[tagsCloseInResource:]
		modifiedContent = pklContent[:openBracePos+1] + modifiedResourceBody + pklContent[closeBracePos:]
	} else {
		// No tags - add new tags property before the closing brace
		newTagsBlock := `
  tags {
    new {
      key = "Environment"
      value = "test"
    }
  }
`
		modifiedContent = pklContent[:closeBracePos] + newTagsBlock + pklContent[closeBracePos:]
	}

	// Write the modified content to the patch file
	if err := os.WriteFile(patchFilePath, []byte(modifiedContent), 0644); err != nil {
		return fmt.Errorf("failed to write patch file: %w", err)
	}

	return nil
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

	// Step 8: Update the resource by adding a tag (using patch mode)
	t.Log("Step 8: Creating patch PKL file to add a tag...")

	// Create patch file in same directory as original PKL file so it can resolve dependencies
	pklDir := filepath.Dir(tc.PKLFile)
	patchFile := filepath.Join(pklDir, "resource-patch.pkl")

	// Use generic helper to create patch file by modifying the original
	err = createPatchFileWithTag(t, tc.PKLFile, patchFile)
	require.NoError(t, err, "Should be able to create patch PKL file")
	t.Logf("Created patch file at: %s", patchFile)

	// Register cleanup to remove the patch file after test
	harness.RegisterCleanup(func() {
		_ = os.Remove(patchFile)
	})

	// Step 9: Apply the patch to add the tag
	t.Log("Step 9: Applying patch to add tag...")
	patchCommandID, err := harness.ApplyWithMode(patchFile, "patch")
	require.NoError(t, err, "Patch apply command should succeed")
	assert.NotEmpty(t, patchCommandID, "Patch apply should return a command ID")

	// Step 10: Poll for patch command to complete successfully
	t.Log("Step 10: Polling for patch command completion...")
	patchStatus, err := harness.PollStatus(patchCommandID, 5*time.Minute)
	require.NoError(t, err, "Patch command should complete successfully")
	assert.Equal(t, "Success", patchStatus, "Patch command should reach Success state")

	// Step 11: Verify the tag was added to the resource
	t.Log("Step 11: Verifying tag was added to resource...")
	inventoryAfterPatch, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	require.NoError(t, err, "Inventory command should succeed after patch")
	assert.Len(t, inventoryAfterPatch.Resources, 1, "Inventory should still contain exactly 1 resource")

	resourceAfterPatch := inventoryAfterPatch.Resources[0]

	// Add the expected tag to our expected properties for comparison
	// Start with the original properties (which include the FormaeResourceLabel and FormaeStackLabel tags)
	expectedPropertiesWithTag := make(map[string]any)
	for k, v := range expectedProperties {
		expectedPropertiesWithTag[k] = v
	}

	// Append the new Environment tag to the existing tags list
	existingTags := expectedPropertiesWithTag["Tags"].([]any)
	expectedTags := append(existingTags, map[string]any{
		"Key":   "Environment",
		"Value": "test",
	})

	// Sort expected tags by Key to ensure consistent ordering (AWS may return tags in different order)
	sort.Slice(expectedTags, func(i, j int) bool {
		return expectedTags[i].(map[string]any)["Key"].(string) < expectedTags[j].(map[string]any)["Key"].(string)
	})
	expectedPropertiesWithTag["Tags"] = expectedTags

	// Sort actual tags from inventory as well
	if actualProperties, ok := resourceAfterPatch["Properties"].(map[string]any); ok {
		if actualTags, ok := actualProperties["Tags"].([]any); ok {
			sort.Slice(actualTags, func(i, j int) bool {
				return actualTags[i].(map[string]any)["Key"].(string) < actualTags[j].(map[string]any)["Key"].(string)
			})
		}
	}

	// Verify all properties including the new tag
	compareProperties(t, expectedPropertiesWithTag, resourceAfterPatch, "after patch")
	t.Log("Update test completed successfully - tag was added!")

	// Step 12: Destroy the resource
	t.Log("Step 12: Destroying resource...")
	destroyCommandID, err := harness.Destroy(tc.PKLFile)
	require.NoError(t, err, "Destroy command should succeed")
	assert.NotEmpty(t, destroyCommandID, "Destroy should return a command ID")

	// Step 13: Poll for destroy command to complete successfully
	t.Log("Step 13: Polling for destroy command completion...")
	destroyStatus, err := harness.PollStatus(destroyCommandID, 5*time.Minute)
	require.NoError(t, err, "Destroy command should complete successfully")
	assert.Equal(t, "Success", destroyStatus, "Destroy command should reach Success state")

	// Step 14: Verify resource no longer exists in inventory
	t.Log("Step 14: Verifying resource removed from inventory...")
	inventoryAfterDestroy, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	require.NoError(t, err, "Inventory command should succeed after destroy")
	assert.Len(t, inventoryAfterDestroy.Resources, 0, "Inventory should be empty after destroy")

	t.Logf("Resource lifecycle test completed successfully for %s!", tc.Name)
}
