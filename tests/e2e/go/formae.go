// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"
)

// CommandResult holds the parsed result of a formae status command query.
type CommandResult struct {
	State           string
	ResourceUpdates []ResourceUpdate
}

// SimulationResult holds the parsed result of a formae simulate command.
type SimulationResult struct {
	ChangesRequired bool
	ResourceUpdates []ResourceUpdate
}

// Resource represents a resource as returned by formae inventory.
type Resource struct {
	Ksuid              string
	Stack              string
	Label              string
	Type               string
	NativeID           string
	Properties         map[string]any
	ReadOnlyProperties map[string]any
	Managed            bool
}

// ResourceUpdate represents an individual resource update within a command.
type ResourceUpdate struct {
	Label           string
	Type            string
	Operation       string
	State           string
	CreateOnlyPatch json.RawMessage
}

// FormaeCLI wraps the formae binary for CLI command execution.
type FormaeCLI struct {
	binaryPath string
	configPath string
	agentPort  int
}

// NewFormaeCLI creates a new FormaeCLI instance that will execute the formae
// binary at binaryPath with the given config path.
func NewFormaeCLI(binaryPath string, configPath string, agentPort int) *FormaeCLI {
	return &FormaeCLI{
		binaryPath: binaryPath,
		configPath: configPath,
		agentPort:  agentPort,
	}
}

// Apply runs `formae apply` with the given mode and fixture file path.
// Returns the command ID from the JSON response.
func (f *FormaeCLI) Apply(t *testing.T, mode string, fixturePath string, extraArgs ...string) string {
	t.Helper()

	args := []string{
		"apply",
		fixturePath,
		"--config", f.configPath,
		"--mode", mode,
		"--output-consumer", "machine",
		"--output-schema", "json",
	}
	args = append(args, extraArgs...)

	stdout := f.run(t, args...)

	var response struct {
		CommandID string `json:"CommandId"`
	}
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatalf("failed to parse apply response: %v\nstdout: %s", err, string(stdout))
	}
	if response.CommandID == "" {
		t.Fatalf("no CommandId in apply response: %s", string(stdout))
	}

	t.Logf("apply submitted, CommandId: %s", response.CommandID)
	return response.CommandID
}

// Simulate runs `formae apply --simulate` with the given mode and fixture file
// path. Returns the parsed simulation result (synchronous — no command is
// actually queued).
func (f *FormaeCLI) Simulate(t *testing.T, mode string, fixturePath string, extraArgs ...string) SimulationResult {
	t.Helper()

	args := []string{
		"apply",
		fixturePath,
		"--config", f.configPath,
		"--mode", mode,
		"--simulate",
		"--output-consumer", "machine",
		"--output-schema", "json",
	}
	args = append(args, extraArgs...)

	stdout := f.run(t, args...)

	var response struct {
		ChangesRequired bool `json:"ChangesRequired"`
		Command         struct {
			ResourceUpdates []struct {
				ResourceLabel   string          `json:"ResourceLabel"`
				ResourceType    string          `json:"ResourceType"`
				Operation       string          `json:"Operation"`
				State           string          `json:"State"`
				CreateOnlyPatch json.RawMessage `json:"CreateOnlyPatch"`
			} `json:"ResourceUpdates"`
		} `json:"Command"`
	}
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatalf("failed to parse simulate response: %v\nstdout: %s", err, string(stdout))
	}

	updates := make([]ResourceUpdate, len(response.Command.ResourceUpdates))
	for i, ru := range response.Command.ResourceUpdates {
		updates[i] = ResourceUpdate{
			Label:           ru.ResourceLabel,
			Type:            ru.ResourceType,
			Operation:       ru.Operation,
			State:           ru.State,
			CreateOnlyPatch: ru.CreateOnlyPatch,
		}
	}

	t.Logf("simulate: ChangesRequired=%v, %d resource updates", response.ChangesRequired, len(updates))
	return SimulationResult{
		ChangesRequired: response.ChangesRequired,
		ResourceUpdates: updates,
	}
}

// Destroy runs `formae destroy` with the given fixture file path.
// Returns the command ID from the JSON response.
func (f *FormaeCLI) Destroy(t *testing.T, fixturePath string, extraArgs ...string) string {
	t.Helper()

	args := []string{
		"destroy",
		fixturePath,
		"--config", f.configPath,
		"--output-consumer", "machine",
		"--output-schema", "json",
	}
	args = append(args, extraArgs...)

	stdout := f.run(t, args...)

	var response struct {
		CommandID string `json:"CommandId"`
	}
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatalf("failed to parse destroy response: %v\nstdout: %s", err, string(stdout))
	}
	if response.CommandID == "" {
		t.Fatalf("no CommandId in destroy response: %s", string(stdout))
	}

	t.Logf("destroy submitted, CommandId: %s", response.CommandID)
	return response.CommandID
}

