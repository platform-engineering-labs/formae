// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/api"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

const clientID = "blackbox-test"

// TestHarness manages an in-process formae agent with an HTTP server and
// external test plugin for blackbox testing. All agent communication goes
// through the REST API via api.Client. TestController communication goes
// through Ergo cross-node calls via a harness-owned Ergo node.
type TestHarness struct {
	t          *testing.T
	client     *api.Client
	m          *metastructure.Metastructure
	cancel     context.CancelFunc
	cfg        *pkgmodel.Config
	pluginsDir string
	logCapture *logging.TestLogCapture

	// Ergo node for communicating with the test plugin's TestController
	ergoNode       gen.Node
	pluginNodeName gen.Atom
}

// NewTestHarness builds the test plugin binary, starts the agent metastructure
// and HTTP server, creates an Ergo node for TestController communication,
// and waits for the plugin to register.
func NewTestHarness(t *testing.T, timeout time.Duration) *TestHarness {
	t.Helper()

	// Create isolated plugins directory
	pluginsDir := t.TempDir()

	// Build the test plugin binary
	buildTestPluginToDir(t, pluginsDir)

	// Create test config with a free port for the HTTP server
	port := getFreePort(t)
	cfg := newTestConfig(t, port)

	// Setup log capture
	logCapture := setupLogCapture()

	// Create a cancellable context for the HTTP server
	ctx, cancel := context.WithCancel(context.Background())

	// Create and start metastructure
	m, pluginManager := startMetastructure(t, ctx, cfg, pluginsDir)

	// Start the HTTP server
	apiServer := api.NewServer(ctx, m, pluginManager, &cfg.Agent.Server, &pkgmodel.PluginConfig{}, nil)
	go apiServer.Start()

	// Create the API client
	client := api.NewClient(pkgmodel.APIConfig{
		URL:  "http://localhost",
		Port: port,
	}, nil, nil)

	// Wait for the HTTP server to be ready
	client.WaitOnAvailable()

	// Compute the test plugin's Ergo node name using the same formula as the
	// plugin process supervisor: {nodename}-{namespace}-plugin@{hostname}
	pluginNodeName := gen.Atom(fmt.Sprintf("%s-%s-plugin@%s",
		cfg.Agent.Server.Nodename,
		strings.ToLower("Test"),
		cfg.Agent.Server.Hostname,
	))

	h := &TestHarness{
		t:              t,
		client:         client,
		m:              m,
		cancel:         cancel,
		cfg:            cfg,
		pluginsDir:     pluginsDir,
		logCapture:     logCapture,
		pluginNodeName: pluginNodeName,
	}

	// Wait for the test plugin to register
	found := logCapture.WaitForLog("Plugin registered", timeout)
	require.True(t, found, "Test plugin should have registered within %v", timeout)
	t.Logf("Test plugin registered")

	// Start the harness Ergo node for TestController communication
	h.startErgoNode(t)

	return h
}

// Cleanup stops the Ergo node, HTTP server, metastructure, and cleans up.
func (h *TestHarness) Cleanup() {
	if h.ergoNode != nil {
		h.ergoNode.Stop()
	}
	h.cancel()
	h.m.Stop(true)
	if h.cfg.Agent.Datastore.Sqlite.FilePath != ":memory:" {
		_ = os.Remove(h.cfg.Agent.Datastore.Sqlite.FilePath)
	}
}

// Client returns the API client for direct use in tests.
func (h *TestHarness) Client() *api.Client {
	return h.client
}

// ApplyForma submits a forma apply command via the REST API and returns the command ID.
func (h *TestHarness) ApplyForma(forma *pkgmodel.Forma, mode pkgmodel.FormaApplyMode) string {
	h.t.Helper()

	resp, err := h.client.ApplyForma(forma, mode, false, clientID, false)
	require.NoError(h.t, err, "ApplyForma should not return an error")
	return resp.CommandID
}

// WaitForCommandDone polls the REST API until the command reaches a terminal
// state (Success, Failed) and returns the final command status.
// Fails the test if the command does not reach a terminal state within the timeout.
func (h *TestHarness) WaitForCommandDone(commandID string, timeout time.Duration) *apimodel.Command {
	h.t.Helper()

	result, ok := h.waitForCommand(commandID, timeout)
	require.True(h.t, ok, "Command %s should reach terminal state within %v", commandID, timeout)
	return result
}

// TryWaitForCommandDone polls the REST API until the command reaches a terminal
// state. Returns the command and true if it completed, or nil and false on timeout.
func (h *TestHarness) TryWaitForCommandDone(commandID string, timeout time.Duration) (*apimodel.Command, bool) {
	h.t.Helper()
	return h.waitForCommand(commandID, timeout)
}

