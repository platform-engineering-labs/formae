// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build blackbox

package blackbox

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// TestHarness manages an in-process formae agent with an external test plugin
// for blackbox testing. It provides helpers for applying formas, querying
// inventory, and verifying command outcomes.
type TestHarness struct {
	t          *testing.T
	m          *metastructure.Metastructure
	cfg        *pkgmodel.Config
	pluginsDir string
	logCapture *logging.TestLogCapture
}

// NewTestHarness builds the test plugin binary, starts the agent metastructure,
// and waits for the plugin to register.
func NewTestHarness(t *testing.T, timeout time.Duration) *TestHarness {
	t.Helper()

	// Create isolated plugins directory
	pluginsDir := t.TempDir()

	// Build the test plugin binary
	buildTestPluginToDir(t, pluginsDir)

	// Create test config
	cfg := newTestConfig(t)

	// Setup log capture
	logCapture := setupLogCapture()

	// Create and start metastructure
	m := startMetastructure(t, cfg, pluginsDir)

	h := &TestHarness{
		t:          t,
		m:          m,
		cfg:        cfg,
		pluginsDir: pluginsDir,
		logCapture: logCapture,
	}

	// Wait for the test plugin to register
	found := logCapture.WaitForLog("Plugin registered", timeout)
	require.True(t, found, "Test plugin should have registered within %v", timeout)
	t.Logf("Test plugin registered")

	return h
}

// Cleanup stops the metastructure and cleans up test resources.
func (h *TestHarness) Cleanup() {
	h.m.Stop(true)
	if h.cfg.Agent.Datastore.Sqlite.FilePath != ":memory:" {
		_ = os.Remove(h.cfg.Agent.Datastore.Sqlite.FilePath)
	}
}

// ApplyForma submits a forma apply command and returns the command ID.
func (h *TestHarness) ApplyForma(forma *pkgmodel.Forma, mode pkgmodel.FormaApplyMode) string {
	h.t.Helper()

	resp, err := h.m.ApplyForma(
		forma,
		&config.FormaCommandConfig{Mode: mode, Simulate: false},
		"blackbox-test",
	)
	require.NoError(h.t, err, "ApplyForma should not return an error")
	return resp.CommandID
}

// WaitForCommandDone polls until the command reaches a terminal state (Success, Failed)
// and returns the final command.
func (h *TestHarness) WaitForCommandDone(commandID string, timeout time.Duration) *forma_command.FormaCommand {
	h.t.Helper()

	var result *forma_command.FormaCommand
	require.Eventually(h.t, func() bool {
		commands, err := h.m.Datastore.LoadFormaCommands()
		if err != nil {
			return false
		}
		for _, cmd := range commands {
			if cmd.ID == commandID {
				if cmd.State == forma_command.CommandStateSuccess || cmd.State == forma_command.CommandStateFailed {
					result = cmd
					return true
				}
			}
		}
		return false
	}, timeout, 100*time.Millisecond, "Command %s should reach terminal state within %v", commandID, timeout)

	return result
}

// --- internal helpers ---

func buildTestPluginToDir(t *testing.T, baseDir string) {
	t.Helper()

	// Layout: <baseDir>/test-plugin/v0.0.1/test-plugin
	pluginDir := filepath.Join(baseDir, "test-plugin", "v0.0.1")
	err := os.MkdirAll(pluginDir, 0755)
	require.NoError(t, err)

	binaryPath := filepath.Join(pluginDir, "test-plugin")

	// Build the test plugin binary
	cmd := exec.Command("go", "build",
		"-C", "./tests/testplugin",
		"-o", binaryPath,
		".",
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build test plugin: %s", string(output))

	// Copy the PKL manifest
	manifestSrc := filepath.Join("tests", "testplugin", "formae-plugin.pkl")
	manifestDst := filepath.Join(pluginDir, "formae-plugin.pkl")
	copyFile(t, manifestSrc, manifestDst)

	t.Logf("Built test plugin at: %s", binaryPath)
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	require.NoError(t, err)
	defer in.Close()

	out, err := os.Create(dst)
	require.NoError(t, err)
	defer out.Close()

	_, err = io.Copy(out, in)
	require.NoError(t, err)
}

func newTestConfig(t *testing.T) *pkgmodel.Config {
	t.Helper()

	prefix := util.RandomString(10) + "-"

	tmpFile, err := os.CreateTemp("", "formae_blackbox_test_*.db")
	require.NoError(t, err)
	_ = tmpFile.Close()

	cookie := util.RandomString(32)

	return &pkgmodel.Config{
		Agent: pkgmodel.AgentConfig{
			Server: pkgmodel.ServerConfig{
				Nodename:     "agent-bb-" + prefix,
				Hostname:     "localhost",
				ObserverPort: 0,
				Secret:       cookie,
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
	}
}

func startMetastructure(t *testing.T, cfg *pkgmodel.Config, pluginsDir string) *metastructure.Metastructure {
	t.Helper()

	db, err := datastore.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
	require.NoError(t, err)

	pluginManager := plugin.NewManager(pluginsDir)
	pluginManager.Load()

	ctx := context.Background()
	m, err := metastructure.NewMetastructureWithDataStoreAndContext(ctx, cfg, pluginManager, db, "test")
	require.NoError(t, err)

	err = m.Start()
	require.NoError(t, err)

	return m
}

func setupLogCapture() *logging.TestLogCapture {
	capture := logging.NewTestLogCapture()
	handler := slog.NewTextHandler(capture, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))
	return capture
}

// SimpleForma creates a forma with N Test::Generic::Resource resources in the default stack.
func SimpleForma(n int) *pkgmodel.Forma {
	resources := make([]pkgmodel.Resource, n)
	for i := range n {
		name := "res-" + string(rune('a'+i))
		resources[i] = pkgmodel.Resource{
			Label:      name,
			Type:       "Test::Generic::Resource",
			Stack:      "default",
			Target:     "test-target",
			Properties: json.RawMessage(`{"Name":"` + name + `","Value":"v1"}`),
			Managed:    true,
		}
	}

	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{
			{Label: "default"},
		},
		Resources: resources,
		Targets: []pkgmodel.Target{
			{
				Label:     "test-target",
				Namespace: "Test",
			},
		},
	}
}