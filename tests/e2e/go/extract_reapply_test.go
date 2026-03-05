// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExtractAndReapply verifies the round-trip: apply → extract → reapply
// (no-op) → destroy via extracted PKL. This ensures that the extract command
// produces valid PKL that exactly represents the managed state.
func TestExtractAndReapply(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "extract_reapply_aws.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply the original fixture to create resources.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify resources were created (role + policy with Resolvable).
	resources := cli.Inventory(t, "--query", "stack:e2e-extract-reapply-aws")
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources after apply, got %d", len(resources))
	}
	role := RequireResource(t, resources, "e2e-extract-reapply-role")
	AssertStringProperty(t, role, "Description", "e2e extract reapply test role")
	policy := RequireResource(t, resources, "e2e-extract-reapply-policy")
	AssertStringProperty(t, policy, "PolicyName", "formae-e2e-extract-reapply-policy")

	// Step 3: Extract the stack to a PKL file.
	extractedPath := filepath.Join(t.TempDir(), "extracted.pkl")
	cli.ExtractToFile(t, "stack:e2e-extract-reapply-aws", extractedPath)

	// Step 3b: Verify the extracted PKL contains a Resolvable reference for
	// the RolePolicy's roleName, not a plain string.
	extractedContent, err := os.ReadFile(extractedPath)
	if err != nil {
		t.Fatalf("failed to read extracted PKL: %v", err)
	}
	if !strings.Contains(string(extractedContent), "RoleResolvable") {
		t.Fatalf("extracted PKL should contain a RoleResolvable reference for roleName, got:\n%s", string(extractedContent))
	}
	t.Logf("extracted PKL contains RoleResolvable (Resolvable preserved)")

	// Step 4: Reapply the extracted PKL — should be a no-op since the
	// extracted state matches the current cloud state.
	reapplyID := cli.Apply(t, "reconcile", extractedPath)
	reapplyResult := cli.WaitForCommand(t, reapplyID, commandTimeout)
	RequireCommandSuccess(t, reapplyResult)

	// Step 5: Verify resources are unchanged.
	afterResources := cli.Inventory(t, "--query", "stack:e2e-extract-reapply-aws")
	if len(afterResources) != 2 {
		t.Fatalf("expected 2 resources after reapply, got %d", len(afterResources))
	}
	roleAfter := RequireResource(t, afterResources, "e2e-extract-reapply-role")
	AssertStringProperty(t, roleAfter, "Description", "e2e extract reapply test role")
	policyAfter := RequireResource(t, afterResources, "e2e-extract-reapply-policy")
	AssertStringProperty(t, policyAfter, "PolicyName", "formae-e2e-extract-reapply-policy")

	// Step 6: Destroy using the extracted PKL.
	destroyID := cli.Destroy(t, extractedPath)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 7: Verify the stack is empty.
	remaining := cli.Inventory(t, "--query", "stack:e2e-extract-reapply-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}
