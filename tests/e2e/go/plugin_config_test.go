// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPluginConfig verifies that per-plugin configuration flows through the
// full path: PKL config → agent translation → supervisor env var → SDK
// Configure() call on the plugin binary.
//
// It bundles the SFTP plugin (installed by make install-external-plugins)
// and configures it with a custom rate limit override and plugin-specific
// fields (defaultTimeoutSeconds, maxConcurrentConnections). The test verifies:
//
// 1. The SFTP plugin appears in /api/v1/stats with the overridden rate limit
// 2. The agent log contains the SFTP plugin's Configure() output showing
//    the custom config values were received
func TestPluginConfig(t *testing.T) {
	bin := FormaeBinary(t)

	resourcePluginsBlock := `
    resourcePlugins {
        new {
            type = "sftp"
            rateLimit { maxRequestsPerSecond = 3 }
            defaultTimeoutSeconds = 60
            maxConcurrentConnections = 10
        }
    }`

	agent := StartAgent(t, bin, WithResourcePlugins(resourcePluginsBlock))
	baseURL := fmt.Sprintf("http://localhost:%d", agent.Port())

	// Wait for the agent to be ready
	waitForAgent(t, baseURL, 30*time.Second)

	// Give the SFTP plugin time to start and announce
	time.Sleep(3 * time.Second)

	t.Run("SFTP plugin registered with overridden rate limit", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/v1/stats")
		if err != nil {
			t.Fatalf("stats request failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		var stats struct {
			Plugins []struct {
				Namespace            string `json:"Namespace"`
				MaxRequestsPerSecond int    `json:"MaxRequestsPerSecond"`
			} `json:"Plugins"`
		}
		if err := json.Unmarshal(body, &stats); err != nil {
			t.Fatalf("failed to parse stats: %v", err)
		}

		var found bool
		for _, p := range stats.Plugins {
			if p.Namespace == "SFTP" {
				found = true
				if p.MaxRequestsPerSecond != 3 {
					t.Errorf("expected SFTP rate limit 3, got %d", p.MaxRequestsPerSecond)
				}
				break
			}
		}
		if !found {
			t.Errorf("SFTP plugin not found in stats. Plugins: %+v", stats.Plugins)
		}
	})

	t.Run("SFTP plugin received custom config via Configure()", func(t *testing.T) {
		// The SFTP plugin prints "SFTP plugin configured: defaultTimeoutSeconds=60 maxConcurrentConnections=10"
		// to stdout when Configure() is called. The supervisor captures plugin stdout and
		// logs it to the agent's log file. Check both the agent log and stdout capture.
		var allLogs string
		if content, err := os.ReadFile(agent.LogFile()); err == nil {
			allLogs += string(content)
		}
		if content, err := os.ReadFile(agent.StdoutLogFile()); err == nil {
			allLogs += string(content)
		}

		if !strings.Contains(allLogs, "defaultTimeoutSeconds=60") {
			t.Errorf("logs do not contain Configure() output with defaultTimeoutSeconds=60.\nLogs:\n%s", allLogs)
		}
		if !strings.Contains(allLogs, "maxConcurrentConnections=10") {
			t.Errorf("logs do not contain Configure() output with maxConcurrentConnections=10.\nLogs:\n%s", allLogs)
		}
	})
}

// waitForAgent polls the health endpoint until the agent is ready.
func waitForAgent(t *testing.T, baseURL string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/api/v1/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("agent not ready after %v", timeout)
}
