// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin-conformance-tests/testutil"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// CreatedResourceInfo tracks a resource that was created during multi-resource test setup
type CreatedResourceInfo struct {
	ResourceType string
	Label        string
	NativeID     string
	Properties   json.RawMessage // Properties from the plugin after creation
}

// resolvablePath tracks a resolvable reference found in resource properties
type resolvablePath struct {
	path       string
	label      string
	resType    string
	property   string
	fullObject gjson.Result
}

// TestHarness manages the lifecycle of formae agent and CLI commands for testing
type TestHarness struct {
	t             *testing.T
	formaeBinary  string
	agentCmd      *exec.Cmd
	agentCtx      context.Context
	agentCancel   context.CancelFunc
	cleanupFuncs  []func()
	agentStarted  bool
	tempDir       string
	configFile    string
	logFile       string
	networkCookie string // Network cookie for distributed plugin communication
	testRunID     string // Unique ID for this test run, used by PKL files for resource naming
	pluginManager *plugin.Manager

	// Ergo actor system for direct plugin communication (discovery tests)
	ergoNode          gen.Node
	ergoNodeName      gen.Atom
	testHelperPID     gen.PID
	pluginCoordinator gen.PID
	ergoNodeStarted   bool

	// Plugin info for cleanup operations
	lastPluginBinaryPath string
	lastPluginNamespace  string
}

// NewTestHarness creates a new test harness instance.
// The formae binary location is determined by:
// 1. FORMAE_BINARY env var (for external plugin tests)
// 2. Default path relative to repo root (for internal tests)
func NewTestHarness(t *testing.T) *TestHarness {
	// Check for FORMAE_BINARY env var first
	formaeBinary := os.Getenv("FORMAE_BINARY")
	if formaeBinary == "" {
		// Fall back to default path relative to repo root
		// When running with `go test -C`, we need to go up to repo root
		binPath := filepath.Join("..", "..", "..", "formae")
		absPath, err := filepath.Abs(binPath)
		if err != nil {
			t.Fatalf("failed to get absolute path for formae binary: %v", err)
		}
		formaeBinary = absPath
	}

	if _, err := os.Stat(formaeBinary); os.IsNotExist(err) {
		t.Fatalf("formae binary not found at %s. Set FORMAE_BINARY env var or run 'make build' first", formaeBinary)
	}

	ctx, cancel := context.WithCancel(context.Background())

	h := &TestHarness{
		t:            t,
		formaeBinary: formaeBinary,
		agentCtx:     ctx,
		agentCancel:  cancel,
		cleanupFuncs: []func(){},
		agentStarted: false,
	}

	// Set up test environment (temp dir and config)
	if err := h.setupTestEnvironment(); err != nil {
		t.Fatalf("failed to setup test environment: %v", err)
	}

	// Initialize plugin manager
	if err := h.setupPluginManager(); err != nil {
		t.Fatalf("failed to setup plugin manager: %v", err)
	}

	return h
}

// setupTestEnvironment creates a temporary directory and config file for testing
func (h *TestHarness) setupTestEnvironment() error {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "formae-test-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	h.tempDir = tempDir

	// Register cleanup to remove temp directory
	h.RegisterCleanup(func() {
		h.t.Logf("Cleaning up temp directory: %s", tempDir)
		_ = os.RemoveAll(tempDir)
	})

	// Create database path in temp directory
	dbPath := filepath.Join(tempDir, "formae-test.db")

	// Create log file path in temp directory
	logPath := filepath.Join(tempDir, "formae-test.log")

	// Generate a random network cookie for distributed plugin communication
	cookieBytes := make([]byte, 16)
	if _, err := rand.Read(cookieBytes); err != nil {
		return fmt.Errorf("failed to generate network cookie: %w", err)
	}
	h.networkCookie = hex.EncodeToString(cookieBytes)

	// Generate a unique test run ID for resource naming consistency within this test run
	testRunIDBytes := make([]byte, 4)
	if _, err := rand.Read(testRunIDBytes); err != nil {
		return fmt.Errorf("failed to generate test run ID: %w", err)
	}
	h.testRunID = hex.EncodeToString(testRunIDBytes)

	// Generate test config file
	configContent := fmt.Sprintf(`/*
 * © 2025 Platform Engineering Labs Inc.
 *
 * SPDX-License-Identifier: FSL-1.1-ALv2
 */

// Auto-generated test configuration
amends "formae:/Config.pkl"

agent {
    server {
        secret = %q
    }
    datastore {
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
    logging {
        consoleLogLevel = "debug"
        filePath = %q
        fileLogLevel = "debug"
    }
}

cli {
	disableUsageReporting = true
}

plugins {
	pluginDir = "~/.pel/formae/plugins"
}
`, h.networkCookie, dbPath, logPath)

	// Write config to temp directory
	configFile := filepath.Join(tempDir, "test-config.pkl")
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	h.configFile = configFile
	h.logFile = logPath

	h.t.Logf("Created test environment: tempDir=%s, configFile=%s, dbPath=%s, logPath=%s", tempDir, configFile, dbPath, logPath)
	return nil
}

// setupPluginManager initializes the plugin manager and loads plugins
func (h *TestHarness) setupPluginManager() error {
	// Find plugin directory similar to how we find the formae binary
	// Plugins should be at ../../../plugins relative to the test directory
	pluginPath := filepath.Join("..", "..", "..", "plugins")
	absPluginPath, err := filepath.Abs(pluginPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for plugins: %w", err)
	}

	h.t.Logf("Loading plugins from: %s", absPluginPath)

	// Create plugin manager with the plugin path
	h.pluginManager = plugin.NewManager(ExpandHomePath("~/.pel/formae/plugins"), absPluginPath)

	// Load all plugins
	h.pluginManager.Load()

	h.t.Logf("Plugin manager initialized successfully")
	return nil
}