// WaitForCommand polls the status of a command until it reaches a terminal
// state (Success or Failed) or the timeout is reached.
func (f *FormaeCLI) WaitForCommand(t *testing.T, commandID string, timeout time.Duration) CommandResult {
	t.Helper()

	deadline := time.Now().Add(timeout)
	pollInterval := 2 * time.Second

	for time.Now().Before(deadline) {
		result := f.StatusCommand(t, commandID)

		switch result.State {
		case "Success", "NoOp":
			return result
		case "Failed", "Canceled":
			return result
		case "NotStarted", "InProgress", "Pending", "Canceling":
			// Continue polling.
			time.Sleep(pollInterval)
		default:
			t.Fatalf("unknown command state: %s", result.State)
		}
	}

	t.Fatalf("timeout waiting for command %s to complete after %v", commandID, timeout)
	return CommandResult{} // unreachable
}

// Inventory runs `formae inventory resources` and returns the parsed resources.
func (f *FormaeCLI) Inventory(t *testing.T, args ...string) []Resource {
	t.Helper()

	cmdArgs := []string{
		"inventory",
		"resources",
		"--config", f.configPath,
		"--output-consumer", "machine",
		"--output-schema", "json",
	}
	cmdArgs = append(cmdArgs, args...)

	stdout := f.run(t, cmdArgs...)

	var response struct {
		Resources []struct {
			Ksuid              string          `json:"Ksuid"`
			Stack              string          `json:"Stack"`
			Label              string          `json:"Label"`
			Type               string          `json:"Type"`
			NativeID           string          `json:"NativeID"`
			Properties         json.RawMessage `json:"Properties"`
			ReadOnlyProperties json.RawMessage `json:"ReadOnlyProperties"`
			Managed            bool            `json:"Managed"`
		} `json:"Resources"`
	}
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatalf("failed to parse inventory response: %v\nstdout: %s", err, string(stdout))
	}

	resources := make([]Resource, len(response.Resources))
	for i, r := range response.Resources {
		resources[i] = Resource{
			Ksuid:    r.Ksuid,
			Stack:    r.Stack,
			Label:    r.Label,
			Type:     r.Type,
			NativeID: r.NativeID,
			Managed:  r.Managed,
		}
		if len(r.Properties) > 0 {
			var props map[string]any
			if err := json.Unmarshal(r.Properties, &props); err != nil {
				t.Fatalf("failed to parse Properties for resource %s: %v", r.Label, err)
			}
			resources[i].Properties = props
		}
		if len(r.ReadOnlyProperties) > 0 {
			var roProps map[string]any
			if err := json.Unmarshal(r.ReadOnlyProperties, &roProps); err != nil {
				t.Fatalf("failed to parse ReadOnlyProperties for resource %s: %v", r.Label, err)
			}
			resources[i].ReadOnlyProperties = roProps
		}
	}

	return resources
}

// ExtractToFile runs `formae extract` with the given query and writes the
// resulting PKL to targetPath. The --yes flag is set to overwrite without
// prompting.
func (f *FormaeCLI) ExtractToFile(t *testing.T, query string, targetPath string) {
	t.Helper()

	args := []string{
		"extract",
		"--config", f.configPath,
		"--query", query,
		"--yes",
		targetPath,
	}

	f.run(t, args...)
}

// StatusCommand queries the status of a specific command by ID.
func (f *FormaeCLI) StatusCommand(t *testing.T, commandID string) CommandResult {
	t.Helper()

	args := []string{
		"status",
		"command",
		"--config", f.configPath,
		"--query", fmt.Sprintf("id:%s", commandID),
		"--output-consumer", "machine",
		"--output-schema", "json",
	}

	stdout := f.run(t, args...)

	var response struct {
		Commands []struct {
			State           string `json:"State"`
			ResourceUpdates []struct {
				ResourceLabel string `json:"ResourceLabel"`
				ResourceType  string `json:"ResourceType"`
				Operation     string `json:"Operation"`
				State         string `json:"State"`
			} `json:"ResourceUpdates"`
		} `json:"Commands"`
	}
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatalf("failed to parse status response: %v\nstdout: %s", err, string(stdout))
	}
	if len(response.Commands) == 0 {
		// Command not found — this happens for no-op applies where the
		// server returned a command ID but no command was persisted
		// (ChangesRequired: false). Treat as a successful no-op.
		t.Logf("no commands in status response for id %s (no-op)", commandID)
		return CommandResult{State: "NoOp"}
	}

	cmd := response.Commands[0]
	updates := make([]ResourceUpdate, len(cmd.ResourceUpdates))
	for i, ru := range cmd.ResourceUpdates {
		updates[i] = ResourceUpdate{
			Label:     ru.ResourceLabel,
			Type:      ru.ResourceType,
			Operation: ru.Operation,
			State:     ru.State,
		}
	}

	return CommandResult{
		State:           cmd.State,
		ResourceUpdates: updates,
	}
}

// ForceDiscover triggers an immediate discovery cycle via the admin API.
func (f *FormaeCLI) ForceDiscover(t *testing.T) {
	t.Helper()
	url := fmt.Sprintf("http://localhost:%d/api/v1/admin/discover", f.agentPort)
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatalf("failed to trigger discovery: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("force discover returned status %d", resp.StatusCode)
	}
}

