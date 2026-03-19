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
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/api"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

const clientID = "blackbox-test"

// formaeBinary is set by TestMain to the path of the compiled formae binary.
var formaeBinary string

// TestHarness manages a formae agent subprocess with an external test plugin
// for blackbox testing. All agent communication goes through the REST API via
// api.Client. TestController communication goes through Ergo cross-node calls
// via a harness-owned Ergo node.
type TestHarness struct {
	t          *testing.T
	client     *api.Client
	cancel     context.CancelFunc
	pluginsDir string

	// Agent subprocess
	agentCmd     *exec.Cmd
	agentLogFile *os.File
	agentDataDir string

	// Config values needed for restarts and Ergo connection
	agentBinary string
	configPath  string
	port        int
	nodename    string
	cookie      string
	dbPath      string

	// Ergo node for communicating with the test plugin's TestController
	ergoNode       gen.Node
	pluginNodeName gen.Atom

	// Cloud state mirror — tracks all PutCloudState/DeleteCloudState calls
	// so we can re-inject after a crash.
	cloudStateMirror map[string]testcontrol.CloudStateEntry

	// Response sequence accumulator — tracks all ProgramResponses calls
	// so we can re-program after a crash.
	allProgrammedSequences []testcontrol.PluginOpSequence

	// strictMode disables best-effort self-healing in assertion paths so tests
	// fail immediately instead of repairing or retrying through anomalies.
	strictMode bool

	// terminalCommandStates tracks commands once they reach a terminal state so
	// we can assert they never later regress back to a non-terminal state.
	terminalCommandStates map[string]string
}

// NewTestHarness builds the test plugin binary, starts the agent as an external
// subprocess, creates an Ergo node for TestController communication, and waits
// for the plugin to register.
func NewTestHarness(t *testing.T, timeout time.Duration) *TestHarness {
	t.Helper()

	pluginsDir := t.TempDir()
	buildTestPluginToDir(t, pluginsDir)

	dataDir := t.TempDir()
	port := getFreePort(t)
	dbPath := filepath.Join(dataDir, "formae.db")
	nodename := "agent-bb-" + randomString(10)
	cookie := randomString(32)

	pluginNodeName := gen.Atom(fmt.Sprintf("%s-%s-plugin@%s",
		nodename, strings.ToLower("Test"), "localhost"))

	h := &TestHarness{
		t:              t,
		agentBinary:    formaeBinary,
		agentDataDir:   dataDir,
		pluginsDir:     pluginsDir,
		port:           port,
		nodename:       nodename,
		cookie:         cookie,
		dbPath:         dbPath,
		pluginNodeName: pluginNodeName,
	}

	h.cloudStateMirror = make(map[string]testcontrol.CloudStateEntry)
	h.terminalCommandStates = make(map[string]string)

	h.configPath = h.writePKLConfig(t)
	h.startAgent(t, timeout, true)
	return h
}

// SetStrictMode toggles strict assertion behavior. In strict mode the harness
// avoids auto-repair in end-state validation paths and fails on the first
// anomaly instead of attempting cleanup/retry.
func (h *TestHarness) SetStrictMode(strict bool) {
	h.strictMode = strict
}

func isTerminalCommandState(state string) bool {
	return state == "Success" || state == "Failed" || state == "Canceled"
}

// ObserveCommandState records terminal command states and asserts they never
// later regress back to a non-terminal state or change terminal outcome.
func (h *TestHarness) ObserveCommandState(t *testing.T, commandID, state string) {
	t.Helper()
	if commandID == "" || state == "" {
		return
	}
	if prev, ok := h.terminalCommandStates[commandID]; ok {
		require.Equal(t, prev, state, "terminal command state changed for %s", commandID)
		return
	}
	if isTerminalCommandState(state) {
		h.terminalCommandStates[commandID] = state
	}
}