// InitErgoNode initializes the Ergo actor system for direct plugin communication.
// This is required for discovery tests to communicate with external plugins.
func (h *TestHarness) InitErgoNode() error {
	if h.ergoNodeStarted {
		return nil // Already initialized
	}

	h.t.Log("Initializing Ergo node for direct plugin communication")

	// Register EDF types for network serialization before starting node
	if err := plugin.RegisterSharedEDFTypes(); err != nil {
		return fmt.Errorf("failed to register EDF types: %w", err)
	}

	// Generate unique node name for this test run
	h.ergoNodeName = gen.Atom(fmt.Sprintf("test-harness-%s@localhost", h.testRunID))

	// Setup Ergo node options
	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	options.Network.Cookie = h.networkCookie
	options.Log.Level = gen.LogLevelWarning

	// Start the Ergo node
	node, err := ergo.StartNode(h.ergoNodeName, options)
	if err != nil {
		return fmt.Errorf("failed to start Ergo node: %w", err)
	}
	h.ergoNode = node

	// Spawn TestHelperActor for bridging test code with actor system
	messages := make(chan any, 100)
	testHelperPID, err := testutil.StartTestHelperActor(h.ergoNode, messages)
	if err != nil {
		h.ergoNode.Stop()
		return fmt.Errorf("failed to spawn TestHelperActor: %w", err)
	}
	h.testHelperPID = testHelperPID

	// Spawn PluginCoordinator to receive plugin announcements
	// Pass the node name and network cookie so it can spawn plugins
	coordinatorPID, err := h.ergoNode.SpawnRegister(
		"PluginCoordinator",
		NewPluginCoordinator,
		gen.ProcessOptions{},
		string(h.ergoNodeName), // agentNodeName
		h.networkCookie,        // networkCookie
	)
	if err != nil {
		h.ergoNode.Stop()
		return fmt.Errorf("failed to spawn PluginCoordinator: %w", err)
	}
	h.pluginCoordinator = coordinatorPID

	h.ergoNodeStarted = true
	h.t.Logf("Ergo node initialized: %s", h.ergoNodeName)

	return nil
}

// StopErgoNode stops the Ergo actor system
func (h *TestHarness) StopErgoNode() {
	if h.ergoNodeStarted && h.ergoNode != nil {
		// First, terminate any spawned plugin processes
		h.t.Log("Terminating spawned plugins before stopping Ergo node")
		result, err := testutil.CallWithTimeout(h.ergoNode, "PluginCoordinator", TerminatePluginsRequest{}, 10)
		if err != nil {
			h.t.Logf("Warning: failed to terminate plugins: %v", err)
		} else if r, ok := result.(TerminatePluginsResult); ok {
			h.t.Logf("Terminated %d plugin(s)", r.Terminated)
		}

		// Give plugins a moment to shut down
		time.Sleep(500 * time.Millisecond)

		h.t.Log("Stopping Ergo node")
		h.ergoNode.Stop()
		h.ergoNodeStarted = false
	}
}

// LaunchPluginDirect spawns an external plugin process and waits for it to announce itself.
// This is used by discovery tests to create resources without the formae agent.
func (h *TestHarness) LaunchPluginDirect(pluginBinaryPath, namespace string) error {
	if !h.ergoNodeStarted {
		if err := h.InitErgoNode(); err != nil {
			return fmt.Errorf("failed to initialize Ergo node: %w", err)
		}
	}

	h.t.Logf("Launching plugin directly: %s (namespace: %s)", pluginBinaryPath, namespace)

	// Call the PluginCoordinator to launch the plugin via actor message
	// This is required because SpawnMeta can only be called from within an actor
	result, err := testutil.Call(h.ergoNode, "PluginCoordinator", LaunchPluginRequest{
		PluginBinaryPath: pluginBinaryPath,
		Namespace:        namespace,
		TestRunID:        h.testRunID,
	})
	if err != nil {
		return fmt.Errorf("failed to call PluginCoordinator: %w", err)
	}

	if launchResult, ok := result.(LaunchPluginResult); ok {
		if launchResult.Error != "" {
			return fmt.Errorf("failed to launch plugin: %s", launchResult.Error)
		}
	}

	h.t.Logf("Plugin process spawned, waiting for announcement...")
	return nil
}

