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
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/api/model"
)

// TestHarness manages the lifecycle of formae agent and CLI commands for testing
type TestHarness struct {
	t              *testing.T
	formaeBinary   string
	agentCmd       *exec.Cmd
	agentCtx       context.Context
	agentCancel    context.CancelFunc
	cleanupFuncs   []func()
	agentStarted   bool
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

	return &TestHarness{
		t:            t,
		formaeBinary: absPath,
		agentCtx:     ctx,
		agentCancel:  cancel,
		cleanupFuncs: []func(){},
		agentStarted: false,
	}
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

	// Start agent with context so we can cancel it
	h.agentCmd = exec.CommandContext(h.agentCtx, h.formaeBinary, "agent", "start")
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
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
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
		case "Failed", "Cancelled", "Canceled":
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
