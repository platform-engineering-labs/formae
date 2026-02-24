// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

func TestDestroy(t *testing.T) {
	t.Run("AWS", testDestroyAWS)
	t.Run("Azure", testDestroyAzure)
}

func testDestroyAWS(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath())

	fixture := filepath.Join(fixturesDir(t), "reconcile_apply_aws.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply in reconcile mode.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Query inventory and verify resources exist.
	resources := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources in stack e2e-reconcile-aws, got %d", len(resources))
	}

	// Step 3: Capture the role name for later cloud verification.
	role := RequireResource(t, resources, "e2e-test-role")
	roleName := StringProperty(t, role, "RoleName")
	t.Logf("captured AWS IAM role name: %s", roleName)

	// Step 4: Destroy.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 5: Verify inventory is empty.
	remaining := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}

	// Step 6: Verify the IAM role is actually gone in AWS.
	verifyAWSRoleDeleted(t, roleName)
}

func testDestroyAzure(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath())

	fixture := filepath.Join(fixturesDir(t), "reconcile_apply_azure.pkl")
	commandTimeout := 5 * time.Minute

	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if subscriptionID == "" {
		t.Fatal("AZURE_SUBSCRIPTION_ID environment variable is required for Azure tests")
	}

	// Step 1: Apply in reconcile mode.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Query inventory and verify resources exist.
	resources := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure")
	if len(resources) != 3 {
		t.Fatalf("expected 3 resources in stack e2e-reconcile-azure, got %d", len(resources))
	}

	// Step 3: Capture the resource group name for later cloud verification.
	rg := RequireResource(t, resources, "e2e-test-rg")
	rgName := StringProperty(t, rg, "name")
	t.Logf("captured Azure resource group name: %s", rgName)

	// Step 4: Destroy.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 5: Verify inventory is empty.
	remaining := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}

	// Step 6: Verify the resource group is actually gone in Azure.
	verifyAzureResourceGroupDeleted(t, subscriptionID, rgName)
}

// verifyAWSRoleDeleted uses the AWS IAM SDK to confirm that the given role
// no longer exists. It expects a NoSuchEntity error from GetRole.
func verifyAWSRoleDeleted(t *testing.T, roleName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-west-2"))
	if err != nil {
		t.Fatalf("failed to load AWS config: %v", err)
	}

	client := iam.NewFromConfig(cfg)
	_, err = client.GetRole(ctx, &iam.GetRoleInput{
		RoleName: &roleName,
	})
	if err == nil {
		t.Errorf("expected IAM role %q to be deleted, but GetRole succeeded", roleName)
		return
	}

	// The AWS SDK returns a NoSuchEntity error when the role does not exist.
	if !strings.Contains(err.Error(), "NoSuchEntity") {
		t.Errorf("expected NoSuchEntity error for role %q, got: %v", roleName, err)
	}
}

// verifyAzureResourceGroupDeleted uses the Azure SDK to confirm that the
// given resource group no longer exists. It expects a NotFound (404) error.
func verifyAzureResourceGroupDeleted(t *testing.T, subscriptionID, rgName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("failed to create Azure credential: %v", err)
	}

	client, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)
	if err != nil {
		t.Fatalf("failed to create Azure resource groups client: %v", err)
	}

	_, err = client.Get(ctx, rgName, nil)
	if err == nil {
		t.Errorf("expected resource group %q to be deleted, but Get succeeded", rgName)
		return
	}

	// The Azure SDK returns a ResourceGroupNotFound or ResourceNotFound error
	// with a 404 status code when the resource group does not exist.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "ResourceGroupNotFound") && !strings.Contains(errMsg, "ResourceNotFound") && !strings.Contains(errMsg, "404") {
		t.Errorf("expected NotFound error for resource group %q, got: %v", rgName, err)
	}
}