// WaitForPluginReady waits for a plugin to announce itself to the PluginCoordinator.
func (h *TestHarness) WaitForPluginReady(namespace string, timeout time.Duration) error {
	if !h.ergoNodeStarted {
		return fmt.Errorf("Ergo node not started")
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		result, err := testutil.Call(h.ergoNode, "PluginCoordinator", CheckPluginAvailable{Namespace: namespace})
		if err == nil {
			if r, ok := result.(CheckPluginAvailableResult); ok && r.Available {
				h.t.Logf("Plugin %s is ready", namespace)
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for plugin %s to become ready", namespace)
}

// RegisterTargetForDiscovery registers a target with Discoverable=true via the formae agent API.
// This must be called after the agent is started.
func (h *TestHarness) RegisterTargetForDiscovery(evalOutput json.RawMessage) error {
	// Parse the eval output to get the forma structure
	var forma pkgmodel.Forma
	if err := json.Unmarshal(evalOutput, &forma); err != nil {
		return fmt.Errorf("failed to parse eval output: %w", err)
	}

	if len(forma.Targets) == 0 {
		return fmt.Errorf("no targets found in eval output")
	}

	// Set Discoverable=true on the target
	discoverableTarget := forma.Targets[0]
	discoverableTarget.Discoverable = true
	h.t.Logf("Registering target with Discoverable=true: %s", discoverableTarget.Label)

	targetOnlyForma := pkgmodel.Forma{
		Targets: []pkgmodel.Target{discoverableTarget},
	}

	targetJSON, err := json.Marshal(targetOnlyForma)
	if err != nil {
		return fmt.Errorf("failed to marshal target-only forma: %w", err)
	}

	_, err = h.submitForma(targetJSON, "target-registration")
	if err != nil {
		return fmt.Errorf("failed to register target for discovery: %w", err)
	}

	return nil
}

// waitForOperationProgress polls the PluginCoordinator for operation progress until complete.
// This is used to wait for async operations (create/delete) to finish.
func (h *TestHarness) waitForOperationProgress(operatorPID gen.PID, initialProgress resource.ProgressResult, timeout time.Duration) (resource.ProgressResult, error) {
	deadline := time.Now().Add(timeout)
	pollInterval := 5 * time.Second

	progress := initialProgress

	for time.Now().Before(deadline) {
		if progress.OperationStatus != resource.OperationStatusInProgress {
			return progress, nil
		}

		time.Sleep(pollInterval)

		// Poll the PluginCoordinator for the latest progress
		result, err := testutil.Call(h.ergoNode, "PluginCoordinator", GetLatestProgressRequest{
			OperatorPID: operatorPID,
		})
		if err != nil {
			h.t.Logf("Warning: failed to get progress: %v", err)
			continue
		}

		progressResult, ok := result.(GetLatestProgressResult)
		if !ok {
			h.t.Logf("Warning: unexpected response type: %T", result)
			continue
		}

		if progressResult.Found {
			h.t.Logf("Progress update: status=%s, nativeID=%s", progressResult.Progress.OperationStatus, progressResult.Progress.NativeID)
			progress = progressResult.Progress
		} else {
			h.t.Logf("No progress update yet, continuing to wait...")
		}
	}

	return progress, fmt.Errorf("timeout waiting for operation to complete")
}

// ConfigureDiscovery updates the config file

// GetPluginBinaryPath returns the path to an external plugin binary.
// It uses the plugin manager to find installed plugins.
func (h *TestHarness) GetPluginBinaryPath(namespace string) (string, error) {
	if h.pluginManager == nil {
		return "", fmt.Errorf("plugin manager not initialized")
	}

	// Look up in external resource plugins
	for _, p := range h.pluginManager.ListExternalResourcePlugins() {
		if strings.EqualFold(p.Namespace, namespace) {
			h.t.Logf("Found plugin binary for %s: %s", namespace, p.BinaryPath)
			return p.BinaryPath, nil
		}
	}

	return "", fmt.Errorf("no plugin binary found for namespace: %s", namespace)
}

// ConfigureDiscovery updates the config file to enable discovery for specific resource types
// This must be called before StartAgent() to take effect
func (h *TestHarness) ConfigureDiscovery(resourceTypes []string) error {
	if h.agentStarted {
		return fmt.Errorf("cannot configure discovery after agent has started")
	}

	h.t.Logf("Configuring discovery for resource types: %v", resourceTypes)

	// Create database path in temp directory
	dbPath := filepath.Join(h.tempDir, "formae-test.db")

	// Build the resourceTypesToDiscover listing
	resourceTypesList := ""
	for _, rt := range resourceTypes {
		resourceTypesList += fmt.Sprintf("        %q\n", rt)
	}

	// Generate updated config file with discovery enabled
	configContent := fmt.Sprintf(`/*
 * © 2025 Platform Engineering Labs Inc.
 *
 * SPDX-License-Identifier: FSL-1.1-ALv2
 */

// Auto-generated test configuration
amends "formae:/Config.pkl"

agent {
    server {
        secret = %q
    }
    datastore {
        sqlite {
            filePath = %q
        }
    }
    synchronization {
        enabled = false
    }
    discovery {
        enabled = true
        resourceTypesToDiscover {
%s        }
    }
    logging {
        consoleLogLevel = "debug"
        filePath = %q
        fileLogLevel = "debug"
    }
}

cli {
	disableUsageReporting = true
}
`, h.networkCookie, dbPath, resourceTypesList, h.logFile)

	// Overwrite the config file
	if err := os.WriteFile(h.configFile, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated config file: %w", err)
	}

	h.t.Log("Discovery configuration updated successfully")
	return nil
}

// setCommandEnv sets the required environment variables for a command
// This ensures the FORMAE_TEST_RUN_ID is available to all CLI commands
func (h *TestHarness) setCommandEnv(cmd *exec.Cmd) {
	cmd.Env = append(os.Environ(), fmt.Sprintf("FORMAE_TEST_RUN_ID=%s", h.testRunID))
}

// StartAgent starts the formae agent in the background
func (h *TestHarness) StartAgent() error {
	if h.agentStarted {
		return fmt.Errorf("agent already started")
	}

	// Clean up any stale PID file first
	// Note: agent uses /tmp/formae.pid (see internal/agent/agent.go:24)
	pidFile := "/tmp/formae.pid"
	_ = os.Remove(pidFile)

	h.t.Log("Starting formae agent...")

	// Start agent with context and custom config so we can cancel it
	h.agentCmd = exec.CommandContext(h.agentCtx, h.formaeBinary, "agent", "start", "--config", h.configFile)
	h.setCommandEnv(h.agentCmd)
	h.agentCmd.Stdout = os.Stdout
	h.agentCmd.Stderr = os.Stderr

	if err := h.agentCmd.Start(); err != nil {
		return fmt.Errorf("failed to start agent: %w", err)
	}

	h.agentStarted = true

	// Wait for agent to be ready by polling health endpoint
	if err := h.waitForAgent(); err != nil {
		h.StopAgent()
		return err
	}

	h.t.Log("Formae agent started and ready")
	return nil
}

// waitForAgent polls the health endpoint until the agent is ready
func (h *TestHarness) waitForAgent() error {
	// Default port from plugins/pkl/assets/formae/Config.pkl:20
	healthURL := "http://localhost:49684/api/v1/health"
	timeout := 30 * time.Second
	pollInterval := 500 * time.Millisecond
	deadline := time.Now().Add(timeout)

	h.t.Log("Waiting for agent to be ready...")

	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return nil
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for agent to become ready after %v", timeout)
}

// StopAgent stops the formae agent
func (h *TestHarness) StopAgent() {
	if !h.agentStarted {
		return
	}

	h.t.Log("Stopping formae agent...")
	h.agentCancel()

	// Wait for process to exit
	if h.agentCmd != nil && h.agentCmd.Process != nil {
		_ = h.agentCmd.Wait()
	}

	h.agentStarted = false
	h.t.Log("Formae agent stopped")
}

// Cleanup runs all registered cleanup functions and stops the agent
func (h *TestHarness) Cleanup() {
	// Run cleanup functions in reverse order
	for i := len(h.cleanupFuncs) - 1; i >= 0; i-- {
		h.cleanupFuncs[i]()
	}

	h.StopAgent()
	h.StopErgoNode()
}

// RegisterCleanup adds a cleanup function to be called during Cleanup()
func (h *TestHarness) RegisterCleanup(f func()) {
	h.cleanupFuncs = append(h.cleanupFuncs, f)
}

// GetTempDir returns the temporary directory path used for test files
func (h *TestHarness) GetTempDir() string {
	return h.tempDir
}

// Apply runs `formae apply` with the given PKL file in reconcile mode and returns the command ID
func (h *TestHarness) Apply(pklFile string) (string, error) {
	return h.ApplyWithMode(pklFile, "reconcile")
}

// ApplyWithMode runs `formae apply` with the given PKL file and mode, and returns the command ID
func (h *TestHarness) ApplyWithMode(pklFile string, mode string) (string, error) {
	h.t.Logf("Running formae apply with %s (mode: %s)", pklFile, mode)

	cmd := exec.Command(
		h.formaeBinary,
		"apply",
		pklFile,
		"--config", h.configFile,
		"--mode", mode,
		"--output-consumer", "machine",
		"--output-schema", "json",
	)
	h.setCommandEnv(cmd)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("apply command failed: %w\nOutput: %s", err, string(output))
	}

	// Parse JSON response
	var response model.SubmitCommandResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return "", fmt.Errorf("failed to parse apply response: %w\nOutput: %s", err, string(output))
	}

	if response.CommandID == "" {
		return "", fmt.Errorf("no command ID in response: %s", string(output))
	}

	h.t.Logf("Apply command submitted, CommandID: %s", response.CommandID)
	return response.CommandID, nil
}

// Destroy runs `formae destroy` with the given PKL file and returns the command ID
func (h *TestHarness) Destroy(pklFile string) (string, error) {
	h.t.Logf("Running formae destroy with %s", pklFile)

	cmd := exec.Command(
		h.formaeBinary,
		"destroy",
		pklFile,
		"--config", h.configFile,
		"--output-consumer", "machine",
		"--output-schema", "json",
	)
	h.setCommandEnv(cmd)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("destroy command failed: %w\nOutput: %s", err, string(output))
	}

	// Parse JSON response
	var response model.SubmitCommandResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return "", fmt.Errorf("failed to parse destroy response: %w\nOutput: %s", err, string(output))
	}

	if response.CommandID == "" {
		return "", fmt.Errorf("no command ID in response: %s", string(output))
	}

	h.t.Logf("Destroy command submitted, CommandID: %s", response.CommandID)
	return response.CommandID, nil
}

