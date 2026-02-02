// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package test_helpers

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"
)

func DelegatesByNamespace(t *testing.T, m *metastructure.Metastructure, namespace string) int {
	pids, err := m.Node.ProcessList()
	assert.NoError(t, err)

	var ret int
	for _, p := range pids {
		pi, err := m.Node.ProcessInfo(p)
		if err != nil {
			t.Errorf("Failed to get process info: %v", err)
		}

		uri := pkgmodel.TripletURI(string(pi.Name))
		if uri.IsValid() {
			ns := uri.Namespace()
			if strings.EqualFold(ns, namespace) {
				ret++
			}
		}
	}

	return ret
}

// SetupTestLogger configures a test logger that captures all log output
// Returns the log capture that tests can use for assertions
func SetupTestLogger() *logging.TestLogCapture {
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

func NewTestMetastructureConfig() *pkgmodel.Config {
	// Note: logging.SetupInitialLogging() removed - tests should call
	// SetupTestLogger() if they need log capture, or call
	// logging.SetupInitialLogging() explicitly if they need standard logging
	prefix := util.RandomString(10) + "-"

	// Create a temporary file for the database
	tmpFile, err := os.CreateTemp("", "formae_test_*.db")
	if err != nil {
		panic(err)
	}

	_ = tmpFile.Close() // Close it so SQLite can open it

	return &pkgmodel.Config{
		Agent: pkgmodel.AgentConfig{
			Server: pkgmodel.ServerConfig{
				Nodename:     "formae-test-" + prefix,
				Hostname:     "localhost",
				ObserverPort: 0,
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
				Enabled: false, // we turn off the synchronization for tests
			},
			Discovery: pkgmodel.DiscoveryConfig{
				Interval: 5 * time.Minute,
				Enabled:  false,
			},
			// Note: StackExpirer always runs with 5s interval (DefaultStackExpirerInterval)
		},
		Plugins: pkgmodel.PluginConfig{},
	}
}

func NewTestMetastructureWithConfig(t *testing.T, pluginOverrides *plugin.ResourcePluginOverrides, cfg *pkgmodel.Config) (*metastructure.Metastructure, func(), error) {
	db, err := datastore.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
	if err != nil {
		t.Error(err)
	}

	return NewTestMetastructureWithEverything(t, pluginOverrides, db, cfg)
}

func NewTestMetastructure(t *testing.T, pluginOverrides *plugin.ResourcePluginOverrides) (*metastructure.Metastructure, func(), error) {
	cfg := NewTestMetastructureConfig()
	db, err := datastore.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
	if err != nil {
		t.Error(err)
	}

	return NewTestMetastructureWithEverything(t, pluginOverrides, db, cfg)
}

func NewTestMetastructureWithEverything(t *testing.T, pluginOverrides *plugin.ResourcePluginOverrides, db datastore.Datastore, cfg *pkgmodel.Config) (*metastructure.Metastructure, func(), error) {
	ctx, cancel := testutil.PluginOverridesContext(pluginOverrides)
	pluginManager := plugin.NewManager("")
	pluginManager.Load()
	m, err := metastructure.NewMetastructureWithDataStoreAndContext(ctx, cfg, pluginManager, db, "test")
	if err != nil {
		t.Error(err)
	}

	f := func() {
		if m != nil {
			m.Stop(true)
		}

		// Clean up the temporary database file - getting created in NewTestMetastructureConfig
		// Added workaround that if there is no-reset in the file path, it won't delete the file
		if cfg.Agent.Datastore.Sqlite.FilePath != ":memory:" &&
			!strings.Contains(cfg.Agent.Datastore.Sqlite.FilePath, ":memory:") &&
			!strings.Contains(cfg.Agent.Datastore.Sqlite.FilePath, "no-reset") {
			_ = os.Remove(cfg.Agent.Datastore.Sqlite.FilePath)
		}

		cancel()
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- m.Start()
	}()

	if err := <-errChan; err != nil {
		t.Errorf("Failed to start metastructure: %v", err)
		return nil, f, err
	}

	return m, f, nil
}
