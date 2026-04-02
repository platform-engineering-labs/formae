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

// TestTargetReplace tests that changing a target's config (e.g. region) is
// rejected when resources are not marked as portable. Once the formae PKL
// package is updated to include the portable field and plugins annotate their
// schemas, a happy-path replace test should be added here.
func TestTargetReplace(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	t.Run("AWS", func(t *testing.T) { testTargetReplaceRejectedAWS(t, cli) })
}

func testTargetReplaceRejectedAWS(t *testing.T, cli *FormaeCLI) {
	fixtureV1 := filepath.Join(fixturesDir(t), "target_replace_aws_v1.pkl")
	fixtureV2 := filepath.Join(fixturesDir(t), "target_replace_aws_v2.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply v1 — target in us-west-2 with an IAM role.
	cmdID := cli.Apply(t, "reconcile", fixtureV1)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify the role exists.
	resources := cli.Inventory(t, "--query", "stack:e2e-target-replace-aws")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource after v1 apply, got %d", len(resources))
	}
	RequireResource(t, resources, "e2e-target-replace-role")

	// Step 3: Apply v2 — target config changes from us-west-2 to us-east-1.
	// Since the IAM role's schema does not have Portable=true (not yet
	// annotated in the plugin PKL), the apply should be rejected.
	stderr := cli.ApplyExpectRejected(t, "reconcile", fixtureV2)
	if !strings.Contains(stderr, "not portable") && !strings.Contains(stderr, "cannot replace target") {
		t.Errorf("expected rejection to mention non-portable resources, got: %s", stderr)
	}

	// Step 4: Verify the original resource is still intact (not destroyed).
	afterResources := cli.Inventory(t, "--query", "stack:e2e-target-replace-aws")
	if len(afterResources) != 1 {
		t.Fatalf("expected 1 resource still present after rejected replace, got %d", len(afterResources))
	}
	RequireResource(t, afterResources, "e2e-target-replace-role")

	// Step 5: Destroy and verify cleanup.
	destroyID := cli.Destroy(t, fixtureV1)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-target-replace-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}

	verifyAWSRoleDeleted(t, "formae-e2e-target-replace-role")
}