// Eval runs `formae eval` with the given PKL file and returns the JSON output
func (h *TestHarness) Eval(pklFile string) (string, error) {
	h.t.Logf("Running formae eval with %s", pklFile)

	cmd := exec.Command(
		h.formaeBinary,
		"eval",
		pklFile,
		"--config", h.configFile,
		"--output-consumer", "machine",
		"--output-schema", "json",
	)
	h.setCommandEnv(cmd)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("eval command failed: %w\nOutput: %s", err, string(output))
	}

	return string(output), nil
}

// Extract runs `formae extract` with the given query and output file
func (h *TestHarness) Extract(query string, outputFile string) error {
	h.t.Logf("Running formae extract with query '%s' to %s", query, outputFile)

	cmd := exec.Command(
		h.formaeBinary,
		"extract",
		"--config", h.configFile,
		"--query", query,
		outputFile,
	)
	h.setCommandEnv(cmd)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("extract command failed: %w\nOutput: %s", err, string(output))
	}

	h.t.Logf("Extract completed successfully")
	return nil
}

// InventoryResponse represents the JSON response from formae inventory
type InventoryResponse struct {
	Targets   []map[string]any `json:"Targets,omitempty"`
	Resources []map[string]any `json:"Resources,omitempty"`
}

// Inventory runs `formae inventory` with the given query and returns the parsed response
func (h *TestHarness) Inventory(query string) (*InventoryResponse, error) {
	h.t.Logf("Running formae inventory with query: %s", query)

	cmd := exec.Command(
		h.formaeBinary,
		"inventory",
		"resources", // Need to specify the subcommand
		"--config", h.configFile,
		"--query", query,
		"--output-consumer", "machine",
		"--output-schema", "json",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("inventory command failed: %w\nOutput: %s", err, string(output))
	}

	// Parse JSON response
	var response InventoryResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("failed to parse inventory response: %w\nOutput: %s", err, string(output))
	}

	h.t.Logf("Inventory returned %d resource(s)", len(response.Resources))
	return &response, nil
}

// Sync triggers a synchronization operation via the admin endpoint
func (h *TestHarness) Sync() error {
	h.t.Log("Triggering synchronization via /api/v1/admin/synchronize")

	// Default port from plugins/pkl/assets/formae/Config.pkl:20
	syncURL := "http://localhost:49684/api/v1/admin/synchronize"

	resp, err := http.Post(syncURL, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to trigger sync: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sync endpoint returned status %d", resp.StatusCode)
	}

	h.t.Log("Synchronization triggered successfully")
	return nil
}

// WaitForSyncCompletion waits for the synchronization to complete by polling the agent log file
func (h *TestHarness) WaitForSyncCompletion(timeout time.Duration) error {
	h.t.Log("Waiting for synchronization to complete...")

	deadline := time.Now().Add(timeout)
	pollInterval := 500 * time.Millisecond
	syncMessage := "Synchronization finished"

	for time.Now().Before(deadline) {
		// Read the log file
		logContent, err := os.ReadFile(h.logFile)
		if err != nil {
			// File might not exist yet, continue polling
			time.Sleep(pollInterval)
			continue
		}

		if strings.Contains(string(logContent), syncMessage) {
			h.t.Log("Synchronization completed")
			return nil
		}
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for synchronization to complete after %v", timeout)
}

// GetStats fetches the agent stats via the /api/v1/stats endpoint
func (h *TestHarness) GetStats() (*model.Stats, error) {
	statsURL := "http://localhost:49684/api/v1/stats"

	resp, err := http.Get(statsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stats endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read stats response: %w", err)
	}

	var stats model.Stats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("failed to parse stats response: %w", err)
	}

	return &stats, nil
}

