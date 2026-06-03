// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// AgentConfig holds the configuration needed to start a formae agent.
type AgentConfig struct {
	BinaryPath string
	ConfigPath string
	DataDir    string
	Port       int
	LogFile    string
}

// Agent represents a running formae agent process.
type Agent struct {
	config       AgentConfig
	cmd          *exec.Cmd
	cancel       context.CancelFunc
	logFile      *os.File
	authUsername string // empty when auth is disabled
	authPassword string
}

// AgentOption configures agent behavior.
type AgentOption func(*agentOptions)

type agentOptions struct {
	discoveryEnabled        bool
	discoveryInterval       string   // PKL duration, e.g. "30.s"
	discoveryResourceTypes  []string // resource types to discover (empty = all)
	extraEnv                []string // additional KEY=VALUE env vars for the agent process
	authEnabled             bool
	authUsername            string
	authPassword            string
	authBcryptHash         string
	resourcePluginsBlock    string   // raw PKL block for agent.resourcePlugins
	pklImports              string   // raw PKL import statements (top-level)
}

// WithDiscovery enables discovery with the given interval (PKL duration format, e.g. "30.s").
func WithDiscovery(interval string, resourceTypes ...string) AgentOption {
	return func(o *agentOptions) {
		o.discoveryEnabled = true
		o.discoveryInterval = interval
		o.discoveryResourceTypes = resourceTypes
	}
}

// WithEnv adds environment variables to the agent process.
func WithEnv(envVars ...string) AgentOption {
	return func(o *agentOptions) {
		o.extraEnv = append(o.extraEnv, envVars...)
	}
}

// WithResourcePlugins adds a raw PKL block for agent.resourcePlugins.
// imports is optional top-level PKL import statements (e.g., `import "plugins:/Sftp.pkl" as Sftp`).
func WithResourcePlugins(imports, pklBlock string) AgentOption {
	return func(o *agentOptions) {
		o.pklImports = imports
		o.resourcePluginsBlock = pklBlock
	}
}

// WithAuth enables HTTP Basic Authentication on the agent.
// The bcryptHash must be a valid bcrypt hash of password.
func WithAuth(username, password, bcryptHash string) AgentOption {
	return func(o *agentOptions) {
		o.authEnabled = true
		o.authUsername = username
		o.authPassword = password
		o.authBcryptHash = bcryptHash
	}
}

