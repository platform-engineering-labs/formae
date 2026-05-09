// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestTargetResolvable verifies the cross-plugin target resolvable flow:
// a Docker Compose stack exposes endpoints, a Grafana target resolves its
// connection URL from those endpoints, and a Grafana folder is created on
// that target.
//
// Requires: Docker with Compose v2, GRAFANA_AUTH not needed (uses admin:admin
// from the LGTM stack).
func TestTargetResolvable(t *testing.T) {
	// Skip if docker compose is not available.
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found, skipping target resolvable test")
	}

	bin := FormaeBinary(t)
	agent := StartAgent(t, bin, WithEnv("GRAFANA_AUTH=admin:admin"))
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "target_resolvable.pkl")
	commandTimeout := 5 * time.Minute

	// Step 1: Apply — creates compose stack, resolves grafana target, creates folder.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify resources were created.
	resources := cli.Inventory(t, "--query", "stack:e2e-target-resolvable")
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources (compose stack + grafana folder), got %d", len(resources))
	}
	RequireResource(t, resources, "e2e-lgtm")
	RequireResource(t, resources, "e2e-resolvable-folder")

	// Step 3: Verify the compose stack has resolved endpoints.
	lgtm := FindResource(resources, "e2e-lgtm")
	if lgtm == nil {
		t.Fatal("e2e-lgtm resource not found")
	}
	AssertStringProperty(t, *lgtm, "projectName", "formae-e2e-resolvable")

	// Step 4: Destroy — grafana target has $ref so it gets deleted,
	// docker target survives (plain config, no $ref).
	destroyID := cli.Destroy(t, fixture, "--yes")
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 5: Verify resources are gone.
	remaining := cli.Inventory(t, "--query", "stack:e2e-target-resolvable")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}

	// Step 6: Clean up compose project (in case destroy didn't fully clean Docker).
	_ = exec.Command("docker", "compose", "-p", "formae-e2e-resolvable", "down", "-v", "--remove-orphans").Run()
}
