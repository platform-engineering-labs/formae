// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAutoReconcile(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "auto_reconcile_aws.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply reconcile fixture — creates role + attaches auto-reconcile policy.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify 1 resource in the stack.
	resources := cli.Inventory(t, "--query", "stack:e2e-auto-reconcile-aws")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	RequireResource(t, resources, "e2e-auto-reconcile-role")

	// Step 3: OOB change — add a tag to the role via AWS SDK.
	tagAWSRole(t, "formae-e2e-auto-reconcile-role", "OOBTag", "oob-value")

	// Step 4: Force sync so the agent detects the drift.
	cli.ForceSync(t)

	// Step 5: Wait for auto-reconcile to revert the tag.
	// The auto-reconcile policy has a 5s interval; we allow 60s for it to
	// detect the drift and execute the reconcile.
	WaitForOOBChangeGone(t, cli, "stack:e2e-auto-reconcile-aws", "e2e-auto-reconcile-role",
		"Tags", 60*time.Second)

	// Step 6: Verify no Tags property on the resource.
	afterResources := cli.Inventory(t, "--query", "stack:e2e-auto-reconcile-aws")
	if len(afterResources) != 1 {
		t.Fatalf("expected 1 resource after auto-reconcile, got %d", len(afterResources))
	}
	roleAfter := RequireResource(t, afterResources, "e2e-auto-reconcile-role")
	if _, hasTags := roleAfter.Properties["Tags"]; hasTags {
		t.Error("expected OOB tag to be removed by auto-reconcile")
	}

	// Step 7: Destroy and verify cleanup.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-auto-reconcile-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}
