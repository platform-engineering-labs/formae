// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package pluginsdk_test

import (
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

// runCRUDTest runs the CRUD lifecycle test for a single resource
func runCRUDTest(t *testing.T, harness *framework.TestHarness, tc framework.TestCase) {
	t.Logf("Testing resource: %s (file: %s)", tc.Name, tc.PKLFile)

	// Step 1: Apply the forma to create the resource
	t.Log("Step 1: Applying forma to create resource...")
	applyCommandID, err := harness.Apply(tc.PKLFile)
	require.NoError(t, err, "Apply command should succeed")
	assert.NotEmpty(t, applyCommandID, "Apply should return a command ID")

	// Step 2: Poll for apply command to complete successfully
	t.Log("Step 2: Polling for apply command completion...")
	applyStatus, err := harness.PollStatus(applyCommandID, 5*time.Minute)
	require.NoError(t, err, "Apply command should complete successfully")
	assert.Equal(t, "Success", applyStatus, "Apply command should reach Success state")

	// Step 3: Destroy the resource
	t.Log("Step 3: Destroying resource...")
	destroyCommandID, err := harness.Destroy(tc.PKLFile)
	require.NoError(t, err, "Destroy command should succeed")
	assert.NotEmpty(t, destroyCommandID, "Destroy should return a command ID")

	// Step 4: Poll for destroy command to complete successfully
	t.Log("Step 4: Polling for destroy command completion...")
	destroyStatus, err := harness.PollStatus(destroyCommandID, 5*time.Minute)
	require.NoError(t, err, "Destroy command should complete successfully")
	assert.Equal(t, "Success", destroyStatus, "Destroy command should reach Success state")

	t.Logf("CRUD test completed successfully for %s!", tc.Name)
}
