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

// TestPluginConfig verifies the full per-plugin configuration flow:
//
// 1. Typed PKL config with plugin import → agent translation → coordinator merge
// 2. Base-class overrides (rate limit, resourceTypesToDiscover) visible in stats API
// 3. Plugin-specific custom fields (defaultTimeoutSeconds, defaultFilePermissions)
//    reach the plugin binary via the Configurable interface
func TestPluginConfig(t *testing.T) {
	bin := FormaeBinary(t)

	sftpImport := `import "plugins:/Sftp.pkl" as Sftp`
	resourcePluginsBlock := `
    resourcePlugins {
        new Sftp.PluginConfig {
            rateLimit { maxRequestsPerSecond = 3 }
            resourceTypesToDiscover { "SFTP::File" }
            labelTagKeys { "custom-label" }
            discoveryFilters {
                new MatchFilter {
                    resourceTypes { "SFTP::File" }
                    conditions {
                        new FilterCondition {
                            propertyPath = "$.hidden"
                            propertyValue = "true"
                        }
                    }
                }
            }
            defaultTimeoutSeconds = 60
            defaultFilePermissions = "0755"
        }
    }`

	agent := StartAgent(t, bin, WithResourcePlugins(sftpImport, resourcePluginsBlock))
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
			LabelConfig *struct {
				DefaultQuery string `json:"DefaultQuery"`
			} `json:"LabelConfig"`
			DiscoveryFilters []struct {
				ResourceTypes []string `json:"ResourceTypes"`
			} `json:"DiscoveryFilters"`
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

			t.Run("discovery filters override", func(t *testing.T) {
				if len(p.DiscoveryFilters) != 1 {
					t.Fatalf("expected 1 discovery filter, got %d", len(p.DiscoveryFilters))
				}
				if len(p.DiscoveryFilters[0].ResourceTypes) != 1 || p.DiscoveryFilters[0].ResourceTypes[0] != "SFTP::File" {
					t.Errorf("expected filter for SFTP::File, got %v", p.DiscoveryFilters[0].ResourceTypes)
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

	t.Run("plugin-specific config received via Configure()", func(t *testing.T) {
		// The SFTP plugin logs its config when Configure() is called.
		// Check the agent stdout log for the expected output.
		var allLogs string
		if content, err := os.ReadFile(agent.LogFile()); err == nil {
			allLogs += string(content)
		}
		if content, err := os.ReadFile(agent.StdoutLogFile()); err == nil {
			allLogs += string(content)
		}

		if !strings.Contains(allLogs, "defaultTimeoutSeconds=60") {
			t.Errorf("expected Configure() log with defaultTimeoutSeconds=60\nLogs:\n%s", allLogs)
		}
		if !strings.Contains(allLogs, "defaultFilePermissions=0755") {
			t.Errorf("expected Configure() log with defaultFilePermissions=0755\nLogs:\n%s", allLogs)
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
