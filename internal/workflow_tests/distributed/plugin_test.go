// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package distributed

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// TestMetastructure_ApplyForma_DistributedPlugin tests the distributed plugin workflow
// with FakeAWS running as a separate process that announces itself to PluginRegistry.
//
// This test verifies:
// - Building the fake-aws plugin binary
// - Starting the plugin process via PluginProcessSupervisor
// - Plugin announcing itself to PluginRegistry
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

		// Step 5: Wait for the plugin to register with PluginRegistry
		// The plugin sends a PluginAnnouncement to PluginRegistry which logs it
		foundMessage := logCapture.WaitForLog("Plugin registered", 5*time.Second)

		require.True(t, foundMessage, "Plugin should have registered with PluginRegistry")
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
	pluginManager := plugin.NewManager()
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
