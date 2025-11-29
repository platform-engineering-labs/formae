// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package distributed

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	internalutil "github.com/platform-engineering-labs/formae/internal/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// TestMetastructure_ApplyForma_DistributedPlugin tests the distributed plugin workflow
// with FakeAWS running as a separate process that announces itself to PluginCoordinator.
//
// This test verifies:
// - Building the fake-aws plugin binary
// - Starting the plugin process via PluginProcessSupervisor
// - Plugin announcing itself to PluginCoordinator
// - Plugin registration being logged
func TestMetastructure_ApplyForma_DistributedPlugin(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Step 1: Build the fake-aws plugin binary with hardcoded test behavior
		pluginBinaryPath := buildFakeAWSPluginToStandardLocation(t)
		defer os.Remove(pluginBinaryPath)

		// Step 2: Setup test configuration
		cfg := newDistributedTestConfig(t)
		defer cleanupTestDatabase(t, cfg)

		// Step 3: Setup test logger to capture all logs
		logCapture := setupTestLogger()

		// Step 4: Create and start metastructure
		// This should trigger PluginCoordinator to scan ~/.pel/formae/plugins/
		// and spawn the fake-aws plugin via meta.Port
		m := newDistributedMetastructure(t, cfg)
		defer m.Stop(true)

		// Step 5: Wait for the plugin to register with PluginCoordinator
		// The plugin sends a PluginAnnouncement to PluginCoordinator which logs it
		foundMessage := logCapture.WaitForLog("Plugin registered", 5*time.Second)

		require.True(t, foundMessage, "Plugin should have registered with PluginCoordinator")
		t.Logf("✓ Plugin registered successfully")
	})
}

// buildFakeAWSPlugin compiles the fake-aws plugin binary for testing
func buildFakeAWSPlugin(t *testing.T) string {
	t.Helper()

	// Build to standard plugin location: ~/.pel/formae/plugins/fake-aws/v1.0.0/fake-aws-plugin
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err, "Failed to get home directory")

	pluginDir := filepath.Join(homeDir, ".pel", "formae", "plugins", "fake-aws", "v1.0.0")
	err = os.MkdirAll(pluginDir, 0755)
	require.NoError(t, err, "Failed to create plugin directory")

	pluginPath := filepath.Join(pluginDir, "fake-aws-plugin")

	// Build the plugin binary from its own module directory
	cmd := exec.Command("go", "build",
		"-C", "./plugins/fake-aws",
		"-o", pluginPath,
		".",
	)

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build fake-aws plugin: %s", string(output))

	// Verify the binary was created
	_, err = os.Stat(pluginPath)
	require.NoError(t, err, "Plugin binary should exist at %s", pluginPath)

	t.Logf("Built fake-aws plugin at: %s", pluginPath)
	return pluginPath
}

// Alias for clarity in test
func buildFakeAWSPluginToStandardLocation(t *testing.T) string {
	return buildFakeAWSPlugin(t)
}

// newDistributedTestConfig creates a test configuration for distributed plugins
func newDistributedTestConfig(t *testing.T) *pkgmodel.Config {
	t.Helper()

	prefix := util.RandomString(10) + "-"

	// Create temporary database
	tmpFile, err := os.CreateTemp("", "formae_distributed_test_*.db")
	require.NoError(t, err)
	_ = tmpFile.Close()

	// Generate a unique network cookie for this test
	cookie := util.RandomString(32)

	return &pkgmodel.Config{
		Agent: pkgmodel.AgentConfig{
			Server: pkgmodel.ServerConfig{
				Nodename:     "agent-test-" + prefix,
				Hostname:     "localhost",
				ObserverPort: 0,
				Secret:       cookie, // Network cookie for plugin communication
			},
			Datastore: pkgmodel.DatastoreConfig{
				DatastoreType: pkgmodel.SqliteDatastore,
				Sqlite: pkgmodel.SqliteConfig{
					FilePath: tmpFile.Name(),
				},
			},
			Retry: pkgmodel.RetryConfig{
				MaxRetries:          2,
				RetryDelay:          1 * time.Second,
				StatusCheckInterval: 1 * time.Second,
			},
			Synchronization: pkgmodel.SynchronizationConfig{
				Enabled: false,
			},
			Discovery: pkgmodel.DiscoveryConfig{
				Enabled: false,
			},
		},
		Plugins: pkgmodel.PluginConfig{
			// PluginCoordinator will scan ~/.pel/formae/plugins/ for plugins
		},
	}
}

// newDistributedMetastructure creates a metastructure instance configured for distributed plugins
func newDistributedMetastructure(t *testing.T, cfg *pkgmodel.Config) *metastructure.Metastructure {
	t.Helper()

	// Create datastore
	db, err := datastore.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
	require.NoError(t, err, "Failed to create datastore")

	// Create plugin manager and load all plugins
	// Load() discovers both in-process plugins and external resource plugins
	pluginManager := plugin.NewManager(internalutil.ExpandHomePath("~/.pel/formae/plugins"))
	pluginManager.Load()

	// Create metastructure
	ctx := context.Background()
	m, err := metastructure.NewMetastructureWithDataStoreAndContext(
		ctx,
		cfg,
		pluginManager,
		db,
		"test",
	)
	require.NoError(t, err, "Failed to create metastructure")

	// Start the metastructure (this should spawn PluginCoordinator and plugin processes)
	err = m.Start()
	require.NoError(t, err, "Failed to start metastructure")

	t.Logf("Started metastructure with distributed plugins")
	return m
}

