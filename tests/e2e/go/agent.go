// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	config  AgentConfig
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	logFile *os.File
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

	// Intentionally no pluginDir override here. The agent's default
	// ~/.pel/formae/plugins (empty in CI) plus the multi-source scan
	// of SystemPluginDir(binPath) finds orbital-installed plugins
	// without help. Setting cfg.PluginDir to the same path as the
	// system dir would collide with CleanStaleDevPlugins and delete
	// the only copy of every plugin. setup_pkl.sh still reads
	// FORMAE_PLUGIN_DIR independently of the agent.
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
		cmd:     cmd,
		cancel:  cancel,
		logFile: logFile,
	}

	t.Cleanup(func() { agent.Stop(t) })

	agent.HealthCheck(t, 30*time.Second)

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