// StartAgent creates a temp directory, picks a free port, generates a minimal
// formae config, and starts the agent as a subprocess. It registers a cleanup
// function on the test so that the agent is stopped even if the test fails.
func StartAgent(t *testing.T, binaryPath string, opts ...AgentOption) *Agent {
	t.Helper()

	options := agentOptions{
		discoveryEnabled:  false,
		discoveryInterval: "10.min",
	}
	for _, opt := range opts {
		opt(&options)
	}

	dataDir := t.TempDir()
	port := pickFreePort(t)

	configPath := filepath.Join(dataDir, "formae.conf.pkl")
	dbPath := filepath.Join(dataDir, "formae.db")
	logPath := filepath.Join(dataDir, "formae.log")

	discoveryEnabled := "false"
	if options.discoveryEnabled {
		discoveryEnabled = "true"
	}

	// Build resourceTypesToDiscover listing if types are specified.
	resourceTypesBlock := ""
	if len(options.discoveryResourceTypes) > 0 {
		var lines []string
		for _, rt := range options.discoveryResourceTypes {
			lines = append(lines, fmt.Sprintf("            %q", rt))
		}
		resourceTypesBlock = fmt.Sprintf("\n        resourceTypesToDiscover {\n%s\n        }", strings.Join(lines, "\n"))
	}

	agentAuthBlock := ""
	cliAuthBlock := ""
	if options.authEnabled {
		agentAuthBlock = fmt.Sprintf(`
    auth {
        type = "auth-basic"
        authorizedUsers = new Listing {
            new Mapping {
                ["Username"] = %q
                ["Password"] = %q
            }
        }
    }`, options.authUsername, options.authBcryptHash)
		cliAuthBlock = fmt.Sprintf(`
    auth {
        type = "auth-basic"
        username = %q
        password = %q
    }`, options.authUsername, options.authPassword)
	}

	// Intentionally no pluginDir override. cfg.PluginDir defaults to
	// ~/.pel/formae/plugins (empty in CI) and the multi-source plugin
	// discovery added in the discovery refactor finds orbital-installed
	// plugins via SystemPluginDir(binPath) without help. The CLI's
	// extract / project init paths now query the agent for installed
	// plugin versions instead of scanning local dirs, so a single
	// pluginDir on the CLI box no longer matters.
	pluginDirBlock := ""

	configContent := fmt.Sprintf(`/*
 * Auto-generated e2e test configuration
 */

amends "formae:/Config.pkl"
%s%s
agent {
    server {
        port = %d
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
        enabled = %s
        interval = %s%s
    }
    logging {
        consoleLogLevel = "debug"
        filePath = %q
        fileLogLevel = "debug"
    }%s%s
}

cli {
    api {
        port = %d
    }
    disableUsageReporting = true%s
}
`, options.pklImports, pluginDirBlock, port, dbPath, discoveryEnabled, options.discoveryInterval, resourceTypesBlock, logPath, agentAuthBlock, options.resourcePluginsBlock, port, cliAuthBlock)

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write agent config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binaryPath, "agent", "start", "--config", configPath)

	cmd.Env = os.Environ()

	// Use a unique PID file to allow parallel test execution.
	pidFile := filepath.Join(dataDir, "formae.pid")
	cmd.Env = append(cmd.Env, fmt.Sprintf("FORMAE_PID_FILE=%s", pidFile))

	// Add any extra env vars (e.g. GRAFANA_AUTH for plugin credentials).
	cmd.Env = append(cmd.Env, options.extraEnv...)

	// Log agent output to a file for debugging on failure.
	logFile, err := os.Create(filepath.Join(dataDir, "agent-stdout.log"))
	if err != nil {
		cancel()
		t.Fatalf("failed to create agent log file: %v", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatalf("failed to start agent: %v", err)
	}

	agent := &Agent{
		config: AgentConfig{
			BinaryPath: binaryPath,
			ConfigPath: configPath,
			DataDir:    dataDir,
			Port:       port,
			LogFile:    logPath,
		},
		cmd:          cmd,
		cancel:       cancel,
		logFile:      logFile,
		authUsername: options.authUsername,
		authPassword: options.authPassword,
	}

	t.Cleanup(func() { agent.Stop(t) })

	agent.HealthCheck(t, 30*time.Second)
	agent.waitForExpectedPlugins(t, 30*time.Second)

	return agent
}

// Stop sends SIGTERM to the agent, waits up to 5 seconds for graceful
// shutdown, and sends SIGKILL if the process is still running.
func (a *Agent) Stop(t *testing.T) {
	t.Helper()

	if a.cmd == nil || a.cmd.Process == nil {
		return
	}

	// Send SIGTERM for graceful shutdown.
	if err := a.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process may already be dead, that's fine.
		t.Logf("failed to send SIGTERM to agent: %v", err)
		return
	}

	done := make(chan error, 1)
	go func() {
		done <- a.cmd.Wait()
	}()

	select {
	case <-done:
		// Process exited gracefully.
	case <-time.After(5 * time.Second):
		t.Log("agent did not exit within 5s after SIGTERM, sending SIGKILL")
		a.cancel()
		<-done
	}

	// Close the log file handle now that the process has exited.
	if a.logFile != nil {
		_ = a.logFile.Close()
	}

	// Dump the agent log on test failure for debugging.
	if t.Failed() {
		logPath := filepath.Join(a.config.DataDir, "agent-stdout.log")
		if data, err := os.ReadFile(logPath); err == nil {
			t.Logf("=== Agent stdout/stderr ===\n%s\n=== End agent log ===", string(data))
		}
	}
}

// HealthCheck polls the agent health endpoint until it responds with 200 OK
// or the timeout is reached.
func (a *Agent) HealthCheck(t *testing.T, timeout time.Duration) {
	t.Helper()

	healthURL := fmt.Sprintf("http://localhost:%d/api/v1/health", a.config.Port)
	deadline := time.Now().Add(timeout)
	pollInterval := 1 * time.Second

	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("agent health check failed after %v (url: %s)", timeout, healthURL)
}

