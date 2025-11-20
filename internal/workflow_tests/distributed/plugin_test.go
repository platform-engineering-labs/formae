// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package distributed

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// TestMetastructure_ApplyForma_DistributedPlugin tests the full apply workflow
// with FakeAWS running as a distributed plugin in a separate process.
//
// This test verifies:
// - Building the fake-aws plugin binary
// - Starting the plugin process via PluginCoordinator
// - Executing a create operation through remote PluginOperator
// - Completing the apply workflow successfully
func TestMetastructure_ApplyForma_DistributedPlugin(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Step 1: Build the fake-aws plugin binary
		pluginBinaryPath := buildFakeAWSPlugin(t)
		defer os.Remove(pluginBinaryPath)

		// Step 2: Setup test configuration with plugin discovery
		cfg := newDistributedTestConfig(t, pluginBinaryPath)
		defer cleanupTestDatabase(t, cfg)

		// Step 3: Create metastructure with distributed plugin support
		m := newDistributedMetastructure(t, cfg)
		defer m.Stop(true)

		// Step 4: Define a simple Forma with one FakeAWS resource
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "test-bucket",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "FakeAWS",
				},
			},
		}

		// Step 5: Apply the forma (create resource via distributed plugin)
		_, err := m.ApplyForma(
			forma,
			&config.FormaCommandConfig{
				Mode:     pkgmodel.FormaApplyModeReconcile,
				Simulate: false,
			},
			"test-client-id",
		)
		require.NoError(t, err, "ApplyForma should succeed")

		// Step 6: Wait for command to complete
		var commands []*forma_command.FormaCommand
		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)

			incompleteCommands, err := m.Datastore.LoadIncompleteFormaCommands()
			assert.NoError(t, err)

			// Success: one completed command, no incomplete commands
			return len(commands) == 1 && len(incompleteCommands) == 0
		}, 10*time.Second, 100*time.Millisecond, "Command should complete successfully")

		// Step 7: Verify the resource was created
		require.Len(t, commands, 1, "Should have one completed command")
		assert.True(t, commands[0].Forma.Resources[0].Managed, "Resource should be managed")
		assert.Equal(t, "test-bucket", commands[0].Forma.Resources[0].Label)
		assert.Equal(t, "FakeAWS::S3::Bucket", commands[0].Forma.Resources[0].Type)
	})
}

// buildFakeAWSPlugin compiles the fake-aws plugin binary for testing
func buildFakeAWSPlugin(t *testing.T) string {
	t.Helper()

	// Create temp directory for the plugin binary
	tmpDir := t.TempDir()
	pluginPath := filepath.Join(tmpDir, "fake-aws-plugin")

	// Build the plugin binary
	cmd := exec.Command("go", "build",
		"-o", pluginPath,
		"./plugins/fake-aws",
	)

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build fake-aws plugin: %s", string(output))

	// Verify the binary was created
	_, err = os.Stat(pluginPath)
	require.NoError(t, err, "Plugin binary should exist at %s", pluginPath)

	t.Logf("Built fake-aws plugin at: %s", pluginPath)
	return pluginPath
}

// newDistributedTestConfig creates a test configuration for distributed plugins
func newDistributedTestConfig(t *testing.T, pluginBinaryPath string) *pkgmodel.Config {
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
			// TODO: Add ResourcePlugins configuration
			// This will tell PluginCoordinator where to find the fake-aws binary
			// Resources: []pkgmodel.ResourcePluginConfig{
			// 	{
			// 		Namespace:   "FakeAWS",
			// 		Enabled:     true,
			// 		BinaryPath:  pluginBinaryPath,
			// 		Environment: map[string]string{},
			// 	},
			// },
		},
	}
}

// newDistributedMetastructure creates a metastructure instance configured for distributed plugins
func newDistributedMetastructure(t *testing.T, cfg *pkgmodel.Config) *metastructure.Metastructure {
	t.Helper()

	// Create datastore
	db, err := datastore.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
	require.NoError(t, err, "Failed to create datastore")

	// Create plugin manager WITHOUT loading in-process plugins
	pluginManager := plugin.NewManager()
	// NOTE: We do NOT call pluginManager.Load() here
	// The PluginCoordinator will spawn external plugin processes

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
