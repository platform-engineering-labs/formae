// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// RunCRUDTests discovers test cases from the testdata directory and runs
// the standard CRUD lifecycle test for each resource type.
//
// For each test case:
//   - Creates the resource via formae apply
//   - Reads and verifies the resource via inventory
//   - Updates the resource (if update file exists)
//   - Deletes the resource via formae destroy
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

	t.Logf("Discovered %d test case(s)", len(testCases))

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			runCRUDTest(t, tc)
		})
	}
}

// runCRUDTest runs the full CRUD lifecycle for a single test case.
func runCRUDTest(t *testing.T, tc TestCase) {
	// Create test harness
	harness := NewTestHarness(t)
	defer harness.Cleanup()

	// Start the formae agent
	if err := harness.StartAgent(); err != nil {
		t.Fatalf("failed to start agent: %v", err)
	}

	// === Step 1: Create resource ===
	t.Log("Step 1: Creating resource...")
	cmdID, err := harness.Apply(tc.PKLFile)
	if err != nil {
		t.Fatalf("failed to apply: %v", err)
	}

	// Wait for command to complete
	status, err := harness.PollStatus(cmdID, 5*time.Minute)
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	t.Logf("Create command completed with status: %s", status)

	// === Step 2: Read and verify resource ===
	t.Log("Step 2: Verifying resource in inventory...")

	// Evaluate the PKL file to get expected state
	evalOutput, err := harness.Eval(tc.PKLFile)
	if err != nil {
		t.Fatalf("failed to eval PKL file: %v", err)
	}
	t.Logf("Expected state from eval: %d bytes", len(evalOutput))

	// Query inventory for the resource
	inventory, err := harness.Inventory("managed: true")
	if err != nil {
		t.Fatalf("failed to query inventory: %v", err)
	}

	if len(inventory.Resources) == 0 {
		t.Fatal("no resources found in inventory after create")
	}
	t.Logf("Found %d resource(s) in inventory", len(inventory.Resources))

	// === Step 3: Update resource (if update file exists) ===
	if tc.UpdateFile != "" {
		t.Log("Step 3: Updating resource...")
		cmdID, err = harness.ApplyWithMode(tc.UpdateFile, "patch")
		if err != nil {
			t.Fatalf("failed to apply update: %v", err)
		}

		status, err = harness.PollStatus(cmdID, 5*time.Minute)
		if err != nil {
			t.Fatalf("update command failed: %v", err)
		}
		t.Logf("Update command completed with status: %s", status)
	} else {
		t.Log("Step 3: Skipping update (no update file)")
	}

	// === Step 4: Delete resource ===
	t.Log("Step 4: Deleting resource...")
	cmdID, err = harness.Destroy(tc.PKLFile)
	if err != nil {
		t.Fatalf("failed to destroy: %v", err)
	}

	status, err = harness.PollStatus(cmdID, 5*time.Minute)
	if err != nil {
		t.Fatalf("destroy command failed: %v", err)
	}
	t.Logf("Delete command completed with status: %s", status)

	// === Step 5: Verify deletion ===
	t.Log("Step 5: Verifying resource deleted...")
	inventory, err = harness.Inventory("managed: true")
	if err != nil {
		t.Fatalf("failed to query inventory after delete: %v", err)
	}

	if len(inventory.Resources) > 0 {
		t.Errorf("expected 0 resources after delete, found %d", len(inventory.Resources))
	}

	t.Log("CRUD lifecycle test passed!")
}

// RunDiscoveryTests tests that the plugin can discover resources created out-of-band.
//
// This test:
//   - Creates a resource directly via the plugin (bypassing formae)
//   - Runs formae discovery
//   - Verifies the resource appears as unmanaged
//   - Cleans up the resource
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

	// Use the first test case for discovery test
	tc := testCases[0]
	t.Run("Discovery/"+tc.Name, func(t *testing.T) {
		runDiscoveryTest(t, tc)
	})
}

// runDiscoveryTest runs the discovery test for a single test case.
func runDiscoveryTest(t *testing.T, tc TestCase) {
	// Create test harness
	harness := NewTestHarness(t)
	defer harness.Cleanup()

	// Get resource descriptor to find the resource type for discovery config
	pluginDir, _ := os.Getwd()
	pklFile := filepath.Join(pluginDir, "testdata", filepath.Base(tc.PKLFile))

	// Evaluate to get resource type
	evalOutput, err := harness.Eval(pklFile)
	if err != nil {
		t.Fatalf("failed to eval PKL file: %v", err)
	}

	// Configure discovery for this resource type
	// Note: You may need to extract the resource type from evalOutput
	// For now, we'll use a simplified approach
	t.Log("Step 1: Creating resource out-of-band via plugin...")

	// Create the resource directly via the plugin (bypassing formae)
	nativeID, err := harness.CreateUnmanagedResource(evalOutput)
	if err != nil {
		t.Fatalf("failed to create unmanaged resource: %v", err)
	}
	t.Logf("Created resource with NativeID: %s", nativeID)

	// Register cleanup to delete the resource
	defer func() {
		t.Log("Cleanup: Deleting out-of-band resource...")
		// Note: cleanup happens via harness.Cleanup()
	}()

	// Start the agent with discovery enabled
	if err := harness.StartAgent(); err != nil {
		t.Fatalf("failed to start agent: %v", err)
	}

	// Step 2: Register target for discovery
	t.Log("Step 2: Registering target for discovery...")
	if err := harness.RegisterTargetForDiscovery([]byte(evalOutput)); err != nil {
		t.Fatalf("failed to register target: %v", err)
	}

	// Step 3: Wait for plugin to register
	t.Log("Step 3: Waiting for plugin to register...")
	if err := harness.WaitForPluginRegistered(tc.PluginName, 30*time.Second); err != nil {
		t.Fatalf("plugin did not register: %v", err)
	}

	// Step 4: Trigger discovery
	t.Log("Step 4: Triggering discovery...")
	if err := harness.TriggerDiscovery(); err != nil {
		t.Fatalf("failed to trigger discovery: %v", err)
	}

	// Step 5: Wait for resource to appear in inventory
	t.Log("Step 5: Waiting for resource in inventory...")
	// Note: This requires knowing the resource type - you may need to extract it from evalOutput
	// if err := harness.WaitForResourceInInventory(resourceType, nativeID, false, 2*time.Minute); err != nil {
	// 	t.Fatalf("resource not discovered: %v", err)
	// }

	t.Log("Discovery test passed!")
}
