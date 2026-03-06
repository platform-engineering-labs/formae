// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCascadeDelete(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	parentFixture := filepath.Join(fixturesDir(t), "cascade_parent_aws.pkl")
	childFixture := filepath.Join(fixturesDir(t), "cascade_child_aws.pkl")
	commandTimeout := 5 * time.Minute

	// Step 1: Apply parent stack — creates VPC.
	parentCmdID := cli.Apply(t, "reconcile", parentFixture)
	parentResult := cli.WaitForCommand(t, parentCmdID, commandTimeout)
	RequireCommandSuccess(t, parentResult)

	// Step 2: Apply child stack — creates subnet referencing parent VPC.
	childCmdID := cli.Apply(t, "reconcile", childFixture)
	childResult := cli.WaitForCommand(t, childCmdID, commandTimeout)
	RequireCommandSuccess(t, childResult)

	// Step 3: Verify 1 resource in each stack.
	parentResources := cli.Inventory(t, "--query", "stack:e2e-cascade-parent-aws")
	if len(parentResources) != 1 {
		t.Fatalf("expected 1 resource in parent stack, got %d", len(parentResources))
	}
	RequireResource(t, parentResources, "e2e-cascade-vpc")

	childResources := cli.Inventory(t, "--query", "stack:e2e-cascade-child-aws")
	if len(childResources) != 1 {
		t.Fatalf("expected 1 resource in child stack, got %d", len(childResources))
	}
	RequireResource(t, childResources, "e2e-cascade-subnet")

	// Step 4: Destroy parent without cascade — should fail with abort.
	stderr := cli.DestroyExpectError(t, parentFixture, "--yes")
	if !strings.Contains(stderr, "cascade deletes detected") {
		t.Errorf("expected 'cascade deletes detected' in stderr, got: %s", stderr)
	}

	// Step 5: Verify both stacks still have their resources.
	parentAfterAbort := cli.Inventory(t, "--query", "stack:e2e-cascade-parent-aws")
	if len(parentAfterAbort) != 1 {
		t.Fatalf("expected parent stack to still have 1 resource after abort, got %d", len(parentAfterAbort))
	}
	childAfterAbort := cli.Inventory(t, "--query", "stack:e2e-cascade-child-aws")
	if len(childAfterAbort) != 1 {
		t.Fatalf("expected child stack to still have 1 resource after abort, got %d", len(childAfterAbort))
	}

	// Step 6: Destroy parent with cascade — should succeed and destroy both stacks.
	cascadeID := cli.Destroy(t, parentFixture, "--on-dependents=cascade", "--yes")
	cascadeResult := cli.WaitForCommand(t, cascadeID, commandTimeout)
	RequireCommandSuccess(t, cascadeResult)

	// Step 7: Verify both stacks are empty.
	parentRemaining := cli.Inventory(t, "--query", "stack:e2e-cascade-parent-aws")
	if len(parentRemaining) != 0 {
		t.Errorf("expected 0 resources in parent stack after cascade, got %d", len(parentRemaining))
	}
	childRemaining := cli.Inventory(t, "--query", "stack:e2e-cascade-child-aws")
	if len(childRemaining) != 0 {
		t.Errorf("expected 0 resources in child stack after cascade, got %d", len(childRemaining))
	}
}