// cleanupTestDatabase removes the test database file
func cleanupTestDatabase(t *testing.T, cfg *pkgmodel.Config) {
	t.Helper()
	if cfg.Agent.Datastore.Sqlite.FilePath != ":memory:" {
		_ = os.Remove(cfg.Agent.Datastore.Sqlite.FilePath)
	}
}

// setupTestLogger configures a test logger that captures all log output
// Returns the log capture that tests can use for assertions
func setupTestLogger() *logging.TestLogCapture {
	capture := logging.NewTestLogCapture()

	// Create a text handler that writes to our capture
	handler := slog.NewTextHandler(capture, &slog.HandlerOptions{
		Level: slog.LevelDebug, // Capture all debug logs
	})

	// Set as the global default - this will affect all slog calls
	// including those from ErgoLogger
	slog.SetDefault(slog.New(handler))

	return capture
}

// isPluginProcessRunning checks if a process with the given name pattern is running
func isPluginProcessRunning(processName string) bool {
	cmd := exec.Command("pgrep", "-f", processName)
	err := cmd.Run()
	// pgrep returns exit code 0 if a process is found, non-zero otherwise
	return err == nil
}

// TestMetastructure_ApplyForma_DistributedPlugin_HappyPath tests a full apply workflow
// with FakeAWS running as a separate process.
//
// This test verifies the end-to-end flow:
// - Building and starting the fake-aws plugin as a separate process
// - Plugin announcing itself to PluginCoordinator with capabilities
// - Submitting a Forma apply command
// - PluginOperator routing CRUD operations to the remote plugin
// - Command completing successfully
func TestMetastructure_ApplyForma_DistributedPlugin_HappyPath(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Step 1: Build the fake-aws plugin binary
		pluginBinaryPath := buildFakeAWSPluginToStandardLocation(t)
		defer os.Remove(pluginBinaryPath)

		// Step 2: Setup test configuration
		cfg := newDistributedTestConfig(t)
		defer cleanupTestDatabase(t, cfg)

		// Step 3: Setup test logger to capture logs
		logCapture := setupTestLogger()

		// Step 4: Create and start metastructure
		m := newDistributedMetastructure(t, cfg)
		// NOTE: We don't use defer m.Stop(true) here because we need to
		// explicitly test the shutdown behavior

		// Step 5: Wait for the plugin to register
		foundMessage := logCapture.WaitForLog("Plugin registered", 5*time.Second)
		require.True(t, foundMessage, "Plugin should have registered with PluginCoordinator")
		t.Logf("✓ Plugin registered successfully")

		// Step 6: Submit a Forma apply command (mirrors happy_path_test.go)
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "test-stack",
					Target:     "test-target",
					Properties: json.RawMessage(`{"BucketName":"my-test-bucket"}`),
					Managed:    true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "FakeAWS",
				},
			},
		}

		_, err := m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id",
		)
		require.NoError(t, err, "ApplyForma should not return an error")

		// Step 7: Wait for the command to complete successfully
		var commands []*forma_command.FormaCommand
		require.Eventually(t, func() bool {
			var err error
			commands, err = m.Datastore.LoadFormaCommands()
			if err != nil {
				t.Logf("Error loading commands: %v", err)
				return false
			}
			if len(commands) > 0 {
				t.Logf("Command state: %s", commands[0].State)
			}

			return len(commands) == 1 && commands[0].State == forma_command.CommandStateSuccess
		}, 10*time.Second, 100*time.Millisecond, "Command should complete successfully")

		require.Len(t, commands[0].ResourceUpdates[0].ProgressResult, 1)
		assert.Equal(t, "1234", commands[0].ResourceUpdates[0].ProgressResult[0].RequestID)
		assert.Equal(t, "5678", commands[0].ResourceUpdates[0].ProgressResult[0].NativeID)
		assert.Equal(t, json.RawMessage(`{"BucketName":"my-test-bucket"}`), commands[0].ResourceUpdates[0].ProgressResult[0].ResourceProperties)

		t.Logf("✓ Apply command completed successfully")

		// Step 8: Verify the plugin process is running before shutdown
		require.True(t, isPluginProcessRunning("fake-aws-plugin"),
			"Plugin process should be running before shutdown")
		t.Logf("✓ Plugin process is running before shutdown")

		// Step 9: Stop the metastructure
		m.Stop(true)
		t.Logf("✓ Metastructure stopped")

		// Step 10: Verify the plugin process has been terminated
		// Wait a reasonable amount of time for graceful shutdown
		// With proper graceful shutdown, the plugin should terminate within 2 seconds
		var pluginStillRunning bool
		require.Eventually(t, func() bool {
			pluginStillRunning = isPluginProcessRunning("fake-aws-plugin")
			return !pluginStillRunning
		}, 2*time.Second, 100*time.Millisecond,
			"Plugin process should be terminated within 2 seconds after metastructure stops")
		t.Logf("✓ Plugin process terminated after shutdown")
	})
}
