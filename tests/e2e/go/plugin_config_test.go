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
// full path: PKL config → agent translation → coordinator merge → stats API.
//
// It configures the SFTP plugin with overrides for rate limit, label config,
// and resourceTypesToDiscover, then verifies these merged values are visible
// in the /api/v1/stats response.
func TestPluginConfig(t *testing.T) {
	bin := FormaeBinary(t)

	resourcePluginsBlock := `
    resourcePlugins {
        new BaseResourcePluginConfig {
            type = "sftp"
            rateLimit { maxRequestsPerSecond = 3 }
            resourceTypesToDiscover { "SFTP::File" }
            labelTagKeys { "custom-label" }
        }
    }`

	agent := StartAgent(t, bin, WithResourcePlugins(resourcePluginsBlock))
	baseURL := fmt.Sprintf("http://localhost:%d", agent.Port())

	waitForAgent(t, baseURL, 30*time.Second)

	// Give plugins time to start and announce
	time.Sleep(3 * time.Second)

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
			Namespace               string   `json:"Namespace"`
			MaxRequestsPerSecond    int      `json:"MaxRequestsPerSecond"`
			ResourceTypesToDiscover []string `json:"ResourceTypesToDiscover"`
			LabelTagKeys            []string `json:"LabelTagKeys"`
			LabelConfig             *struct {
				DefaultQuery string `json:"DefaultQuery"`
			} `json:"LabelConfig"`
		} `json:"Plugins"`
	}
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("failed to parse stats: %v\nbody: %s", err, string(body))
	}

	var found bool
	for _, p := range stats.Plugins {
		if p.Namespace == "SFTP" {
			found = true

			t.Run("rate limit override", func(t *testing.T) {
				if p.MaxRequestsPerSecond != 3 {
					t.Errorf("expected rate limit 3, got %d", p.MaxRequestsPerSecond)
				}
			})

			t.Run("resourceTypesToDiscover override", func(t *testing.T) {
				if len(p.ResourceTypesToDiscover) != 1 || p.ResourceTypesToDiscover[0] != "SFTP::File" {
					t.Errorf("expected resourceTypesToDiscover [SFTP::File], got %v", p.ResourceTypesToDiscover)
				}
			})

			t.Run("labelTagKeys override", func(t *testing.T) {
				if len(p.LabelTagKeys) != 1 || p.LabelTagKeys[0] != "custom-label" {
					t.Errorf("expected labelTagKeys [custom-label], got %v", p.LabelTagKeys)
				}
			})

			t.Run("label config from plugin defaults", func(t *testing.T) {
				if p.LabelConfig == nil {
					t.Fatal("expected LabelConfig to be present")
				}
				// SFTP plugin default: $.path
				if p.LabelConfig.DefaultQuery != "$.path" {
					t.Errorf("expected LabelConfig.DefaultQuery '$.path', got %q", p.LabelConfig.DefaultQuery)
				}
			})

			break
		}
	}
	if !found {
		t.Errorf("SFTP plugin not found in stats. Plugins: %s", string(body))
	}
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