// WaitForPluginRegistered polls the /stats endpoint until the specified
// plugin namespace appears in the registered plugins list.
// This is used to ensure a plugin is fully registered before triggering discovery.
func (h *TestHarness) WaitForPluginRegistered(namespace string, timeout time.Duration) error {
	h.t.Logf("Waiting for plugin %s to be registered...", namespace)

	deadline := time.Now().Add(timeout)
	pollInterval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		stats, err := h.GetStats()
		if err != nil {
			// Agent might not be fully ready yet, keep polling
			time.Sleep(pollInterval)
			continue
		}

		for _, plugin := range stats.Plugins {
			if strings.EqualFold(plugin.Namespace, namespace) {
				h.t.Logf("Plugin %s registered successfully", namespace)
				return nil
			}
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for plugin %s to register", namespace)
}

// TriggerDiscovery triggers a discovery operation via the admin endpoint
func (h *TestHarness) TriggerDiscovery() error {
	h.t.Log("Triggering discovery via /api/v1/admin/discover")

	// Default port from plugins/pkl/assets/formae/Config.pkl:20
	discoverURL := "http://localhost:49684/api/v1/admin/discover"

	resp, err := http.Post(discoverURL, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to trigger discovery: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discover endpoint returned status %d", resp.StatusCode)
	}

	h.t.Log("Discovery triggered successfully")
	return nil
}

// WaitForDiscoveryCompletion waits for the discovery to complete by polling the agent log file
// Deprecated: Use WaitForResourceInInventory instead for reliable waiting in CI environments.
// Log file polling is unreliable due to buffering differences between environments.
func (h *TestHarness) WaitForDiscoveryCompletion(timeout time.Duration) error {
	h.t.Log("Waiting for discovery to complete...")

	deadline := time.Now().Add(timeout)
	pollInterval := 500 * time.Millisecond
	discoveryMessage := "Discovery finished."

	for time.Now().Before(deadline) {
		// Read the log file
		logContent, err := os.ReadFile(h.logFile)
		if err != nil {
			// File might not exist yet, continue polling
			time.Sleep(pollInterval)
			continue
		}

		if strings.Contains(string(logContent), discoveryMessage) {
			h.t.Log("Discovery completed")
			return nil
		}
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for discovery to complete after %v", timeout)
}

// WaitForResourceInInventory polls the inventory API until a resource with the specified
// type and nativeID appears. This is more reliable than log file polling because it
// directly queries the data store rather than depending on log file buffering.
// This is the recommended way to wait for discovery completion in tests.
func (h *TestHarness) WaitForResourceInInventory(resourceType, nativeID string, managed bool, timeout time.Duration) error {
	managedStr := "true"
	if !managed {
		managedStr = "false"
	}
	h.t.Logf("Waiting for resource to appear in inventory: type=%s, nativeID=%s, managed=%s", resourceType, nativeID, managedStr)

	deadline := time.Now().Add(timeout)
	pollInterval := 2 * time.Second

	for time.Now().Before(deadline) {
		// Query inventory for this specific resource type and managed status
		inventory, err := h.Inventory(fmt.Sprintf("type: %s managed: %s", resourceType, managedStr))
		if err != nil {
			// Inventory query might fail early on, continue polling
			h.t.Logf("Inventory query failed (will retry): %v", err)
			time.Sleep(pollInterval)
			continue
		}

		// Check if our resource is in the results
		for _, res := range inventory.Resources {
			if resNativeID, ok := res["NativeID"].(string); ok {
				if resNativeID == nativeID {
					h.t.Logf("Resource found in inventory: NativeID=%s", nativeID)
					return nil
				}
			}
		}

		h.t.Logf("Resource not yet in inventory (found %d resources of type %s), polling...", len(inventory.Resources), resourceType)
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for resource %s (type: %s) to appear in inventory after %v", nativeID, resourceType, timeout)
}

// CreateUnmanagedResource creates unmanaged resources using the external plugin directly,
// bypassing formae's validation and database storage. This method handles multi-resource
// formas by creating all cloud resources in dependency order and resolving references
// between them. Returns the NativeID of the LAST (main) resource for discovery verification.
// Use CreateAllUnmanagedResources if you need info about all created resources.
func (h *TestHarness) CreateUnmanagedResource(evaluatedJSON string) (string, error) {
	createdResources, err := h.CreateAllUnmanagedResources(evaluatedJSON)
	if err != nil {
		return "", err
	}

	if len(createdResources) == 0 {
		return "", fmt.Errorf("no resources were created")
	}

	// Return the last resource's NativeID (the main resource being tested)
	return createdResources[len(createdResources)-1].NativeID, nil
}

// CreateAllUnmanagedResources creates ALL cloud resources in a forma using the external plugin
// directly, handling dependencies and resolvable references between resources.
// Resources are created in dependency order (resources without resolvables first).
// Returns a slice of CreatedResourceInfo for all created resources (for cleanup).
func (h *TestHarness) CreateAllUnmanagedResources(evaluatedJSON string) ([]CreatedResourceInfo, error) {
	h.t.Log("Creating unmanaged resources via external plugin")

	// Parse the evaluated JSON into a Forma object
	var forma pkgmodel.Forma
	if err := json.Unmarshal([]byte(evaluatedJSON), &forma); err != nil {
		return nil, fmt.Errorf("failed to parse forma JSON: %w", err)
	}

	// Collect all cloud resources (those with "::" in the type)
	var cloudResources []*pkgmodel.Resource
	for i := range forma.Resources {
		if strings.Contains(forma.Resources[i].Type, "::") {
			// Make a copy to avoid modifying the original
			res := forma.Resources[i]
			cloudResources = append(cloudResources, &res)
		}
	}

	if len(cloudResources) == 0 {
		return nil, fmt.Errorf("no cloud resources found in forma")
	}

	h.t.Logf("Found %d cloud resource(s) to create", len(cloudResources))

	// Sort resources by dependency order (resources without resolvables first)
	sortedResources := h.sortResourcesByDependency(cloudResources)

	// Extract namespace from the first resource type (all resources should be in same namespace)
	namespace := strings.Split(sortedResources[0].Type, "::")[0]
	h.t.Logf("Using plugin namespace: %s", namespace)

	// Get plugin binary path
	pluginBinaryPath, err := h.GetPluginBinaryPath(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get plugin binary path: %w", err)
	}

	// Store for later cleanup
	h.lastPluginBinaryPath = pluginBinaryPath
	h.lastPluginNamespace = namespace

	// Initialize Ergo node if needed
	if err := h.InitErgoNode(); err != nil {
		return nil, fmt.Errorf("failed to initialize Ergo node: %w", err)
	}

	// Launch the plugin
	if err := h.LaunchPluginDirect(pluginBinaryPath, namespace); err != nil {
		return nil, fmt.Errorf("failed to launch plugin: %w", err)
	}

	// Wait for plugin to be ready
	if err := h.WaitForPluginReady(namespace, 30*time.Second); err != nil {
		return nil, fmt.Errorf("plugin not ready: %w", err)
	}

	// Find the target
	if len(forma.Targets) == 0 {
		return nil, fmt.Errorf("no targets found in forma")
	}
	target := forma.Targets[0]

	// Create each resource in dependency order, tracking what was created
	var createdResources []CreatedResourceInfo

	for _, res := range sortedResources {
		h.t.Logf("Creating resource: type=%s, label=%s", res.Type, res.Label)

		// Strip Formae metadata tags from the resource properties
		h.stripFormaeTags(res)

		// Resolve any resolvable references using previously created resources
		resolvedProps, err := h.resolveResolvablesInProperties(res.Properties, createdResources)
		if err != nil {
			return createdResources, fmt.Errorf("failed to resolve resolvables for %s: %w", res.Label, err)
		}
		res.Properties = resolvedProps

		// Create the resource
		result, err := testutil.CallWithTimeout(h.ergoNode, "PluginCoordinator", CreateResourceRequest{
			ResourceType: res.Type,
			Namespace:    namespace,
			Label:        res.Label,
			Properties:   res.Properties,
			Target:       target,
		}, 120) // 2 minute timeout for the initial call
		if err != nil {
			return createdResources, fmt.Errorf("CreateResource call failed for %s: %w", res.Label, err)
		}

		createResult, ok := result.(CreateResourceResult)
		if !ok {
			return createdResources, fmt.Errorf("unexpected response type for %s: %T", res.Label, result)
		}

		if createResult.Error != "" {
			return createdResources, fmt.Errorf("create failed for %s: %s", res.Label, createResult.Error)
		}

		// Wait for the operation to complete by polling
		progress := createResult.InitialProgress
		if progress.OperationStatus == resource.OperationStatusInProgress {
			h.t.Logf("Resource %s creation in progress, waiting for completion...", res.Label)
			progress, err = h.waitForOperationProgress(createResult.OperatorPID, progress, 10*time.Minute)
			if err != nil {
				return createdResources, fmt.Errorf("waiting for create to complete for %s: %w", res.Label, err)
			}
		}

		if progress.OperationStatus == resource.OperationStatusFailure {
			return createdResources, fmt.Errorf("create failed for %s: %s (code: %s)", res.Label, progress.StatusMessage, progress.ErrorCode)
		}

		h.t.Logf("Resource %s created successfully, NativeID: %s", res.Label, progress.NativeID)

		// Track the created resource for cleanup and for resolving subsequent resolvables
		createdResources = append(createdResources, CreatedResourceInfo{
			ResourceType: res.Type,
			Label:        res.Label,
			NativeID:     progress.NativeID,
			Properties:   progress.ResourceProperties,
		})
	}

	h.t.Logf("Successfully created %d resource(s)", len(createdResources))
	return createdResources, nil
}

// sortResourcesByDependency performs a topological sort on resources based on their
// resolvable dependencies. Resources are ordered so that dependencies come before
// the resources that depend on them. This handles arbitrary nesting depths.
func (h *TestHarness) sortResourcesByDependency(resources []*pkgmodel.Resource) []*pkgmodel.Resource {
	// Build a map of label -> resource for quick lookup
	byLabel := make(map[string]*pkgmodel.Resource)
	for _, res := range resources {
		byLabel[res.Label] = res
	}

	// Build dependency graph: for each resource, find which other resources it depends on
	// Dependencies are identified by the $label field in resolvable objects
	dependencies := make(map[string][]string) // resource label -> labels of resources it depends on
	for _, res := range resources {
		deps := h.extractDependencyLabels(res.Properties)
		dependencies[res.Label] = deps
	}

	// Perform topological sort using Kahn's algorithm
	// Calculate in-degree (number of dependencies) for each resource
	inDegree := make(map[string]int)
	for _, res := range resources {
		if _, exists := inDegree[res.Label]; !exists {
			inDegree[res.Label] = 0
		}
		for _, depLabel := range dependencies[res.Label] {
			// Only count dependencies that are in our resource set
			if _, exists := byLabel[depLabel]; exists {
				inDegree[res.Label]++
			}
		}
	}

	// Build reverse dependency map: which resources depend on each resource
	dependents := make(map[string][]string)
	for _, res := range resources {
		for _, depLabel := range dependencies[res.Label] {
			if _, exists := byLabel[depLabel]; exists {
				dependents[depLabel] = append(dependents[depLabel], res.Label)
			}
		}
	}

	// Start with resources that have no dependencies (in-degree 0)
	var queue []string
	for _, res := range resources {
		if inDegree[res.Label] == 0 {
			queue = append(queue, res.Label)
		}
	}

	// Process queue, adding resources to result as their dependencies are satisfied
	var sortedLabels []string
	for len(queue) > 0 {
		// Pop from queue
		label := queue[0]
		queue = queue[1:]
		sortedLabels = append(sortedLabels, label)

		// Decrease in-degree for all resources that depend on this one
		for _, dependentLabel := range dependents[label] {
			inDegree[dependentLabel]--
			if inDegree[dependentLabel] == 0 {
				queue = append(queue, dependentLabel)
			}
		}
	}

	// Check for cycles (if we didn't process all resources, there's a cycle)
	if len(sortedLabels) != len(resources) {
		h.t.Logf("Warning: dependency cycle detected, falling back to original order")
		return resources
	}

	// Convert sorted labels back to resources
	result := make([]*pkgmodel.Resource, 0, len(resources))
	for _, label := range sortedLabels {
		result = append(result, byLabel[label])
	}

	return result
}

// extractDependencyLabels finds all resource labels that a resource depends on
// by examining the $label fields in resolvable objects within its properties.
func (h *TestHarness) extractDependencyLabels(properties json.RawMessage) []string {
	if len(properties) == 0 {
		return nil
	}

	var resolvables []resolvablePath
	result := gjson.ParseBytes(properties)
	h.findResolvablesRecursive("", result, &resolvables)

	// Extract unique labels
	labelSet := make(map[string]struct{})
	for _, res := range resolvables {
		if res.label != "" {
			labelSet[res.label] = struct{}{}
		}
	}

	labels := make([]string, 0, len(labelSet))
	for label := range labelSet {
		labels = append(labels, label)
	}
	return labels
}

// resolveResolvablesInProperties replaces resolvable references with actual values
// from previously created resources.
func (h *TestHarness) resolveResolvablesInProperties(properties json.RawMessage, createdResources []CreatedResourceInfo) (json.RawMessage, error) {
	if len(properties) == 0 {
		return properties, nil
	}

	propsStr := string(properties)
	result := gjson.Parse(propsStr)

	// Find all resolvables in the properties
	var resolvables []resolvablePath
	h.findResolvablesRecursive("", result, &resolvables)

	if len(resolvables) == 0 {
		return properties, nil
	}

	// Build a map of created resources by label and type for quick lookup
	createdByKey := make(map[string]CreatedResourceInfo)
	for _, created := range createdResources {
		key := created.Label + "::" + created.ResourceType
		createdByKey[key] = created
	}

	// Resolve each resolvable
	for _, res := range resolvables {
		// Look up the created resource by label and type
		key := res.label + "::" + res.resType
		created, found := createdByKey[key]
		if !found {
			h.t.Logf("Warning: could not find created resource for resolvable: label=%s, type=%s", res.label, res.resType)
			continue
		}

		// Extract the property value from the created resource's properties
		resolvedValue, err := h.extractPropertyValue(created.Properties, res.property)
		if err != nil {
			h.t.Logf("Warning: could not extract property %s from created resource %s: %v", res.property, res.label, err)
			continue
		}

		h.t.Logf("Resolved %s.%s -> %s", res.label, res.property, resolvedValue)

		// Replace the resolvable object with the resolved value in the properties JSON
		propsStr, err = sjson.Set(propsStr, res.path, resolvedValue)
		if err != nil {
			return properties, fmt.Errorf("failed to set resolved value at path %s: %w", res.path, err)
		}
	}

	return json.RawMessage(propsStr), nil
}

// findResolvablesRecursive recursively finds all resolvable objects in a JSON structure
func (h *TestHarness) findResolvablesRecursive(basePath string, value gjson.Result, resolvables *[]resolvablePath) {
	if value.IsObject() {
		// Check if this object is a resolvable ($res: true)
		resField := value.Get("$res")
		if resField.Exists() && resField.Bool() {
			*resolvables = append(*resolvables, resolvablePath{
				path:       basePath,
				label:      value.Get("$label").String(),
				resType:    value.Get("$type").String(),
				property:   value.Get("$property").String(),
				fullObject: value,
			})
			return
		}

		// Recurse into object properties
		value.ForEach(func(key, val gjson.Result) bool {
			var newPath string
			if basePath == "" {
				newPath = key.String()
			} else {
				newPath = basePath + "." + key.String()
			}
			h.findResolvablesRecursive(newPath, val, resolvables)
			return true
		})
	} else if value.IsArray() {
		// Recurse into array elements
		idx := 0
		value.ForEach(func(_, val gjson.Result) bool {
			var newPath string
			if basePath == "" {
				newPath = fmt.Sprintf("%d", idx)
			} else {
				newPath = fmt.Sprintf("%s.%d", basePath, idx)
			}
			h.findResolvablesRecursive(newPath, val, resolvables)
			idx++
			return true
		})
	}
}

// extractPropertyValue extracts a property value from resource properties JSON.
// The property path can be a simple name like "Id" or a nested path like "Arn".
func (h *TestHarness) extractPropertyValue(properties json.RawMessage, propertyPath string) (string, error) {
	if len(properties) == 0 {
		return "", fmt.Errorf("empty properties")
	}

	result := gjson.GetBytes(properties, propertyPath)
	if !result.Exists() {
		// Try case-insensitive search for common property names
		// AWS often returns Id but we might be looking for id
		propsStr := string(properties)
		parsed := gjson.Parse(propsStr)

		var foundValue string
		parsed.ForEach(func(key, val gjson.Result) bool {
			if strings.EqualFold(key.String(), propertyPath) {
				foundValue = val.String()
				return false // stop iteration
			}
			return true
		})

		if foundValue != "" {
			return foundValue, nil
		}

		return "", fmt.Errorf("property %s not found in resource properties", propertyPath)
	}

	return result.String(), nil
}

// DeleteUnmanagedResource deletes a resource directly via the external plugin, bypassing formae.
// This method handles the full workflow: initializing the Ergo node, launching the plugin,
// waiting for it to be ready, and deleting the resource via the actor system.
// Uses the plugin binary path and namespace stored from the last CreateUnmanagedResource call.
func (h *TestHarness) DeleteUnmanagedResource(resourceType, nativeID string, target *pkgmodel.Target) error {
	h.t.Logf("Deleting unmanaged resource via external plugin: type=%s, nativeID=%s", resourceType, nativeID)

	// Extract namespace from resource type (e.g., "AWS::S3::Bucket" -> "AWS")
	namespace := strings.Split(resourceType, "::")[0]
	h.t.Logf("Using plugin namespace: %s", namespace)

	// Use stored plugin binary path, or try to look it up
	pluginBinaryPath := h.lastPluginBinaryPath
	if pluginBinaryPath == "" {
		var err error
		pluginBinaryPath, err = h.GetPluginBinaryPath(namespace)
		if err != nil {
			return fmt.Errorf("failed to get plugin binary path: %w", err)
		}
	}

	// Initialize Ergo node if needed
	if err := h.InitErgoNode(); err != nil {
		return fmt.Errorf("failed to initialize Ergo node: %w", err)
	}

	// Launch the plugin
	if err := h.LaunchPluginDirect(pluginBinaryPath, namespace); err != nil {
		return fmt.Errorf("failed to launch plugin: %w", err)
	}

	// Wait for plugin to be ready
	if err := h.WaitForPluginReady(namespace, 30*time.Second); err != nil {
		return fmt.Errorf("plugin not ready: %w", err)
	}

	// Call PluginCoordinator to delete the resource
	result, err := testutil.CallWithTimeout(h.ergoNode, "PluginCoordinator", DeleteResourceRequest{
		ResourceType: resourceType,
		Namespace:    namespace,
		NativeID:     nativeID,
		Target:       *target,
	}, 120) // 2 minute timeout for the initial call
	if err != nil {
		return fmt.Errorf("DeleteResource call failed: %w", err)
	}

	deleteResult, ok := result.(DeleteResourceResult)
	if !ok {
		return fmt.Errorf("unexpected response type: %T", result)
	}

	if deleteResult.Error != "" {
		return fmt.Errorf("delete failed: %s", deleteResult.Error)
	}

	// Wait for the operation to complete by polling
	progress := deleteResult.InitialProgress
	if progress.OperationStatus == resource.OperationStatusInProgress {
		h.t.Logf("Resource deletion in progress, waiting for completion...")
		progress, err = h.waitForOperationProgress(deleteResult.OperatorPID, progress, 10*time.Minute)
		if err != nil {
			return fmt.Errorf("waiting for delete to complete: %w", err)
		}
	}

	if progress.OperationStatus == resource.OperationStatusFailure {
		return fmt.Errorf("delete failed: %s (code: %s)", progress.StatusMessage, progress.ErrorCode)
	}

	h.t.Logf("Resource deleted successfully")
	return nil
}

// stripFormaeTags removes FormaeStackLabel and FormaeResourceLabel from resource tags
func (h *TestHarness) stripFormaeTags(resource *pkgmodel.Resource) {
	if len(resource.Properties) == 0 {
		return
	}

	// Parse Properties into a map
	var properties map[string]any
	if err := json.Unmarshal(resource.Properties, &properties); err != nil {
		h.t.Logf("Failed to unmarshal Properties for resource %s: %v", resource.Type, err)
		return
	}

	// Check if Properties contains Tags
	tagsInterface, hasTags := properties["Tags"]
	if !hasTags {
		return
	}

	// Tags could be an array of tag objects with Key/Value fields
	tags, ok := tagsInterface.([]any)
	if !ok {
		h.t.Logf("Tags field is not an array, skipping tag stripping for resource %s", resource.Type)
		return
	}

	// Filter out Formae metadata tags
	filteredTags := make([]any, 0, len(tags))
	for _, tagInterface := range tags {
		tag, ok := tagInterface.(map[string]any)
		if !ok {
			// Keep non-map tags as-is
			filteredTags = append(filteredTags, tagInterface)
			continue
		}

		key, hasKey := tag["Key"].(string)
		if !hasKey {
			// Keep tags without a Key field
			filteredTags = append(filteredTags, tagInterface)
			continue
		}

		// Skip Formae metadata tags
		if key == "FormaeStackLabel" || key == "FormaeResourceLabel" {
			h.t.Logf("Stripping tag %s from resource %s", key, resource.Type)
			continue
		}

		// Keep all other tags
		filteredTags = append(filteredTags, tagInterface)
	}

	// Update properties with filtered tags
	properties["Tags"] = filteredTags

	// Marshal back to Properties
	modifiedProperties, err := json.Marshal(properties)
	if err != nil {
		h.t.Logf("Failed to marshal modified Properties for resource %s: %v", resource.Type, err)
		return
	}

	resource.Properties = modifiedProperties
}

// submitForma is a helper to submit forma JSON to the API
func (h *TestHarness) submitForma(formaJSON []byte, filename string) (string, error) {
	commandsURL := "http://localhost:49684/api/v1/commands"

	// Create multipart writer
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add form fields
	_ = writer.WriteField("command", "apply")
	_ = writer.WriteField("mode", "reconcile")

	// Add file field with forma JSON
	fileWriter, err := writer.CreateFormFile("file", filename+".json")
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := fileWriter.Write(formaJSON); err != nil {
		return "", fmt.Errorf("failed to write forma JSON: %w", err)
	}

	// Close the multipart writer
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create the HTTP request
	req, err := http.NewRequest("POST", commandsURL, body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Client-ID", "plugin-sdk-test")

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response to get command ID
	var submitResponse model.SubmitCommandResponse
	if err := json.Unmarshal(respBody, &submitResponse); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return submitResponse.CommandID, nil
}

// GetResourceDescriptor returns the ResourceDescriptor for a given resource type by querying the plugin
func (h *TestHarness) GetResourceDescriptor(resourceType string) (*plugin.ResourceDescriptor, error) {
	// Extract namespace from resource type (e.g., "AWS::S3::Bucket" -> "aws")
	parts := strings.Split(resourceType, "::")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid resource type format: %s", resourceType)
	}
	namespace := strings.ToLower(parts[0])

	// Get the resource plugin
	resourcePlugin, err := h.pluginManager.ResourcePlugin(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get resource plugin for namespace %s: %w", namespace, err)
	}

	// Get all supported resources from the plugin
	supportedResources := (*resourcePlugin).SupportedResources()

	// Find the descriptor for the requested resource type
	for i := range supportedResources {
		if supportedResources[i].Type == resourceType {
			return &supportedResources[i], nil
		}
	}

	return nil, fmt.Errorf("resource type %s not found in plugin %s", resourceType, namespace)
}

// GetResourceDescriptorFromCoordinator returns the ResourceDescriptor for a given resource type
// by querying the TestPluginCoordinator. This requires the Ergo node to be started and the
// plugin to have announced itself (e.g., after CreateUnmanagedResource has been called).
func (h *TestHarness) GetResourceDescriptorFromCoordinator(resourceType string) (*plugin.ResourceDescriptor, error) {
	if !h.ergoNodeStarted {
		return nil, fmt.Errorf("Ergo node not started - call CreateUnmanagedResource first")
	}

	// Extract namespace from resource type (e.g., "AWS::S3::Bucket" -> "aws")
	parts := strings.Split(resourceType, "::")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid resource type format: %s", resourceType)
	}
	namespace := parts[0] // Keep original case - plugin announces with original case (e.g., "AWS")

	// Query the TestPluginCoordinator for plugin info
	result, err := testutil.Call(h.ergoNode, "PluginCoordinator", GetPluginInfoRequest{
		Namespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query PluginCoordinator: %w", err)
	}

	pluginInfo, ok := result.(GetPluginInfoResult)
	if !ok {
		return nil, fmt.Errorf("unexpected response type from PluginCoordinator: %T", result)
	}
	if !pluginInfo.Found {
		return nil, fmt.Errorf("plugin not found for namespace %s: %s", namespace, pluginInfo.Error)
	}

	// Find the descriptor for the requested resource type
	for i := range pluginInfo.SupportedResources {
		if pluginInfo.SupportedResources[i].Type == resourceType {
			return &pluginInfo.SupportedResources[i], nil
		}
	}

	return nil, fmt.Errorf("resource type %s not found in plugin %s", resourceType, namespace)
}

// PollStatus polls the command status until it reaches a terminal state or times out
func (h *TestHarness) PollStatus(commandID string, timeout time.Duration) (string, error) {
	h.t.Logf("Polling status for command %s", commandID)

	deadline := time.Now().Add(timeout)
	pollInterval := 2 * time.Second

	for time.Now().Before(deadline) {
		status, err := h.GetStatus(commandID)
		if err != nil {
			return "", fmt.Errorf("failed to get status: %w", err)
		}

		h.t.Logf("Command %s state: %s", commandID, status)

		// Check for terminal states
		switch status {
		case "Success", "Completed":
			return status, nil
		case "Failed", "Canceled":
			return status, fmt.Errorf("command reached terminal state: %s", status)
		case "NotStarted", "InProgress", "Pending", "Canceling":
			// Continue polling
			time.Sleep(pollInterval)
		default:
			return status, fmt.Errorf("unknown command state: %s", status)
		}
	}

	return "", fmt.Errorf("timeout waiting for command %s to complete", commandID)
}

// GetStatus gets the current status of a command
func (h *TestHarness) GetStatus(commandID string) (string, error) {
	cmd := exec.Command(
		h.formaeBinary,
		"status",
		"command",
		"--config", h.configFile,
		"--query", fmt.Sprintf("id:%s", commandID),
		"--output-consumer", "machine",
		"--output-schema", "json",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("status command failed: %w\nOutput: %s", err, string(output))
	}

	// Parse JSON response
	var response model.ListCommandStatusResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return "", fmt.Errorf("failed to parse status response: %w\nOutput: %s", err, string(output))
	}

	if len(response.Commands) == 0 {
		return "", fmt.Errorf("no commands in status response")
	}

	return response.Commands[0].State, nil
}
