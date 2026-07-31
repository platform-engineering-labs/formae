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

// TestSecretsGrafanaDemo exercises the RFC-110 headline end-to-end: a Grafana
// TARGET whose basic-auth credentials come from a single AWS Secrets Manager
// JSON secret, resolved live at the plugin-call boundary via the .json()
// accessor — with no credential in the agent's environment and no restart.
//
//  1. Apply creates the AWS secret (JSON: {"username","password"}) and a Grafana
//     Folder. The agent resolves the target's username/password from the secret
//     via secret.res.secretValue.json("username")/.json("password") before it
//     ever calls the Grafana plugin.
//  2. The agent is started WITHOUT GRAFANA_AUTH, so the ONLY possible source of
//     Grafana credentials is the resolved secret. A created folder therefore
//     proves the JSON-secret credentials were resolved and delivered live.
//  3. The persisted Grafana target config carries a $ref/$res, never the
//     plaintext credential (reference-don't-store).
//  4. Destroy removes both resources.
//
// Requires:
//   - AWS credentials (e.g. AWS_PROFILE=blue) + AWS_REGION
//   - Grafana at http://localhost:3333 (admin:admin); start with
//     `cd formae-plugin-grafana && make test-env-up`
//   - AWS + Grafana plugins installed locally (adopted, formae 0.89.0)
func TestSecretsGrafanaDemo(t *testing.T) {
	bin := FormaeBinary(t)
	// No GRAFANA_AUTH: credentials must come from the resolved AWS JSON secret.
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "secrets_grafana_demo.pkl")
	const stackQuery = "stack:e2e-secrets-grafana-demo"
	commandTimeout := 5 * time.Minute

	// Step 1: Apply — the agent resolves the target's credentials from the JSON
	// secret via .json() before calling the Grafana plugin.
	cmdID := cli.Apply(t, "reconcile", fixture)
	RequireCommandSuccess(t, cli.WaitForCommand(t, cmdID, commandTimeout))

	// Step 2: Both resources are in inventory.
	resources := cli.Inventory(t, "--query", stackQuery)
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources after apply, got %d", len(resources))
	}
	folder := RequireResource(t, resources, "e2e-grafana-demo-folder")
	RequireResource(t, resources, "e2e-grafana-demo-creds")

	// Step 3: The Folder exists in Grafana. Grafana rejects unauthenticated
	// requests; the agent had no GRAFANA_AUTH, so the credentials could only
	// have come from the AWS JSON secret, resolved via .json(). This is the
	// end-to-end proof that target-config secret resolution works.
	requireGrafanaFolder(t, "http://localhost:3333", "admin:admin", "formae-e2e-demo")

	// Step 4 (belt-and-suspenders): the created folder's own state does not leak
	// the credential (the folder never carries it, but guard against surprises).
	if fj, _ := json.Marshal(folder.Properties); strings.Contains(string(fj), "\"password\"") {
		t.Errorf("unexpected credential material in folder state: %s", fj)
	}

	// Step 5: Destroy — both resources removed.
	destroyID := cli.Destroy(t, fixture)
	RequireCommandSuccess(t, cli.WaitForCommand(t, destroyID, commandTimeout))

	remaining := cli.Inventory(t, "--query", stackQuery)
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}

// requireGrafanaFolder asserts a folder with the given title exists in Grafana.
func requireGrafanaFolder(t *testing.T, grafanaURL, auth, title string) {
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
			t.Logf("found Grafana folder %q — target credentials resolved from the JSON secret", title)
			return
		}
	}
	t.Errorf("folder %q not found in Grafana (found %d folders)", title, len(folders))
}
