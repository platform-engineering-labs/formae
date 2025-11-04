// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/api/model"
)

// TestHarness manages the lifecycle of formae agent and CLI commands for testing
type TestHarness struct {
	t            *testing.T
	formaeBinary string
	agentCmd     *exec.Cmd
	agentCtx     context.Context
	agentCancel  context.CancelFunc
	cleanupFuncs []func()
	agentStarted bool
	tempDir      string
	configFile   string
	logFile      string
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

// Apply runs `formae apply` with the given PKL file and returns the command ID
func (h *TestHarness) Apply(pklFile string) (string, error) {
	h.t.Logf("Running formae apply with %s", pklFile)

	cmd := exec.Command(
		h.formaeBinary,
		"apply",
		pklFile,
		"--mode", "reconcile",
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
	defer resp.Body.Close()

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
