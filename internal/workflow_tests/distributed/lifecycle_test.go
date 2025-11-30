// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
)

// TestPluginRestart_AfterCrash verifies that when a plugin crashes,
// PluginProcessSupervisor restarts it and it re-registers successfully.
func TestPluginRestart_AfterCrash(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Step 1: Build the fake-aws plugin binary
		pluginBinaryPath := buildFakeAWSPluginToStandardLocation(t)
		defer cleanupPluginBinary(t, pluginBinaryPath)

		// Step 2: Setup test configuration
		cfg := newDistributedTestConfig(t)
		defer cleanupTestDatabase(t, cfg)

		// Step 3: Setup test logger to capture logs
		logCapture := setupTestLogger()

		// Step 4: Create and start metastructure
		m := newDistributedMetastructure(t, cfg)
		defer m.Stop(true)

		// Step 5: Wait for the plugin to register initially
		foundMessage := logCapture.WaitForLog("Plugin registered", 5*time.Second)
		require.True(t, foundMessage, "Plugin should have registered with PluginCoordinator")
		t.Logf("✓ Plugin registered initially")

		// Step 6: Verify plugin process is running
		require.True(t, isPluginProcessRunning("fake-aws-plugin"),
			"Plugin process should be running")
		t.Logf("✓ Plugin process is running")

		// Step 7: Kill the plugin process with SIGKILL (simulate crash)
		t.Logf("Killing fake-aws-plugin process...")
		cmd := exec.Command("pkill", "-9", "-f", "fake-aws-plugin")
		err := cmd.Run()
		require.NoError(t, err, "pkill should succeed")
		t.Logf("✓ Plugin process killed")

		// Step 8: Wait for plugin to be detected as terminated and restarted
		// Look for the restart log message
		foundRestart := logCapture.WaitForLog("Plugin restarted successfully", 10*time.Second)
		require.True(t, foundRestart, "Plugin should have been restarted by PluginProcessSupervisor")
		t.Logf("✓ Plugin restart initiated")

		// Step 9: Wait for the plugin to re-register with PluginCoordinator
		// Clear previous logs and wait for new registration
		foundReRegister := logCapture.WaitForLog("Plugin registered", 10*time.Second)
		require.True(t, foundReRegister, "Plugin should have re-registered after restart")
		t.Logf("✓ Plugin re-registered after restart")

		// Step 10: Verify plugin process is running again
		require.Eventually(t, func() bool {
			return isPluginProcessRunning("fake-aws-plugin")
		}, 5*time.Second, 100*time.Millisecond,
			"Plugin process should be running after restart")
		t.Logf("✓ Plugin process is running after restart")
	})
}

// cleanupPluginBinary removes the plugin binary after test
func cleanupPluginBinary(t *testing.T, path string) {
	t.Helper()
	// Don't remove - other tests may need it
	// os.Remove(path)
}

// TestPluginShutdown_AfterAgentStop verifies that when the agent stops,
// plugins detect this via MonitorNode and shut down gracefully.
//
// Note: This test verifies the plugin's MonitorNode mechanism works by checking
// that plugin processes terminate when the agent connection is lost.
// The existing HappyPath test already checks that plugins terminate within 2s
// after m.Stop(true), but this test explicitly verifies the monitoring behavior.
func TestPluginShutdown_AfterAgentStop(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Step 1: Build the fake-aws plugin binary
		pluginBinaryPath := buildFakeAWSPluginToStandardLocation(t)
		defer cleanupPluginBinary(t, pluginBinaryPath)

		// Step 2: Setup test configuration
		cfg := newDistributedTestConfig(t)
		defer cleanupTestDatabase(t, cfg)

		// Step 3: Setup test logger to capture logs
		logCapture := setupTestLogger()

		// Step 4: Create and start metastructure
		m := newDistributedMetastructure(t, cfg)
		// NOT using defer m.Stop(true) - we want to test manual stop behavior

		// Step 5: Wait for the plugin to register
		foundMessage := logCapture.WaitForLog("Plugin registered", 5*time.Second)
		require.True(t, foundMessage, "Plugin should have registered with PluginCoordinator")
		t.Logf("✓ Plugin registered")

		// Step 6: Verify plugin process is running
		require.True(t, isPluginProcessRunning("fake-aws-plugin"),
			"Plugin process should be running")
		t.Logf("✓ Plugin process is running")

		// Step 7: Stop the agent (this closes the network connection, triggering MessageDownNode)
		t.Logf("Stopping agent...")
		m.Stop(true)
		t.Logf("✓ Agent stopped")

		// Step 8: Verify the plugin process terminates
		// Plugin should detect agent node is down via MonitorNode and shut down
		require.Eventually(t, func() bool {
			return !isPluginProcessRunning("fake-aws-plugin")
		}, 5*time.Second, 100*time.Millisecond,
			"Plugin process should terminate after agent stops")
		t.Logf("✓ Plugin process terminated after agent stop")
	})
}