func (h *TestHarness) waitForCommand(commandID string, timeout time.Duration) (*apimodel.Command, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		statusResp, err := h.client.GetFormaCommandsStatus("id:"+commandID, clientID, 1)
		if err == nil && statusResp != nil && len(statusResp.Commands) > 0 {
			cmd := statusResp.Commands[0]
			if cmd.State == "Success" || cmd.State == "Failed" || cmd.State == "Canceled" {
				return &cmd, true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, false
}

// --- TestController communication (via Ergo cross-node calls) ---

// GetCloudStateSnapshot queries the test plugin's TestController for the
// current cloud state. Returns the snapshot map keyed by native ID.
func (h *TestHarness) GetCloudStateSnapshot(t *testing.T) map[string]testcontrol.CloudStateEntry {
	t.Helper()

	resp, err := h.callTestController(testcontrol.GetCloudStateSnapshotRequest{})
	require.NoError(t, err, "GetCloudStateSnapshot failed")

	snapshot, ok := resp.(testcontrol.GetCloudStateSnapshotResponse)
	require.True(t, ok, "unexpected response type: %T", resp)
	return snapshot.Entries
}

// GetOperationLog queries the test plugin's TestController for the
// operation log. Returns the log entries.
func (h *TestHarness) GetOperationLog(t *testing.T) []testcontrol.OperationLogEntry {
	t.Helper()

	resp, err := h.callTestController(testcontrol.GetOperationLogRequest{})
	require.NoError(t, err, "GetOperationLog failed")

	opLog, ok := resp.(testcontrol.GetOperationLogResponse)
	require.True(t, ok, "unexpected response type: %T", resp)
	return opLog.Entries
}

// InjectError sends an error injection request to the test plugin's TestController.
func (h *TestHarness) InjectError(t *testing.T, req testcontrol.InjectErrorRequest) {
	t.Helper()

	_, err := h.callTestController(req)
	require.NoError(t, err, "InjectError failed")
}

// ClearInjections clears all fault injection rules in the test plugin's TestController.
func (h *TestHarness) ClearInjections(t *testing.T) {
	t.Helper()

	_, err := h.callTestController(testcontrol.ClearInjectionsRequest{})
	require.NoError(t, err, "ClearInjections failed")
}

// InjectLatency sends a latency injection request to the test plugin's TestController.
func (h *TestHarness) InjectLatency(t *testing.T, req testcontrol.InjectLatencyRequest) {
	t.Helper()

	_, err := h.callTestController(req)
	require.NoError(t, err, "InjectLatency failed")
}

// PutCloudState creates or updates a cloud state entry in the test plugin.
func (h *TestHarness) PutCloudState(t *testing.T, nativeID, resourceType, properties string) {
	t.Helper()

	_, err := h.callTestController(testcontrol.PutCloudStateRequest{
		NativeID:     nativeID,
		ResourceType: resourceType,
		Properties:   properties,
	})
	require.NoError(t, err, "PutCloudState failed")
}

// DeleteCloudState deletes a cloud state entry from the test plugin.
func (h *TestHarness) DeleteCloudState(t *testing.T, nativeID string) {
	t.Helper()

	_, err := h.callTestController(testcontrol.DeleteCloudStateRequest{
		NativeID: nativeID,
	})
	require.NoError(t, err, "DeleteCloudState failed")
}

// callTestController sends a synchronous Ergo call to the TestController
// actor on the test plugin's node.
func (h *TestHarness) callTestController(request any) (any, error) {
	target := gen.ProcessID{
		Name: testcontrol.TestControllerName,
		Node: h.pluginNodeName,
	}
	return callRemote(h.ergoNode, target, request, 5)
}

// --- internal helpers ---

// startErgoNode creates an Ergo node for the test harness. It uses the same
// network cookie as the agent so it can communicate with the test plugin's node.
func (h *TestHarness) startErgoNode(t *testing.T) {
	t.Helper()

	// Register testcontrol EDF types so Ergo can serialize our messages
	err := plugin.RegisterSharedEDFTypes()
	require.NoError(t, err, "RegisterSharedEDFTypes failed")
	err = testcontrol.RegisterEDFTypes()
	require.NoError(t, err, "RegisterEDFTypes failed")

	nodeName := gen.Atom(fmt.Sprintf("test-harness-%s@localhost", util.RandomString(8)))

	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	options.Network.Cookie = h.cfg.Agent.Server.Secret
	options.Log.Level = gen.LogLevelWarning

	node, err := ergo.StartNode(nodeName, options)
	require.NoError(t, err, "Failed to start harness Ergo node")
	h.ergoNode = node

	// Spawn the RemoteCaller actor for cross-node calls
	_, err = node.SpawnRegister("RemoteCaller", newRemoteCaller, gen.ProcessOptions{})
	require.NoError(t, err, "Failed to spawn RemoteCaller")

	t.Logf("Harness Ergo node started: %s (plugin node: %s)", nodeName, h.pluginNodeName)
}

func getFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

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

func newTestConfig(t *testing.T, port int) *pkgmodel.Config {
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
				Port:         port,
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

func startMetastructure(t *testing.T, ctx context.Context, cfg *pkgmodel.Config, pluginsDir string) (*metastructure.Metastructure, *plugin.Manager) {
	t.Helper()

	db, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
	require.NoError(t, err)

	pluginManager := plugin.NewManager(pluginsDir)
	pluginManager.Load()

	m, err := metastructure.NewMetastructureWithDataStoreAndContext(ctx, cfg, pluginManager, db, "test")
	require.NoError(t, err)

	err = m.Start()
	require.NoError(t, err)

	return m, pluginManager
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
			Properties: json.RawMessage(fmt.Sprintf(`{"Name":"%s","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`, name)),
			Schema:     testResourceSchema,
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
