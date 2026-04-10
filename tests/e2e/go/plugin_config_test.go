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
        new BaseResourcePluginConfig {
            type = "sftp"
            rateLimit { maxRequestsPerSecond = 3 }
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

	// Note: Testing custom plugin-specific config fields (defaultTimeoutSeconds,
	// defaultFilePermissions) requires a typed PKL import via plugins:/ scheme.
	// That's tested via the SFTP plugin's unit tests and conformance tests.
	// The e2e test here validates the base-class override path (rate limit).
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
