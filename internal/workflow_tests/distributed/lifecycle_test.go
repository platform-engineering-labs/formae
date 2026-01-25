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
		// Step 1: Create isolated plugins directory for this test
		testPluginsDir := t.TempDir()

		// Step 2: Build the fake-aws plugin binary into isolated directory
		pluginBinaryPath := buildFakeAWSPluginToDir(t, testPluginsDir)
		defer cleanupPluginBinary(t, pluginBinaryPath)

		// Step 3: Setup test configuration
		cfg := newDistributedTestConfig(t)
		defer cleanupTestDatabase(t, cfg)

		// Step 4: Setup test logger to capture logs
		logCapture := setupTestLogger()

		// Step 5: Create and start metastructure with isolated plugins directory
		m := newDistributedMetastructure(t, cfg, testPluginsDir)
		defer m.Stop(true)

		// Step 6: Wait for the plugin to register initially
		foundMessage := logCapture.WaitForLog("Plugin registered", 5*time.Second)
		require.True(t, foundMessage, "Plugin should have registered with PluginCoordinator")
		t.Logf("✓ Plugin registered initially")

		// Step 7: Verify plugin process is running
		require.True(t, isPluginProcessRunning("fake-aws"),
			"Plugin process should be running")
		t.Logf("✓ Plugin process is running")

		// Step 8: Kill the plugin process with SIGKILL (simulate crash)
		t.Logf("Killing fake-aws process...")
		cmd := exec.Command("pkill", "-9", "-f", "fake-aws")
		err := cmd.Run()
		require.NoError(t, err, "pkill should succeed")
		t.Logf("✓ Plugin process killed")

		// Step 9: Wait for plugin to be detected as terminated and restarted
		// Look for the restart log message
		foundRestart := logCapture.WaitForLog("Plugin restarted successfully", 10*time.Second)
		require.True(t, foundRestart, "Plugin should have been restarted by PluginProcessSupervisor")
		t.Logf("✓ Plugin restart initiated")

		// Step 10: Wait for the plugin to re-register with PluginCoordinator
		// Clear previous logs and wait for new registration
		foundReRegister := logCapture.WaitForLog("Plugin registered", 10*time.Second)
		require.True(t, foundReRegister, "Plugin should have re-registered after restart")
		t.Logf("✓ Plugin re-registered after restart")

		// Step 11: Verify plugin process is running again
		require.Eventually(t, func() bool {
			return isPluginProcessRunning("fake-aws")
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
		// Step 1: Create isolated plugins directory for this test
		testPluginsDir := t.TempDir()

		// Step 2: Build the fake-aws plugin binary into isolated directory
		pluginBinaryPath := buildFakeAWSPluginToDir(t, testPluginsDir)
		defer cleanupPluginBinary(t, pluginBinaryPath)

		// Step 3: Setup test configuration
		cfg := newDistributedTestConfig(t)
		defer cleanupTestDatabase(t, cfg)

		// Step 4: Setup test logger to capture logs
		logCapture := setupTestLogger()

		// Step 5: Create and start metastructure with isolated plugins directory
		m := newDistributedMetastructure(t, cfg, testPluginsDir)
		// NOT using defer m.Stop(true) - we want to test manual stop behavior

		// Step 6: Wait for the plugin to register
		foundMessage := logCapture.WaitForLog("Plugin registered", 5*time.Second)
		require.True(t, foundMessage, "Plugin should have registered with PluginCoordinator")
		t.Logf("✓ Plugin registered")

		// Step 7: Verify plugin process is running
		require.True(t, isPluginProcessRunning("fake-aws"),
			"Plugin process should be running")
		t.Logf("✓ Plugin process is running")

		// Step 8: Stop the agent (this closes the network connection, triggering MessageDownNode)
		t.Logf("Stopping agent...")
		m.Stop(true)
		t.Logf("✓ Agent stopped")

		// Step 9: Verify the plugin process terminates
		// Plugin should detect agent node is down via MonitorNode and shut down
		require.Eventually(t, func() bool {
			return !isPluginProcessRunning("fake-aws")
		}, 5*time.Second, 100*time.Millisecond,
			"Plugin process should terminate after agent stops")
		t.Logf("✓ Plugin process terminated after agent stop")
	})
}
