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

// TestSecretsGrafanaDemo exercises the first-class-secrets feature end-to-end:
//
//  1. Apply creates an AWS Secrets Manager secret (with a formae.value opaque
//     string) and a Grafana ContactPoint whose settingsMap wires the secret's
//     value via theSecret.res.secretString — a cross-plugin resolvable.
//  2. After apply the ContactPoint exists in Grafana, confirming the resolvable
//     was resolved at the plugin boundary and the credential delivered live.
//  3. The inventory for the Secret does not expose the plaintext credential
//     (secretString is writeOnly; it is never returned by AWS read-back).
//  4. Destroy removes both resources cleanly.
//
// Requires:
//   - AWS credentials: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION
//   - Grafana at http://localhost:3333 (admin:admin)
//     Start with: cd /path/to/formae-plugin-grafana && make test-env-up
func TestSecretsGrafanaDemo(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin,
		WithEnv("GRAFANA_AUTH=admin:admin"),
	)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "secrets_grafana_demo.pkl")
	const stackQuery = "stack:e2e-secrets-grafana-demo"
	commandTimeout := 5 * time.Minute

	// Step 1: Apply — secret and ContactPoint created in dependency order.
	// The agent resolves theSecret.res.secretString at the plugin boundary
	// before passing it to the Grafana plugin.
	cmdID := cli.Apply(t, "reconcile", fixture)
	RequireCommandSuccess(t, cli.WaitForCommand(t, cmdID, commandTimeout))

	// Step 2: Both resources are in inventory.
	resources := cli.Inventory(t, "--query", stackQuery)
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources after apply, got %d", len(resources))
	}
	RequireResource(t, resources, "e2e-grafana-demo-contactpoint")
	secret := RequireResource(t, resources, "e2e-grafana-demo-secret")

	// Step 3: The secret's plaintext is not exposed in the datastore.
	// secretString is writeOnly — AWS does not return it on read-back —
	// so it must be absent from inventory properties.
	if raw, has := secret.Properties["secretString"]; has {
		rawJSON, _ := json.Marshal(raw)
		if strings.Contains(string(rawJSON), "grafana-webhook-token-demo-placeholder") {
			t.Errorf("plaintext secret value leaked into inventory: %s", rawJSON)
		}
	}

	// Step 4: The ContactPoint exists in Grafana — the resolvable was resolved
	// and the credential delivered at the plugin boundary.
	requireGrafanaContactPoint(t, "http://localhost:3333", "admin:admin", "formae-e2e-demo-webhook")

	// Step 5: Destroy — both resources removed.
	destroyID := cli.Destroy(t, fixture)
	RequireCommandSuccess(t, cli.WaitForCommand(t, destroyID, commandTimeout))

	remaining := cli.Inventory(t, "--query", stackQuery)
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}

// requireGrafanaContactPoint asserts that a contact point with the given name
// exists in Grafana by querying the provisioning API.
func requireGrafanaContactPoint(t *testing.T, grafanaURL, auth, name string) {
	t.Helper()

	url := fmt.Sprintf("%s/api/v1/provisioning/contact-points", grafanaURL)
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
		t.Fatalf("Grafana contact-points API returned %d: %s", resp.StatusCode, body)
	}

	var points []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&points); err != nil {
		t.Fatalf("failed to decode Grafana contact-points response: %v", err)
	}

	for _, p := range points {
		if p.Name == name {
			t.Logf("found Grafana contact point %q", name)
			return
		}
	}
	t.Errorf("contact point %q not found in Grafana (found %d contact points)", name, len(points))
}