// Cleanup stops the Ergo node and the agent subprocess.
func (h *TestHarness) Cleanup() {
	if h.ergoNode != nil {
		h.ergoNode.Stop()
	}
	h.stopAgent()
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
			h.ObserveCommandState(h.t, cmd.CommandID, cmd.State)
			if cmd.State == "Success" || cmd.State == "Failed" || cmd.State == "Canceled" {
				return &cmd, true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, false
}

func (h *TestHarness) latestSyncCommand() (*apimodel.Command, error) {
	statusResp, err := h.client.GetFormaCommandsStatus("", "", 20)
	if err != nil {
		return nil, err
	}
	if statusResp == nil || len(statusResp.Commands) == 0 {
		return nil, nil
	}
	for _, cmd := range statusResp.Commands {
		if cmd.Command == "sync" {
			copy := cmd
			return &copy, nil
		}
	}
	return nil, nil
}

// WaitForNextSyncCommand waits for a new sync command created after the current
// latest sync command, then waits for that command to reach a terminal state.
// Returns nil,false if no new sync command appears within the timeout.
func (h *TestHarness) WaitForNextSyncCommand(timeout time.Duration) (*apimodel.Command, bool) {
	before, err := h.latestSyncCommand()
	if err != nil {
		return nil, false
	}
	beforeID := ""
	if before != nil {
		beforeID = before.CommandID
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		current, err := h.latestSyncCommand()
		if err == nil && current != nil && current.CommandID != "" && current.CommandID != beforeID {
			return h.waitForCommand(current.CommandID, time.Until(deadline))
		}
		time.Sleep(50 * time.Millisecond)
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

// TryGetCloudStateSnapshot is like GetCloudStateSnapshot but returns an error
// instead of failing the test. Used when the agent's actor system may have
// crashed (e.g. supervisor restart intensity exceeded) and the Ergo route is
// stale.
func (h *TestHarness) TryGetCloudStateSnapshot() (map[string]testcontrol.CloudStateEntry, error) {
	resp, err := h.callTestController(testcontrol.GetCloudStateSnapshotRequest{})
	if err != nil {
		return nil, err
	}

	snapshot, ok := resp.(testcontrol.GetCloudStateSnapshotResponse)
	if !ok {
		return nil, fmt.Errorf("unexpected response type: %T", resp)
	}
	return snapshot.Entries, nil
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

// TryGetOperationLog is like GetOperationLog but returns an error instead of
// failing the test. Used in recovery paths where the plugin control route may
// be temporarily stale after crashes or restart-intensity failures.
func (h *TestHarness) TryGetOperationLog() ([]testcontrol.OperationLogEntry, error) {
	resp, err := h.callTestController(testcontrol.GetOperationLogRequest{})
	if err != nil {
		return nil, err
	}
	opLog, ok := resp.(testcontrol.GetOperationLogResponse)
	if !ok {
		return nil, fmt.Errorf("unexpected response type: %T", resp)
	}
	return opLog.Entries, nil
}

// ProgramResponses sends drawn response sequences to the test plugin's TestController.
func (h *TestHarness) ProgramResponses(t *testing.T, sequences []testcontrol.PluginOpSequence) {
	t.Helper()
	_, err := h.callTestController(testcontrol.ProgramResponsesRequest{
		Sequences: sequences,
	})
	require.NoError(t, err, "ProgramResponses failed")

	if sequences == nil {
		// nil clears everything — also clear the accumulator
		h.allProgrammedSequences = nil
	} else {
		h.allProgrammedSequences = append(h.allProgrammedSequences, sequences...)
	}
}

// UnprogramResponses removes previously programmed response sequences.
// Used to roll back responses when a command is rejected after programming.
func (h *TestHarness) UnprogramResponses(t *testing.T, sequences []testcontrol.PluginOpSequence) {
	t.Helper()
	_, err := h.callTestController(testcontrol.UnprogramResponsesRequest{
		Sequences: sequences,
	})
	require.NoError(t, err, "UnprogramResponses failed")
	h.removeProgrammedSequences(sequences)
}

func (h *TestHarness) removeProgrammedSequences(sequences []testcontrol.PluginOpSequence) {
	if len(sequences) == 0 || len(h.allProgrammedSequences) == 0 {
		return
	}
	remaining := make([]testcontrol.PluginOpSequence, 0, len(h.allProgrammedSequences))
	for _, programmed := range h.allProgrammedSequences {
		remove := false
		for _, seq := range sequences {
			if reflect.DeepEqual(programmed, seq) {
				remove = true
				break
			}
		}
		if !remove {
			remaining = append(remaining, programmed)
		}
	}
	h.allProgrammedSequences = remaining
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

	h.cloudStateMirror[nativeID] = testcontrol.CloudStateEntry{
		NativeID:     nativeID,
		ResourceType: resourceType,
		Properties:   properties,
	}
}

// DeleteCloudState deletes a cloud state entry from the test plugin.
func (h *TestHarness) DeleteCloudState(t *testing.T, nativeID string) {
	t.Helper()

	_, err := h.callTestController(testcontrol.DeleteCloudStateRequest{
		NativeID: nativeID,
	})
	require.NoError(t, err, "DeleteCloudState failed")

	delete(h.cloudStateMirror, nativeID)
}

func (h *TestHarness) TryPutCloudState(nativeID, resourceType, properties string) error {
	_, err := h.callTestController(testcontrol.PutCloudStateRequest{
		NativeID:     nativeID,
		ResourceType: resourceType,
		Properties:   properties,
	})
	if err != nil {
		return err
	}
	h.cloudStateMirror[nativeID] = testcontrol.CloudStateEntry{NativeID: nativeID, ResourceType: resourceType, Properties: properties}
	return nil
}

func (h *TestHarness) TryDeleteCloudState(nativeID string) error {
	_, err := h.callTestController(testcontrol.DeleteCloudStateRequest{NativeID: nativeID})
	if err != nil {
		return err
	}
	delete(h.cloudStateMirror, nativeID)
	return nil
}

// OpenGate asks the test plugin's TestController to open the plugin gate,
// unblocking all CRUD operations.
func (h *TestHarness) OpenGate(t *testing.T) {
	t.Helper()
	_, err := h.callTestController(testcontrol.OpenGateRequest{})
	require.NoError(t, err, "OpenGate failed")
}

// SetNativeIDCounter sets the plugin's native ID counter to prevent ID
// collisions after a crash+restart. The counter resets to 0 when the plugin
// restarts, so existing native IDs would be reused.
func (h *TestHarness) SetNativeIDCounter(t *testing.T, value int64) {
	t.Helper()
	_, err := h.callTestController(testcontrol.SetNativeIDCounterRequest{Value: value})
	require.NoError(t, err, "SetNativeIDCounter failed")
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

// KillAgent sends SIGKILL to the agent process, killing it and its child
// plugin immediately. Waits for the process to be reaped.
func (h *TestHarness) KillAgent(t *testing.T) {
	t.Helper()
	require.NotNil(t, h.agentCmd, "agent not running")
	require.NotNil(t, h.agentCmd.Process, "agent process not started")

	pid := h.agentCmd.Process.Pid
	t.Logf("Killing agent (pid %d)", pid)

	err := syscall.Kill(pid, syscall.SIGKILL)
	require.NoError(t, err, "SIGKILL failed")

	// Reap the process
	_ = h.agentCmd.Wait()

	// Stop harness Ergo node (the remote plugin node is gone)
	if h.ergoNode != nil {
		h.ergoNode.Stop()
		h.ergoNode = nil
	}

	// Close the log file handle
	if h.agentLogFile != nil {
		h.agentLogFile.Close()
		h.agentLogFile = nil
	}

	h.agentCmd = nil
}

// RestartAgent starts a new agent subprocess with the same config (same SQLite DB).
// Reconstructs the plugin's cloud state from the agent's inventory and the OOB
// mirror, re-programs response sequences, then opens the gate.
func (h *TestHarness) RestartAgent(t *testing.T, timeout time.Duration, model ...*StateModel) {
	t.Helper()

	// Start the agent but DON'T open the gate yet — ReRunIncompleteCommands
	// fires during startup and would execute CRUD ops before cloud state is
	// reconstructed. The gate keeps those ops blocked until we're ready.
	h.startAgent(t, timeout, false)

	// Reconstruct cloud state from the agent's inventory. The agent's SQLite
	// DB survives the crash, so it knows which resources should exist. The
	// plugin's in-memory CloudState was lost, so we re-inject everything.
	//
	// IMPORTANT: ExtractResources returns properties with $res wrapper objects
	// (e.g. {"$res":true,"$label":"...","$value":"parent-name"}) but the cloud
	// plugin expects flat/plain values (e.g. "parent-name"). We must flatten
	// resolvables before re-injection, otherwise the plugin returns $res objects
	// on Read, which corrupts the $ref.$value in mergeRefsPreservingUserRefs.
	forma, err := h.client.ExtractResources("managed:true")
	pendingManagedProps := map[string]string{}
	pendingManagedDeletes := map[string]bool{}
	if len(model) > 0 && model[0] != nil {
		pendingManagedProps, pendingManagedDeletes = model[0].PendingManagedDriftCloudState()
	}
	if err == nil && forma != nil {
		for _, res := range forma.Resources {
			if res.NativeID == "" {
				continue
			}
			if pendingManagedDeletes[res.NativeID] {
				continue
			}
			flatProps := flattenPropertiesForCloud(res.Properties)
			if driftProps, ok := pendingManagedProps[res.NativeID]; ok {
				flatProps = driftProps
			}
			_, err := h.callTestController(testcontrol.PutCloudStateRequest{
				NativeID:     res.NativeID,
				ResourceType: res.Type,
				Properties:   flatProps,
			})
			if err != nil {
				t.Logf("warning: failed to re-inject cloud state for %s: %v", res.NativeID, err)
			}
		}
	}

	// Re-inject OOB cloud state from the mirror (resources not in inventory).
	for _, entry := range h.cloudStateMirror {
		if _, ok := pendingManagedProps[entry.NativeID]; ok {
			continue
		}
		if pendingManagedDeletes[entry.NativeID] {
			continue
		}
		_, err := h.callTestController(testcontrol.PutCloudStateRequest{
			NativeID:     entry.NativeID,
			ResourceType: entry.ResourceType,
			Properties:   entry.Properties,
		})
		if err != nil {
			t.Logf("warning: failed to re-inject OOB cloud state for %s: %v", entry.NativeID, err)
		}
	}

	// Set the plugin's native ID counter past all existing IDs to prevent
	// collisions. The counter resets to 0 on restart, so without this, new
	// creates would reuse IDs like test-1, test-2, etc.
	var maxID int64
	if forma != nil {
		for _, res := range forma.Resources {
			if id := parseNativeIDNum(res.NativeID); id > maxID {
				maxID = id
			}
		}
	}
	for _, entry := range h.cloudStateMirror {
		if id := parseNativeIDNum(entry.NativeID); id > maxID {
			maxID = id
		}
	}
	if maxID > 0 {
		h.SetNativeIDCounter(t, maxID)
	}

	// Re-program all response sequences (bypass ProgramResponses to avoid
	// double-accumulating).
	if len(h.allProgrammedSequences) > 0 {
		_, err := h.callTestController(testcontrol.ProgramResponsesRequest{
			Sequences: h.allProgrammedSequences,
		})
		require.NoError(t, err, "re-inject response sequences failed")
	}

	// Dump ReRunIncompleteCommands diagnostics from agent log before opening
	// the gate (the re-run happens during startup, before gate opens).
	h.dumpAgentLogLines(t, "ReRunIncompleteCommands")

	// Now that cloud state and response sequences are ready, open the gate
	// so the re-run commands can proceed with correct state.
	h.OpenGate(t)
}

// dumpAgentLogLines reads the agent log and prints lines matching the given substring.
func (h *TestHarness) dumpAgentLogLines(t *testing.T, substr string) {
	t.Helper()
	logPath := filepath.Join(h.agentDataDir, "agent.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, substr) {
			t.Logf("  [agent] %s", line)
		}
	}
}

// --- internal helpers ---

// parseNativeIDNum extracts the numeric suffix from a test plugin native ID
// (format "test-N"). Returns 0 if the format doesn't match.
func parseNativeIDNum(nativeID string) int64 {
	var id int64
	if _, err := fmt.Sscanf(nativeID, "test-%d", &id); err != nil {
		return 0
	}
	return id
}

// randomString generates a random alphanumeric string of length n.
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.IntN(len(letters))]
	}
	return string(b)
}

// writePKLConfig generates a PKL configuration file for the agent subprocess.
func (h *TestHarness) writePKLConfig(t *testing.T) string {
	t.Helper()
	configPath := filepath.Join(h.agentDataDir, "formae.conf.pkl")
	content := fmt.Sprintf(`amends "formae:/Config.pkl"

agent {
    server {
        port = %d
        nodename = %q
        secret = %q
    }
    datastore {
        datastoreType = "sqlite"
        sqlite {
            filePath = %q
        }
    }
    synchronization {
        enabled = false
    }
    discovery {
        enabled = false
    }
    retry {
        maxRetries = 2
        retryDelay = 250.ms
        statusCheckInterval = 1.s
    }
}

plugins {
    pluginDir = %q
}
`, h.port, h.nodename, h.cookie, h.dbPath, h.pluginsDir)
	err := os.WriteFile(configPath, []byte(content), 0644)
	require.NoError(t, err)
	return configPath
}

// startAgent launches the formae agent as a subprocess, waits for health and
// plugin registration, and starts the Ergo node. If openGate is true, it also
// opens the plugin gate immediately. For crash recovery, the caller should pass
// false and open the gate manually after reconstructing cloud state.
func (h *TestHarness) startAgent(t *testing.T, timeout time.Duration, openGate bool) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	cmd := exec.CommandContext(ctx, h.agentBinary, "agent", "start", "--config", h.configPath)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("FORMAE_PID_FILE=%s", filepath.Join(h.agentDataDir, "formae.pid")))

	logFile, err := os.OpenFile(filepath.Join(h.agentDataDir, "agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	require.NoError(t, err)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	h.agentLogFile = logFile

	err = cmd.Start()
	require.NoError(t, err, "Failed to start agent")
	h.agentCmd = cmd

	h.client = api.NewClient(pkgmodel.APIConfig{
		URL:  "http://localhost",
		Port: h.port,
	}, nil, nil)

	h.waitForHealth(t, timeout)
	h.waitForPlugin(t, timeout)
	h.startErgoNode(t)
	if openGate {
		h.OpenGate(t)
	}
}

// waitForHealth polls the agent health endpoint until it responds with HTTP 200.
func (h *TestHarness) waitForHealth(t *testing.T, timeout time.Duration) {
	t.Helper()
	healthURL := fmt.Sprintf("http://localhost:%d/api/v1/health", h.port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			t.Logf("Agent health check passed")
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Dump agent log on failure
	if data, err := os.ReadFile(filepath.Join(h.agentDataDir, "agent.log")); err == nil {
		t.Logf("=== Agent log ===\n%s\n=== End ===", string(data))
	}
	t.Fatalf("agent health check failed after %v", timeout)
}

// waitForPlugin polls the agent stats endpoint until at least one plugin is registered.
func (h *TestHarness) waitForPlugin(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		stats, err := h.client.Stats()
		if err == nil && stats != nil && len(stats.Plugins) > 0 {
			t.Logf("Plugin registered: %v", stats.Plugins)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Dump agent log on failure
	if data, err := os.ReadFile(filepath.Join(h.agentDataDir, "agent.log")); err == nil {
		t.Logf("=== Agent log ===\n%s\n=== End ===", string(data))
	}
	t.Fatalf("plugin did not register within %v", timeout)
}

// stopAgent sends SIGTERM to the agent subprocess, waits for graceful shutdown,
// and falls back to context cancellation if needed.
func (h *TestHarness) stopAgent() {
	if h.agentCmd == nil || h.agentCmd.Process == nil {
		return
	}
	_ = h.agentCmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- h.agentCmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		h.cancel()
		<-done
	}
	if h.agentLogFile != nil {
		h.agentLogFile.Close()
	}
	if h.t.Failed() {
		logPath := filepath.Join(h.agentDataDir, "agent.log")
		if data, err := os.ReadFile(logPath); err == nil {
			h.t.Logf("=== Agent log ===\n%s\n=== End ===", string(data))
		}
	}
}

// startErgoNode creates an Ergo node for the test harness. It uses the same
// network cookie as the agent so it can communicate with the test plugin's node.
func (h *TestHarness) startErgoNode(t *testing.T) {
	t.Helper()

	// Register testcontrol EDF types so Ergo can serialize our messages
	err := plugin.RegisterSharedEDFTypes()
	require.NoError(t, err, "RegisterSharedEDFTypes failed")
	err = testcontrol.RegisterEDFTypes()
	require.NoError(t, err, "RegisterEDFTypes failed")

	nodeName := gen.Atom(fmt.Sprintf("test-harness-%s@localhost", randomString(8)))

	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	options.Network.Cookie = h.cookie
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

// flattenPropertiesForCloud strips $ref and $res wrapper objects from properties,
// replacing them with their $value. This is needed when re-injecting cloud state
// after a restart because ExtractResources returns $res format but the plugin
// expects flat values.
func flattenPropertiesForCloud(raw json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return string(raw)
	}
	flattenResolvables(m)
	out, err := json.Marshal(m)
	if err != nil {
		return string(raw)
	}
	return string(out)
}
