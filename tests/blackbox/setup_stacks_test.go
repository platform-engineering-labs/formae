// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/api"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// The child executes real SetupStacks against an HTTP boundary, so swallowed
// setup failures cause a successful exit and fail the parent regression.
func TestSetupStacks_StopsOnUnusableSetup(t *testing.T) {
	if scenario := os.Getenv("FORMAE_SETUP_REGRESSION"); scenario != "" {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/commands":
				if scenario == "rejected" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_ = json.NewEncoder(w).Encode(apimodel.SubmitCommandResponse{CommandID: "setup", Simulation: apimodel.Simulation{ChangesRequired: scenario != "nochange"}})
			case "/api/v1/commands/status":
				state := scenario
				if scenario == "missing-inventory" {
					state = "Success"
				}
				_ = json.NewEncoder(w).Encode(apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{{CommandID: "setup", State: state}}})
			case "/api/v1/resources":
				forma := pkgmodel.Forma{}
				if scenario == "Success" {
					forma.Resources = []pkgmodel.Resource{{Stack: "stack-0", Label: "res-stack-0-a", Type: "Test::Generic::Resource", Properties: json.RawMessage(`{"Name":"res-stack-0-a","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`)}}
				}
				_ = json.NewEncoder(w).Encode(forma)
			default:
				t.Errorf("unexpected setup request: %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()
		endpoint, err := url.Parse(server.URL)
		require.NoError(t, err)
		port, err := strconv.Atoi(endpoint.Port())
		require.NoError(t, err)
		h := &TestHarness{t: t, client: api.NewClient(&pkgmodel.ClassicConnection{URL: "http://" + endpoint.Hostname(), Port: port}, nil, nil), terminalCommandStates: make(map[string]string)}
		model := NewStateModel(1, 1)
		h.SetupStacks(t, model, PropertyTestConfig{})
		t.Log("CHAOS_REACHED_AFTER_UNUSABLE_SETUP")
		return
	}
	for _, scenario := range []string{"rejected", "nochange", "Failed", "Canceled", "missing-inventory", "Success"} {
		t.Run(scenario, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSetupStacks_StopsOnUnusableSetup$", "-test.v")
			cmd.Env = append(os.Environ(), "FORMAE_SETUP_REGRESSION="+scenario)
			output, err := cmd.CombinedOutput()
			if scenario == "Success" {
				require.NoError(t, err, "valid setup should reach chaos; output:\n%s", output)
				require.Contains(t, string(output), "CHAOS_REACHED_AFTER_UNUSABLE_SETUP")
				return
			}
			require.Error(t, err, "unusable setup must fail its test case; output:\n%s", output)
			require.Contains(t, string(output), "SetupStacks:", "failure must come from setup validation")
			require.NotContains(t, string(output), "CHAOS_REACHED_AFTER_UNUSABLE_SETUP")
		})
	}
}