// ForceSync triggers an immediate synchronization cycle via the admin API.
func (f *FormaeCLI) ForceSync(t *testing.T) {
	t.Helper()
	url := fmt.Sprintf("http://localhost:%d/api/v1/admin/synchronize", f.agentPort)
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatalf("failed to trigger sync: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("force sync returned status %d", resp.StatusCode)
	}
}

// ApplyExpectRejected runs `formae apply` and expects it to fail with a
// reconcile rejection (drift detected). Returns the stderr output for
// optional inspection. Fails the test if the command succeeds.
func (f *FormaeCLI) ApplyExpectRejected(t *testing.T, mode string, fixturePath string, extraArgs ...string) string {
	t.Helper()

	args := []string{
		"apply",
		fixturePath,
		"--config", f.configPath,
		"--mode", mode,
		"--output-consumer", "machine",
		"--output-schema", "json",
	}
	args = append(args, extraArgs...)

	stdout, stderr, err := f.runAllowError(t, args...)
	if err == nil {
		t.Fatalf("expected apply to be rejected, but it succeeded\nstdout: %s", string(stdout))
	}

	t.Logf("apply rejected as expected: %s", stderr)
	return stderr
}

// run executes the formae binary with the given arguments, capturing stdout
// and stderr separately. It returns the raw stdout bytes. On command failure
// it logs stderr and fails the test.
func (f *FormaeCLI) run(t *testing.T, args ...string) []byte {
	t.Helper()

	cmd := exec.Command(f.binaryPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("formae command failed: %v\nargs: %v\nstderr: %s\nstdout: %s",
			err, args, stderr.String(), stdout.String())
	}

	if stderr.Len() > 0 {
		t.Logf("formae stderr (ignored for parsing): %s", stderr.String())
	}

	return stdout.Bytes()
}

// Cancel cancels in-progress commands via the admin API. If no query is
// provided, cancels the most recent in-progress command. Returns the
// canceled command IDs.
func (f *FormaeCLI) Cancel(t *testing.T, query string) []string {
	t.Helper()

	url := fmt.Sprintf("http://localhost:%d/api/v1/commands/cancel", f.agentPort)
	if query != "" {
		url += "?query=" + query
	}

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		t.Fatalf("failed to create cancel request: %v", err)
	}
	req.Header.Set("Client-ID", "e2e-test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to cancel commands: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		t.Log("no in-progress commands found to cancel")
		return nil
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel returned status %d", resp.StatusCode)
	}

	var response struct {
		CommandIDs []string `json:"CommandIds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("failed to parse cancel response: %v", err)
	}

	t.Logf("cancel submitted, CommandIds: %v", response.CommandIDs)
	return response.CommandIDs
}

// DestroyExpectError runs `formae destroy` and expects it to fail (non-zero
// exit code). Returns stderr for assertion. Fails the test if the command
// succeeds. Uses human-readable output because cascade abort logic is only
// implemented in the human-readable path.
func (f *FormaeCLI) DestroyExpectError(t *testing.T, fixturePath string, extraArgs ...string) string {
	t.Helper()

	args := []string{
		"destroy",
		fixturePath,
		"--config", f.configPath,
	}
	args = append(args, extraArgs...)

	stdout, stderr, err := f.runAllowError(t, args...)
	if err == nil {
		t.Fatalf("expected destroy to fail, but it succeeded\nstdout: %s", string(stdout))
	}

	t.Logf("destroy failed as expected: %s", stderr)
	return stderr
}

// ProjectInit runs `formae project init` to generate a new project in the
// given directory. This is a CLI-only command that does not require an agent.
// It does need --plugin-dir to locate locally-staged test plugins for @local
// schema resolution; the value comes from FORMAE_PLUGIN_DIR (set by CI / the
// agent test harness) when present.
func (f *FormaeCLI) ProjectInit(t *testing.T, dir string, includes ...string) {
	t.Helper()

	args := []string{
		"project", "init", dir,
		"--schema", "pkl",
		"--yes",
	}
	if pluginDir := os.Getenv("FORMAE_PLUGIN_DIR"); pluginDir != "" {
		args = append(args, "--plugin-dir", pluginDir)
	}
	for _, inc := range includes {
		args = append(args, "--include", inc)
	}

	cmd := exec.Command(f.binaryPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("formae project init failed: %v\nargs: %v\nstderr: %s\nstdout: %s",
			err, args, stderr.String(), stdout.String())
	}

	if stderr.Len() > 0 {
		t.Logf("project init stderr (ignored): %s", stderr.String())
	}
}

// runAllowError executes the formae binary, returning stdout, stderr, and any
// error. Unlike run, it does not fail the test on non-zero exit.
func (f *FormaeCLI) runAllowError(t *testing.T, args ...string) ([]byte, string, error) {
	t.Helper()

	cmd := exec.Command(f.binaryPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.Bytes(), stderr.String(), err
}
