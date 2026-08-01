// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSecretsGrafanaCascadeDelete exercises the RFC-110 dependency-aware DELETE
// cascade end-to-end: an AWS Secrets Manager JSON secret supplies a Grafana
// target's basic-auth credentials, and a folder lives on that target.
//
// Deleting JUST the secret is a dependency conflict — the Grafana target's config
// $refs it. The model:
//  1. Default (no flag): ABORT. The destroy is rejected and names the dependent
//     target; nothing is torn down.
//  2. --on-dependents=cascade: the dependent target and its folder ARE torn down.
//     The target's credentials are resolved from the still-present secret so the
//     authenticated folder delete succeeds, then the secret is deleted last.
//
// The agent runs WITHOUT GRAFANA_AUTH, so a successful cascade teardown of the
// folder proves the target's credentials were resolved from the secret on the
// delete path — the whole point of the delete-path/cascade work.
//
// Requires:
//   - AWS credentials (e.g. AWS_PROFILE=blue) + AWS_REGION
//   - Grafana at http://localhost:3333 (admin:admin)
//   - AWS + Grafana plugins installed locally (adopted, formae 0.89.0)
func TestSecretsGrafanaCascadeDelete(t *testing.T) {
	bin := FormaeBinary(t)
	// No GRAFANA_AUTH: credentials must come from the resolved AWS JSON secret.
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "secrets_grafana_cascade.pkl")
	// A forma declaring only the secret (same identity). Destroying it targets
	// just the secret; the Grafana target that $refs it surfaces as a cascade.
	secretFixture := filepath.Join(fixturesDir(t), "secrets_grafana_cascade_secret.pkl")
	const stackQuery = "stack:e2e-secrets-grafana-cascade"
	const folderTitle = "formae-e2e-cascade"
	commandTimeout := 5 * time.Minute

	// Step 1: Apply — secret + Grafana target (creds from the secret) + folder.
	cmdID := cli.Apply(t, "reconcile", fixture)
	RequireCommandSuccess(t, cli.WaitForCommand(t, cmdID, commandTimeout))

	resources := cli.Inventory(t, "--query", stackQuery)
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources after apply, got %d", len(resources))
	}
	RequireResource(t, resources, "e2e-grafana-cascade-creds")
	RequireResource(t, resources, "e2e-grafana-cascade-folder")
	if !grafanaHasFolder(t, "http://localhost:3333", "admin:admin", folderTitle) {
		t.Fatalf("folder %q not found in Grafana after apply", folderTitle)
	}

	// Step 2: Default-abort — destroy just the secret without cascade. The
	// Grafana target depends on it, so the destroy is rejected and nothing runs.
	stderr := cli.DestroyExpectError(t, secretFixture, "--yes")
	if !strings.Contains(stderr, "cascade deletes detected") {
		t.Errorf("expected a cascade-abort error, got stderr: %s", stderr)
	}

	// Nothing was torn down: both resources remain and the folder still exists.
	afterAbort := cli.Inventory(t, "--query", stackQuery)
	if len(afterAbort) != 2 {
		t.Fatalf("expected 2 resources to survive the abort, got %d", len(afterAbort))
	}
	if !grafanaHasFolder(t, "http://localhost:3333", "admin:admin", folderTitle) {
		t.Errorf("folder %q must still exist after an aborted destroy", folderTitle)
	}

	// Step 3: Cascade — destroy the secret with --on-dependents=cascade. The
	// dependent target and its folder are torn down (folder delete authenticated
	// with credentials resolved from the still-present secret), then the secret.
	cascadeID := cli.Destroy(t, secretFixture, "--on-dependents=cascade", "--yes")
	RequireCommandSuccess(t, cli.WaitForCommand(t, cascadeID, commandTimeout))

	// Everything is gone: no resources remain and the folder is removed from Grafana.
	remaining := cli.Inventory(t, "--query", stackQuery)
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after cascade, got %d", len(remaining))
	}
	if grafanaHasFolder(t, "http://localhost:3333", "admin:admin", folderTitle) {
		t.Errorf("folder %q must be gone after the cascade teardown", folderTitle)
	}
}

// grafanaHasFolder reports whether a folder with the given title exists in
// Grafana. A failed request (Grafana unreachable / unauthenticated) fails the test.
func grafanaHasFolder(t *testing.T, grafanaURL, auth, title string) bool {
	t.Helper()

	url := fmt.Sprintf("%s/api/folders", grafanaURL)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("failed to build Grafana request: %v", err)
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth)))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Grafana API call failed (is Grafana running at %s?): %v", grafanaURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Grafana folders API returned %d: %s", resp.StatusCode, body)
	}

	var folders []struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&folders); err != nil {
		t.Fatalf("failed to decode Grafana folders response: %v", err)
	}

	for _, f := range folders {
		if f.Title == title {
			return true
		}
	}
	return false
}
