// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func TestSoftReconcile(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath())

	t.Run("AWS", func(t *testing.T) { testSoftReconcileAWS(t, cli) })
}

func testSoftReconcileAWS(t *testing.T, cli *FormaeCLI) {
	fixture := filepath.Join(fixturesDir(t), "soft_reconcile_aws.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply in reconcile mode to create the base resources.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify initial state.
	resources := cli.Inventory(t, "--query", "stack:e2e-soft-reconcile-aws")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	role := RequireResource(t, resources, "e2e-soft-reconcile-role")
	AssertStringProperty(t, role, "Description", "e2e soft reconcile test role")

	// Step 3: Make an OOB change via AWS SDK — add a tag to the role.
	tagAWSRole(t, "formae-e2e-soft-reconcile-role", "OOBTag", "oob-value")

	// Step 4: Trigger sync so the agent detects the OOB change.
	cli.ForceSync(t)
	// Give the sync command time to complete and update the datastore.
	time.Sleep(10 * time.Second)

	// Step 5: Attempt a soft reconcile (no --force) — should be rejected.
	cli.ApplyExpectRejected(t, "reconcile", fixture)

	// Step 6: Extract the current state to absorb the OOB change into IaC.
	extractedPath := filepath.Join(t.TempDir(), "extracted.pkl")
	cli.ExtractToFile(t, "stack:e2e-soft-reconcile-aws", extractedPath)

	// Step 7: Apply the extracted PKL (which reflects the current cloud state
	// including the OOB tag) — this updates formae's desired state to match reality.
	extractCmdID := cli.Apply(t, "reconcile", extractedPath)
	extractResult := cli.WaitForCommand(t, extractCmdID, commandTimeout)
	RequireCommandSuccess(t, extractResult)

	// Step 8: Reconcile again — should succeed as a no-op (no drift).
	noopCmdID := cli.Apply(t, "reconcile", extractedPath)
	noopResult := cli.WaitForCommand(t, noopCmdID, commandTimeout)
	RequireCommandSuccess(t, noopResult)

	// Step 9: Verify the tag is now present in the managed state.
	afterResources := cli.Inventory(t, "--query", "stack:e2e-soft-reconcile-aws")
	roleAfter := RequireResource(t, afterResources, "e2e-soft-reconcile-role")
	AssertTagExists(t, roleAfter, "Tags", "OOBTag", "oob-value")

	// Step 10: Destroy and verify cleanup. Use the extracted PKL since it
	// represents the full desired state including the absorbed OOB change.
	destroyID := cli.Destroy(t, extractedPath)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-soft-reconcile-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}

func TestHardReconcile(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath())

	t.Run("AWS", func(t *testing.T) { testHardReconcileAWS(t, cli) })
}

func testHardReconcileAWS(t *testing.T, cli *FormaeCLI) {
	fixture := filepath.Join(fixturesDir(t), "hard_reconcile_aws.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply in reconcile mode to create the base resources.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify initial state — no tags.
	resources := cli.Inventory(t, "--query", "stack:e2e-hard-reconcile-aws")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	role := RequireResource(t, resources, "e2e-hard-reconcile-role")
	AssertStringProperty(t, role, "Description", "e2e hard reconcile test role")

	// Step 3: Make an OOB change via AWS SDK — add a tag to the role.
	tagAWSRole(t, "formae-e2e-hard-reconcile-role", "OOBTag", "oob-value")

	// Step 4: Trigger sync so the agent detects the OOB change.
	cli.ForceSync(t)
	time.Sleep(10 * time.Second)

	// Step 5: Force reconcile — should succeed and overwrite the OOB change.
	forceCmdID := cli.Apply(t, "reconcile", fixture, "--force")
	forceResult := cli.WaitForCommand(t, forceCmdID, commandTimeout)
	RequireCommandSuccess(t, forceResult)

	// Step 6: Trigger another sync to pick up the current cloud state.
	cli.ForceSync(t)
	time.Sleep(10 * time.Second)

	// Step 7: Verify the OOB tag has been removed by the force reconcile.
	afterResources := cli.Inventory(t, "--query", "stack:e2e-hard-reconcile-aws")
	if len(afterResources) != 1 {
		t.Fatalf("expected 1 resource after force reconcile, got %d", len(afterResources))
	}
	roleAfter := RequireResource(t, afterResources, "e2e-hard-reconcile-role")
	if _, hasTags := roleAfter.Properties["Tags"]; hasTags {
		t.Error("expected OOB tag to be removed after force reconcile")
	}

	// Step 8: Destroy and verify cleanup.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-hard-reconcile-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}

// tagAWSRole adds a tag to an IAM role via the AWS SDK (OOB change).
func tagAWSRole(t *testing.T, roleName, tagKey, tagValue string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-west-2"))
	if err != nil {
		t.Fatalf("failed to load AWS config: %v", err)
	}

	client := iam.NewFromConfig(cfg)
	_, err = client.TagRole(ctx, &iam.TagRoleInput{
		RoleName: aws.String(roleName),
		Tags: []iamtypes.Tag{
			{Key: aws.String(tagKey), Value: aws.String(tagValue)},
		},
	})
	if err != nil {
		t.Fatalf("failed to tag IAM role %s: %v", roleName, err)
	}

	t.Logf("tagged IAM role %s with %s=%s (OOB)", roleName, tagKey, tagValue)
}