// waitForExpectedPlugins blocks until every resource/schema plugin the test
// expects the agent to have installed appears in /api/v1/plugins. The agent's
// health endpoint flips green as soon as the HTTP server is up, but plugin
// discovery (the multi-source scan over SystemPluginDir + cfg.PluginDir)
// runs concurrently and isn't gated by health. Any CLI command that resolves
// plugin metadata through the agent immediately after StartAgent returns is
// racing the first discovery scan.
//
// The expected set is derived from FORMAE_PLUGIN_DIR — the CI workflow points
// this at whichever directory was populated for the test's matrix entry
// (system_plugins or user_plugins). Each top-level subdirectory there is one
// plugin (orbital and `make install` both use this layout). Only directories
// that look like resource/schema plugins (containing v*/schema/pkl/PklProject)
// are waited on; auth plugins live in the same tree but are loaded via the
// agent's separate auth-plugin discovery path and never appear in
// /api/v1/plugins — tests that need them have their own readiness poll
// (see waitForAuthReady in auth_basic_test.go).
//
// When the agent was started with WithAuth, requests carry the configured
// basic-auth credentials. Non-2xx responses (e.g. 503 while the auth plugin
// is still initializing) are treated as transient and retried.
func (a *Agent) waitForExpectedPlugins(t *testing.T, timeout time.Duration) {
	t.Helper()

	pluginDir := os.Getenv("FORMAE_PLUGIN_DIR")
	if pluginDir == "" {
		return
	}
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		// Path doesn't exist or isn't readable — nothing reliably expected.
		return
	}
	expected := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !hasResourcePluginSchema(filepath.Join(pluginDir, e.Name())) {
			// Auth plugin or other non-resource layout — agent won't list
			// it via /api/v1/plugins. Skip.
			continue
		}
		expected = append(expected, e.Name())
	}
	if len(expected) == 0 {
		return
	}

	url := fmt.Sprintf("http://localhost:%d/api/v1/plugins", a.config.Port)
	deadline := time.Now().Add(timeout)

	// Track the most recent state so the timeout error names whatever was
	// actually still missing — not a misleading "[]" if we never got a
	// successful parse (e.g. the agent returned 401/503 every time).
	missing := append([]string(nil), expected...)
	var lastStatus int
	var lastErr error

	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if a.authUsername != "" {
			req.SetBasicAuth(a.authUsername, a.authPassword)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		lastStatus = resp.StatusCode

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var body struct {
			Plugins []struct {
				Name string `json:"name"`
			} `json:"plugins"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if decodeErr != nil {
			lastErr = decodeErr
			time.Sleep(100 * time.Millisecond)
			continue
		}

		installed := make(map[string]struct{}, len(body.Plugins))
		for _, p := range body.Plugins {
			installed[p.Name] = struct{}{}
		}
		missing = missing[:0]
		for _, name := range expected {
			if _, ok := installed[name]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("expected plugins not all discovered within %v: missing=%v expected=%v dir=%s lastStatus=%d lastErr=%v",
		timeout, missing, expected, pluginDir, lastStatus, lastErr)
}

// hasResourcePluginSchema reports whether nameDir (a top-level entry under
// FORMAE_PLUGIN_DIR) contains a v<version>/schema/pkl/PklProject — the marker
// the agent itself uses to identify a resource/schema plugin (see
// plugin_manager.DiscoverLocalPaths). Auth-only plugin layouts ship a binary
// without a PklProject and are filtered out by this check.
func hasResourcePluginSchema(nameDir string) bool {
	versions, err := os.ReadDir(nameDir)
	if err != nil {
		return false
	}
	for _, v := range versions {
		if !v.IsDir() || !strings.HasPrefix(v.Name(), "v") {
			continue
		}
		pkl := filepath.Join(nameDir, v.Name(), "schema", "pkl", "PklProject")
		if _, err := os.Stat(pkl); err == nil {
			return true
		}
	}
	return false
}

// Port returns the HTTP port the agent is listening on.
func (a *Agent) Port() int {
	return a.config.Port
}

// ConfigPath returns the path to the agent's configuration file.
func (a *Agent) ConfigPath() string {
	return a.config.ConfigPath
}

// LogFile returns the path to the agent's log file.
func (a *Agent) LogFile() string {
	return a.config.LogFile
}

// StdoutLogFile returns the path to the agent's stdout/stderr capture.
func (a *Agent) StdoutLogFile() string {
	return filepath.Join(a.config.DataDir, "agent-stdout.log")
}

// pickFreePort asks the OS for a free TCP port by listening on :0 and
// immediately closing the listener.
func pickFreePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

