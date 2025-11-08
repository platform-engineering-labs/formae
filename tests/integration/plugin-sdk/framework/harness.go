// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package framework

import (
	"bytes"
	"context"
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

	"github.com/platform-engineering-labs/formae/internal/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

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
	pluginManager *plugin.Manager
}

// NewTestHarness creates a new test harness instance
func NewTestHarness(t *testing.T) *TestHarness {
	// Find the formae binary - should be built already at repo root
	// When running with `go test -C`, we need to go up to repo root
	binPath := filepath.Join("..", "..", "..", "formae")
	absPath, err := filepath.Abs(binPath)
	if err != nil {
		t.Fatalf("failed to get absolute path for formae binary: %v", err)
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		t.Fatalf("formae binary not found at %s. Run 'make build' first", absPath)
	}

	ctx, cancel := context.WithCancel(context.Background())

	h := &TestHarness{
		t:            t,
		formaeBinary: absPath,
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

	// Generate test config file
	configContent := fmt.Sprintf(`/*
 * © 2025 Platform Engineering Labs Inc.
 *
 * SPDX-License-Identifier: FSL-1.1-ALv2
 */

// Auto-generated test configuration
amends "formae:/Config.pkl"

agent {
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
`, dbPath, logPath)

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
	h.pluginManager = plugin.NewManager(absPluginPath)

	// Load all plugins
	h.pluginManager.Load()

	h.t.Logf("Plugin manager initialized successfully")
	return nil
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
`, dbPath, resourceTypesList, h.logFile)

	// Overwrite the config file
	if err := os.WriteFile(h.configFile, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated config file: %w", err)
	}

	h.t.Log("Discovery configuration updated successfully")
	return nil
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
		"--mode", mode,
		"--output-consumer", "machine",
		"--output-schema", "json",
	)

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
		"--output-consumer", "machine",
		"--output-schema", "json",
	)

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
		"--output-consumer", "machine",
		"--output-schema", "json",
	)

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
		"--query", query,
		outputFile,
	)

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

// CreateUnmanagedResource creates an unmanaged resource using the plugin directly,
// bypassing formae's validation and database storage
func (h *TestHarness) CreateUnmanagedResource(evaluatedJSON string) (string, error) {
	h.t.Log("Creating unmanaged resource via plugin")

	// Parse the evaluated JSON into a Forma object
	var forma pkgmodel.Forma
	if err := json.Unmarshal([]byte(evaluatedJSON), &forma); err != nil {
		return "", fmt.Errorf("failed to parse forma JSON: %w", err)
	}

	// Find the cloud resource to create
	var cloudResource *pkgmodel.Resource
	for i := range forma.Resources {
		if strings.Contains(forma.Resources[i].Type, "::") {
			cloudResource = &forma.Resources[i]
			break
		}
	}

	if cloudResource == nil {
		return "", fmt.Errorf("no cloud resource found in forma")
	}

	h.t.Logf("Creating resource of type: %s", cloudResource.Type)

	// Extract namespace from resource type (e.g., "AWS::S3::Bucket" -> "aws")
	namespace := strings.ToLower(strings.Split(cloudResource.Type, "::")[0])
	h.t.Logf("Using plugin namespace: %s", namespace)

	// Get the resource plugin
	resourcePlugin, err := h.pluginManager.ResourcePlugin(namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get resource plugin for namespace %s: %w", namespace, err)
	}

	// Strip Formae metadata tags from the resource properties
	h.stripFormaeTags(cloudResource)

	// Find the target
	if len(forma.Targets) == 0 {
		return "", fmt.Errorf("no targets found in forma")
	}
	target := &forma.Targets[0]

	// Create the resource using the plugin
	createRequest := &resource.CreateRequest{
		Resource: cloudResource,
		Target:   target,
	}

	h.t.Logf("Calling plugin Create method")
	createResult, err := (*resourcePlugin).Create(context.Background(), createRequest)
	if err != nil {
		return "", fmt.Errorf("plugin Create failed: %w", err)
	}

	if createResult.ProgressResult == nil {
		return "", fmt.Errorf("plugin Create returned nil ProgressResult")
	}

	h.t.Logf("Create initiated, RequestID: %s, Status: %s",
		createResult.ProgressResult.RequestID,
		createResult.ProgressResult.OperationStatus)

	// Poll for completion
	nativeID, err := h.pollPluginStatus(
		*resourcePlugin,
		createResult.ProgressResult.RequestID,
		cloudResource.Type,
		target,
	)
	if err != nil {
		return "", fmt.Errorf("failed waiting for resource creation: %w", err)
	}

	h.t.Logf("Resource created successfully, NativeID: %s", nativeID)

	// Now we need to submit the target via API so it gets stored with Discoverable=true
	// We create a minimal forma with just the target
	discoverableTarget := forma.Targets[0]
	discoverableTarget.Discoverable = true
	h.t.Logf("Setting Discoverable=true for target: %s", discoverableTarget.Label)

	targetOnlyForma := pkgmodel.Forma{
		Targets: []pkgmodel.Target{discoverableTarget},
	}

	targetJSON, err := json.Marshal(targetOnlyForma)
	if err != nil {
		return "", fmt.Errorf("failed to marshal target-only forma: %w", err)
	}

	_, err = h.submitForma(targetJSON, "target-registration")
	if err != nil {
		h.t.Logf("Warning: failed to register target for discovery: %v", err)
		// Don't fail completely, as the resource was created successfully
	}

	return nativeID, nil
}

// DeleteUnmanagedResource deletes a resource directly via the plugin, bypassing formae.
// This is useful for cleanup in discovery tests where resources were created outside formae.
func (h *TestHarness) DeleteUnmanagedResource(resourceType, nativeID string, target *pkgmodel.Target) error {
	h.t.Logf("Deleting unmanaged resource via plugin: type=%s, nativeID=%s", resourceType, nativeID)

	// Extract namespace from resource type (e.g., "AWS::S3::Bucket" -> "aws")
	namespace := strings.ToLower(strings.Split(resourceType, "::")[0])
	h.t.Logf("Using plugin namespace: %s", namespace)

	// Get the resource plugin
	resourcePlugin, err := h.pluginManager.ResourcePlugin(namespace)
	if err != nil {
		return fmt.Errorf("failed to get resource plugin for namespace %s: %w", namespace, err)
	}

	// Delete the resource using the plugin
	deleteRequest := &resource.DeleteRequest{
		NativeID:     &nativeID,
		ResourceType: resourceType,
		Target:       target,
	}

	h.t.Logf("Calling plugin Delete method")
	deleteResult, err := (*resourcePlugin).Delete(context.Background(), deleteRequest)
	if err != nil {
		return fmt.Errorf("plugin Delete failed: %w", err)
	}

	if deleteResult.ProgressResult == nil {
		return fmt.Errorf("plugin Delete returned nil ProgressResult")
	}

	h.t.Logf("Delete initiated, RequestID: %s, Status: %s",
		deleteResult.ProgressResult.RequestID,
		deleteResult.ProgressResult.OperationStatus)

	// Poll for completion
	_, err = h.pollPluginStatus(
		*resourcePlugin,
		deleteResult.ProgressResult.RequestID,
		resourceType,
		target,
	)
	if err != nil {
		return fmt.Errorf("failed waiting for resource deletion: %w", err)
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

// pollPluginStatus polls the plugin's Status method until the operation completes
func (h *TestHarness) pollPluginStatus(
	resourcePlugin plugin.ResourcePlugin,
	requestID string,
	resourceType string,
	target *pkgmodel.Target,
) (string, error) {
	h.t.Logf("Polling plugin status for RequestID: %s", requestID)

	deadline := time.Now().Add(2 * time.Minute) // 2 minute timeout
	pollInterval := 2 * time.Second

	for time.Now().Before(deadline) {
		statusRequest := &resource.StatusRequest{
			RequestID:    requestID,
			ResourceType: resourceType,
			Target:       target,
		}

		statusResult, err := resourcePlugin.Status(context.Background(), statusRequest)
		if err != nil {
			return "", fmt.Errorf("plugin Status call failed: %w", err)
		}

		if statusResult.ProgressResult == nil {
			return "", fmt.Errorf("plugin Status returned nil ProgressResult")
		}

		h.t.Logf("Status check - Operation: %s, Status: %s",
			statusResult.ProgressResult.Operation,
			statusResult.ProgressResult.OperationStatus)

		// Check if operation succeeded
		if statusResult.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			h.t.Log("Operation completed successfully")
			return statusResult.ProgressResult.NativeID, nil
		}

		// Check if operation failed
		if statusResult.ProgressResult.OperationStatus == resource.OperationStatusFailure {
			return "", fmt.Errorf("operation failed: %s (error code: %s)",
				statusResult.ProgressResult.StatusMessage,
				statusResult.ProgressResult.ErrorCode)
		}

		// Operation still in progress, wait and retry
		time.Sleep(pollInterval)
	}

	return "", fmt.Errorf("timeout waiting for operation to complete after 2 minutes")
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
