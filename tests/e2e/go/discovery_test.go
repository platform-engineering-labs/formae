// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// ptr returns a pointer to the given string value. Used for Azure SDK
// calls that require *string parameters.
func ptr(s string) *string {
	return &s
}

func TestDiscovery(t *testing.T) {
	t.Run("AWS", testDiscoveryAWS)
	t.Run("Azure", testDiscoveryAzure)
}

// testDiscoveryAWS verifies that the agent discovers unmanaged AWS IAM resources
// and injects parent resolvable references for child RolePolicies.
//
// Test hierarchy:
//   - 1 managed Role (via formae) + 1 unmanaged RolePolicy (out-of-band)
//   - 1 unmanaged Role "role-a" (out-of-band) + 1 unmanaged RolePolicy (out-of-band)
//   - 1 unmanaged Role "role-b" (out-of-band) + 2 unmanaged RolePolicies (out-of-band)
//
// Expected unmanaged: 2 Roles + 4 RolePolicies = 6 unmanaged resources.
func testDiscoveryAWS(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin, WithDiscovery("30.s"))
	cli := NewFormaeCLI(bin, agent.ConfigPath())

	fixture := filepath.Join(fixturesDir(t), "discovery_managed_aws.pkl")
	commandTimeout := 2 * time.Minute
	const nativeIDPrefix = "formae-e2e-discovery"

	// Step 1: Apply the managed Role + target.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify the managed Role exists.
	resources := cli.Inventory(t, "--query", "stack:e2e-discovery-aws")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource in stack e2e-discovery-aws, got %d", len(resources))
	}
	managedRole := RequireResource(t, resources, "e2e-discovery-managed-role")
	if managedRole.Type != "AWS::IAM::Role" {
		t.Fatalf("expected type AWS::IAM::Role, got %s", managedRole.Type)
	}

	// Step 3: Create out-of-band resources via AWS SDK.
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-west-2"))
	if err != nil {
		t.Fatalf("failed to load AWS config: %v", err)
	}
	iamClient := iam.NewFromConfig(cfg)

	assumeRolePolicy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
	logsPolicy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"logs:*","Resource":"*"}]}`

	// Create RolePolicy under the managed role.
	createAWSRolePolicy(t, ctx, iamClient, "formae-e2e-discovery-managed", "formae-e2e-discovery-policy-managed", logsPolicy)

	// Create unmanaged Role A + 1 RolePolicy.
	createAWSRole(t, ctx, iamClient, "formae-e2e-discovery-role-a", assumeRolePolicy)
	createAWSRolePolicy(t, ctx, iamClient, "formae-e2e-discovery-role-a", "formae-e2e-discovery-policy-a1", logsPolicy)

	// Create unmanaged Role B + 2 RolePolicies.
	createAWSRole(t, ctx, iamClient, "formae-e2e-discovery-role-b", assumeRolePolicy)
	createAWSRolePolicy(t, ctx, iamClient, "formae-e2e-discovery-role-b", "formae-e2e-discovery-policy-b1", logsPolicy)
	createAWSRolePolicy(t, ctx, iamClient, "formae-e2e-discovery-role-b", "formae-e2e-discovery-policy-b2", logsPolicy)

	// Register cleanup for out-of-band resources as safety net (policies first, then roles).
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		deleteAWSRolePolicy(t, cleanupCtx, iamClient, "formae-e2e-discovery-managed", "formae-e2e-discovery-policy-managed")
		deleteAWSRolePolicy(t, cleanupCtx, iamClient, "formae-e2e-discovery-role-a", "formae-e2e-discovery-policy-a1")
		deleteAWSRolePolicy(t, cleanupCtx, iamClient, "formae-e2e-discovery-role-b", "formae-e2e-discovery-policy-b1")
		deleteAWSRolePolicy(t, cleanupCtx, iamClient, "formae-e2e-discovery-role-b", "formae-e2e-discovery-policy-b2")
		deleteAWSRole(t, cleanupCtx, iamClient, "formae-e2e-discovery-role-a")
		deleteAWSRole(t, cleanupCtx, iamClient, "formae-e2e-discovery-role-b")
	})

	// Step 4: Force discovery and wait for unmanaged resources.
	// We expect 6 unmanaged with our prefix: 2 Roles + 4 RolePolicies.
	discoveryTimeout := 5 * time.Minute
	allResources := WaitForDiscovery(t, cli, nativeIDPrefix, 6, discoveryTimeout)
	unmanaged := FilterByNativeIDContains(FilterUnmanaged(allResources), nativeIDPrefix)
	t.Logf("discovered %d unmanaged test resources", len(unmanaged))

	// Step 5: Verify the managed Role is still managed.
	managedResources := cli.Inventory(t, "--query", "stack:e2e-discovery-aws managed:true")
	foundManaged := false
	for _, r := range managedResources {
		if r.Label == "e2e-discovery-managed-role" && r.Managed {
			foundManaged = true
			break
		}
	}
	if !foundManaged {
		t.Error("managed role 'e2e-discovery-managed-role' was not found or lost its managed status")
	}

	// Step 6: Count unmanaged resources by type (filtered by prefix).
	unmanagedRoles := FindResourceByType(unmanaged, "AWS::IAM::Role")
	unmanagedPolicies := FindResourceByType(unmanaged, "AWS::IAM::RolePolicy")

	if len(unmanagedRoles) != 2 {
		t.Errorf("expected 2 unmanaged Roles, got %d", len(unmanagedRoles))
		for _, r := range unmanagedRoles {
			t.Logf("  unmanaged role: label=%s nativeID=%s", r.Label, r.NativeID)
		}
	}

	if len(unmanagedPolicies) != 4 {
		t.Errorf("expected 4 unmanaged RolePolicies, got %d", len(unmanagedPolicies))
		for _, r := range unmanagedPolicies {
			t.Logf("  unmanaged policy: label=%s nativeID=%s", r.Label, r.NativeID)
		}
	}

	// Step 7: Verify RolePolicies have parent resolvable references.
	knownRoleNames := []string{"formae-e2e-discovery-managed", "formae-e2e-discovery-role-a", "formae-e2e-discovery-role-b"}
	for _, policy := range unmanagedPolicies {
		roleName, ok := policy.Properties["RoleName"]
		if !ok {
			t.Errorf("unmanaged RolePolicy %s missing RoleName property", policy.Label)
			continue
		}
		assertResolvableWithKnownValues(t, policy.Label, "RoleName", roleName,
			"AWS::IAM::Role", "RoleName", knownRoleNames)
	}

	// Step 8: Extract unmanaged resources to a temp PKL file.
	extractDir := t.TempDir()
	extractFile := filepath.Join(extractDir, "extracted.pkl")
	cli.ExtractToFile(t, "managed:false", extractFile)

	// Step 9: Set the stack label in the extracted PKL. The extract command
	// comments out the label for $unmanaged resources; we replace it with a
	// real stack to bring them under management.
	const importedStack = "e2e-discovery-aws-imported"
	SetExtractedStackLabel(t, extractFile, importedStack)

	// Step 10: Apply the extracted PKL to bring resources under management.
	importCmdID := cli.Apply(t, "reconcile", extractFile)
	importResult := cli.WaitForCommand(t, importCmdID, commandTimeout)
	RequireCommandSuccess(t, importResult)

	// Step 11: Verify no unmanaged test resources remain.
	postImportResources := cli.Inventory(t, "--query", "managed:false")
	unmanagedAfterImport := FilterByNativeIDContains(postImportResources, nativeIDPrefix)
	if len(unmanagedAfterImport) != 0 {
		t.Errorf("expected 0 unmanaged test resources after import, got %d", len(unmanagedAfterImport))
		for _, r := range unmanagedAfterImport {
			t.Logf("  still unmanaged: label=%s type=%s nativeID=%s", r.Label, r.Type, r.NativeID)
		}
	}

	// Step 12: Verify imported resources are on the correct stack.
	importedResources := cli.Inventory(t, "--query", "stack:"+importedStack)
	importedTestResources := FilterByNativeIDContains(importedResources, nativeIDPrefix)
	if len(importedTestResources) != 6 {
		t.Errorf("expected 6 managed resources on stack %s, got %d", importedStack, len(importedTestResources))
	}

	// Step 13: Destroy the extracted resources (formerly-unmanaged, now managed).
	// This removes the OOB policies first, enabling the managed Role to be
	// destroyed cleanly in the next step.
	extractDestroyID := cli.Destroy(t, extractFile)
	extractDestroyResult := cli.WaitForCommand(t, extractDestroyID, commandTimeout)
	RequireCommandSuccess(t, extractDestroyResult)

	// Step 14: Destroy the original managed resources.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 15: Verify no test resources remain in either stack.
	remaining := cli.Inventory(t, "--query", "stack:e2e-discovery-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources in stack e2e-discovery-aws after destroy, got %d", len(remaining))
	}
	remainingImported := cli.Inventory(t, "--query", "stack:"+importedStack)
	if len(remainingImported) != 0 {
		t.Errorf("expected 0 resources in stack %s after destroy, got %d", importedStack, len(remainingImported))
	}
}

// testDiscoveryAzure verifies that the agent discovers unmanaged Azure resources
// with double-nested children (RG -> VNet -> Subnet) and injects parent
// resolvable references.
//
// Test hierarchy:
//   - 1 managed RG (via formae) + 1 unmanaged VNet -> 1 unmanaged Subnet
//   - 1 unmanaged RG "rg-a" (out-of-band) + 1 VNet -> 1 Subnet
//   - 1 unmanaged RG "rg-b" (out-of-band) + 2 VNets -> each with 1 Subnet
//
// Expected unmanaged: 2 RGs + 4 VNets + 4 Subnets = 10 unmanaged resources.
func testDiscoveryAzure(t *testing.T) {
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if subscriptionID == "" {
		t.Fatal("AZURE_SUBSCRIPTION_ID environment variable is required for Azure tests")
	}

	bin := FormaeBinary(t)
	agent := StartAgent(t, bin, WithDiscovery("30.s"))
	cli := NewFormaeCLI(bin, agent.ConfigPath())

	fixture := filepath.Join(fixturesDir(t), "discovery_managed_azure.pkl")
	commandTimeout := 5 * time.Minute
	const nativeIDPrefix = "formae-e2e-discovery"

	// Step 1: Apply the managed RG + target.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify the managed RG exists.
	resources := cli.Inventory(t, "--query", "stack:e2e-discovery-azure")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource in stack e2e-discovery-azure, got %d", len(resources))
	}
	managedRg := RequireResource(t, resources, "e2e-discovery-managed-rg")
	if managedRg.Type != "Azure::Resources::ResourceGroup" {
		t.Fatalf("expected type Azure::Resources::ResourceGroup, got %s", managedRg.Type)
	}

	// Step 3: Create out-of-band resources via Azure SDK.
	ctx := context.Background()
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("failed to create Azure credential: %v", err)
	}

	rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)
	if err != nil {
		t.Fatalf("failed to create Azure resource groups client: %v", err)
	}

	vnetClient, err := armnetwork.NewVirtualNetworksClient(subscriptionID, cred, nil)
	if err != nil {
		t.Fatalf("failed to create Azure VNet client: %v", err)
	}

	subnetClient, err := armnetwork.NewSubnetsClient(subscriptionID, cred, nil)
	if err != nil {
		t.Fatalf("failed to create Azure Subnet client: %v", err)
	}

	location := "westeurope"

	// VNet + Subnet in the managed RG.
	createAzureVNet(t, ctx, vnetClient, "formae-e2e-discovery-managed-rg", "formae-e2e-discovery-vnet-managed", location, "10.10.0.0/16")
	createAzureSubnet(t, ctx, subnetClient, "formae-e2e-discovery-managed-rg", "formae-e2e-discovery-vnet-managed", "formae-e2e-discovery-subnet-managed", "10.10.1.0/24")

	// RG A + VNet + Subnet.
	createAzureResourceGroup(t, ctx, rgClient, "formae-e2e-discovery-rg-a", location)
	createAzureVNet(t, ctx, vnetClient, "formae-e2e-discovery-rg-a", "formae-e2e-discovery-vnet-a1", location, "10.20.0.0/16")
	createAzureSubnet(t, ctx, subnetClient, "formae-e2e-discovery-rg-a", "formae-e2e-discovery-vnet-a1", "formae-e2e-discovery-subnet-a1", "10.20.1.0/24")

	// RG B + 2 VNets + 2 Subnets.
	createAzureResourceGroup(t, ctx, rgClient, "formae-e2e-discovery-rg-b", location)
	createAzureVNet(t, ctx, vnetClient, "formae-e2e-discovery-rg-b", "formae-e2e-discovery-vnet-b1", location, "10.30.0.0/16")
	createAzureSubnet(t, ctx, subnetClient, "formae-e2e-discovery-rg-b", "formae-e2e-discovery-vnet-b1", "formae-e2e-discovery-subnet-b1", "10.30.1.0/24")
	createAzureVNet(t, ctx, vnetClient, "formae-e2e-discovery-rg-b", "formae-e2e-discovery-vnet-b2", location, "10.40.0.0/16")
	createAzureSubnet(t, ctx, subnetClient, "formae-e2e-discovery-rg-b", "formae-e2e-discovery-vnet-b2", "formae-e2e-discovery-subnet-b2", "10.40.1.0/24")

	// Register cleanup as safety net (subnets first, then vnets, then RGs).
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		// Subnets.
		deleteAzureSubnet(t, cleanupCtx, subnetClient, "formae-e2e-discovery-managed-rg", "formae-e2e-discovery-vnet-managed", "formae-e2e-discovery-subnet-managed")
		deleteAzureSubnet(t, cleanupCtx, subnetClient, "formae-e2e-discovery-rg-a", "formae-e2e-discovery-vnet-a1", "formae-e2e-discovery-subnet-a1")
		deleteAzureSubnet(t, cleanupCtx, subnetClient, "formae-e2e-discovery-rg-b", "formae-e2e-discovery-vnet-b1", "formae-e2e-discovery-subnet-b1")
		deleteAzureSubnet(t, cleanupCtx, subnetClient, "formae-e2e-discovery-rg-b", "formae-e2e-discovery-vnet-b2", "formae-e2e-discovery-subnet-b2")

		// VNets.
		deleteAzureVNet(t, cleanupCtx, vnetClient, "formae-e2e-discovery-managed-rg", "formae-e2e-discovery-vnet-managed")
		deleteAzureVNet(t, cleanupCtx, vnetClient, "formae-e2e-discovery-rg-a", "formae-e2e-discovery-vnet-a1")
		deleteAzureVNet(t, cleanupCtx, vnetClient, "formae-e2e-discovery-rg-b", "formae-e2e-discovery-vnet-b1")
		deleteAzureVNet(t, cleanupCtx, vnetClient, "formae-e2e-discovery-rg-b", "formae-e2e-discovery-vnet-b2")

		// RGs.
		deleteAzureResourceGroup(t, cleanupCtx, rgClient, "formae-e2e-discovery-rg-a")
		deleteAzureResourceGroup(t, cleanupCtx, rgClient, "formae-e2e-discovery-rg-b")
	})

	// Step 4: Force discovery and wait for unmanaged resources.
	// We expect 10 unmanaged: 2 RGs + 4 VNets + 4 Subnets.
	discoveryTimeout := 10 * time.Minute
	allResources := WaitForDiscovery(t, cli, nativeIDPrefix, 10, discoveryTimeout)
	unmanaged := FilterByNativeIDContains(FilterUnmanaged(allResources), nativeIDPrefix)
	t.Logf("discovered %d unmanaged test resources", len(unmanaged))

	// Step 5: Verify the managed RG is still managed.
	managedResources := cli.Inventory(t, "--query", "stack:e2e-discovery-azure managed:true")
	foundManaged := false
	for _, r := range managedResources {
		if r.Label == "e2e-discovery-managed-rg" && r.Managed {
			foundManaged = true
			break
		}
	}
	if !foundManaged {
		t.Error("managed RG 'e2e-discovery-managed-rg' was not found or lost its managed status")
	}

	// Step 6: Count unmanaged resources by type (filtered by prefix).
	unmanagedRGs := FindResourceByType(unmanaged, "Azure::Resources::ResourceGroup")
	unmanagedVNets := FindResourceByType(unmanaged, "Azure::Network::VirtualNetwork")
	unmanagedSubnets := FindResourceByType(unmanaged, "Azure::Network::Subnet")

	if len(unmanagedRGs) != 2 {
		t.Errorf("expected 2 unmanaged ResourceGroups, got %d", len(unmanagedRGs))
		for _, r := range unmanagedRGs {
			t.Logf("  unmanaged RG: label=%s nativeID=%s", r.Label, r.NativeID)
		}
	}

	if len(unmanagedVNets) != 4 {
		t.Errorf("expected 4 unmanaged VirtualNetworks, got %d", len(unmanagedVNets))
		for _, r := range unmanagedVNets {
			t.Logf("  unmanaged VNet: label=%s nativeID=%s", r.Label, r.NativeID)
		}
	}

	if len(unmanagedSubnets) != 4 {
		t.Errorf("expected 4 unmanaged Subnets, got %d", len(unmanagedSubnets))
		for _, r := range unmanagedSubnets {
			t.Logf("  unmanaged Subnet: label=%s nativeID=%s", r.Label, r.NativeID)
		}
	}

	// Step 7: Verify VNets have resolvable reference to parent RG.
	knownRGNames := []string{"formae-e2e-discovery-managed-rg", "formae-e2e-discovery-rg-a", "formae-e2e-discovery-rg-b"}
	for _, vnet := range unmanagedVNets {
		rgNameProp, ok := vnet.Properties["resourceGroupName"]
		if !ok {
			t.Errorf("unmanaged VNet %s missing resourceGroupName property", vnet.Label)
			continue
		}
		assertResolvableWithKnownValues(t, vnet.Label, "resourceGroupName", rgNameProp,
			"Azure::Resources::ResourceGroup", "name", knownRGNames)
	}

	// Step 8: Verify Subnets have resolvable references to parent VNet and RG.
	knownVNetNames := []string{
		"formae-e2e-discovery-vnet-managed",
		"formae-e2e-discovery-vnet-a1",
		"formae-e2e-discovery-vnet-b1",
		"formae-e2e-discovery-vnet-b2",
	}
	for _, subnet := range unmanagedSubnets {
		// virtualNetworkName should reference parent VNet.
		vnetNameProp, ok := subnet.Properties["virtualNetworkName"]
		if !ok {
			t.Errorf("unmanaged Subnet %s missing virtualNetworkName property", subnet.Label)
		} else {
			assertResolvableWithKnownValues(t, subnet.Label, "virtualNetworkName", vnetNameProp,
				"Azure::Network::VirtualNetwork", "name", knownVNetNames)
		}

		// resourceGroupName should reference parent RG (inherited through the VNet).
		rgNameProp, ok := subnet.Properties["resourceGroupName"]
		if !ok {
			t.Errorf("unmanaged Subnet %s missing resourceGroupName property", subnet.Label)
		} else {
			assertResolvableWithKnownValues(t, subnet.Label, "resourceGroupName", rgNameProp,
				"Azure::Network::VirtualNetwork", "resourceGroupName", knownRGNames)
		}
	}

	// Step 9: Extract unmanaged resources to a temp PKL file.
	extractDir := t.TempDir()
	extractFile := filepath.Join(extractDir, "extracted.pkl")
	cli.ExtractToFile(t, "managed:false", extractFile)

	// Step 10: Set the stack label in the extracted PKL.
	const importedStack = "e2e-discovery-azure-imported"
	SetExtractedStackLabel(t, extractFile, importedStack)

	// Step 11: Apply the extracted PKL to bring resources under management.
	importCmdID := cli.Apply(t, "reconcile", extractFile)
	importResult := cli.WaitForCommand(t, importCmdID, commandTimeout)
	RequireCommandSuccess(t, importResult)

	// Step 12: Verify no unmanaged test resources remain.
	postImportResources := cli.Inventory(t, "--query", "managed:false")
	unmanagedAfterImport := FilterByNativeIDContains(postImportResources, nativeIDPrefix)
	if len(unmanagedAfterImport) != 0 {
		t.Errorf("expected 0 unmanaged test resources after import, got %d", len(unmanagedAfterImport))
		for _, r := range unmanagedAfterImport {
			t.Logf("  still unmanaged: label=%s type=%s nativeID=%s", r.Label, r.Type, r.NativeID)
		}
	}

	// Step 13: Verify imported resources are on the correct stack.
	importedResources := cli.Inventory(t, "--query", "stack:"+importedStack)
	importedTestResources := FilterByNativeIDContains(importedResources, nativeIDPrefix)
	if len(importedTestResources) != 10 {
		t.Errorf("expected 10 managed resources on stack %s, got %d", importedStack, len(importedTestResources))
	}

	// Step 14: Destroy the extracted resources (formerly-unmanaged, now managed).
	extractDestroyID := cli.Destroy(t, extractFile)
	extractDestroyResult := cli.WaitForCommand(t, extractDestroyID, commandTimeout)
	RequireCommandSuccess(t, extractDestroyResult)

	// Step 15: Destroy the original managed resources.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 16: Verify no test resources remain in either stack.
	remaining := cli.Inventory(t, "--query", "stack:e2e-discovery-azure")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources in stack e2e-discovery-azure after destroy, got %d", len(remaining))
	}
	remainingImported := cli.Inventory(t, "--query", "stack:"+importedStack)
	if len(remainingImported) != 0 {
		t.Errorf("expected 0 resources in stack %s after destroy, got %d", importedStack, len(remainingImported))
	}
}

// assertResolvableWithKnownValues asserts that a property is a resolvable
// reference to one of the known values.
func assertResolvableWithKnownValues(t *testing.T, resourceLabel, propName string, propValue any, expectedType, expectedProperty string, knownValues []string) {
	t.Helper()
	m, ok := propValue.(map[string]any)
	if !ok {
		t.Errorf("resource %s property %s: expected resolvable (map), got %T: %v", resourceLabel, propName, propValue, propValue)
		return
	}
	if res, ok := m["$res"].(bool); !ok || !res {
		t.Errorf("resource %s property %s: expected $res=true, got %v", resourceLabel, propName, m["$res"])
		return
	}
	if m["$type"] != expectedType {
		t.Errorf("resource %s property %s: $type: got %v, want %s", resourceLabel, propName, m["$type"], expectedType)
	}
	if m["$property"] != expectedProperty {
		t.Errorf("resource %s property %s: $property: got %v, want %s", resourceLabel, propName, m["$property"], expectedProperty)
	}
	val, _ := m["$value"].(string)
	found := false
	for _, known := range knownValues {
		if val == known {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("resource %s property %s: $value %q not in known values %v", resourceLabel, propName, val, knownValues)
	}
}

// --- AWS SDK helpers ---

func createAWSRole(t *testing.T, ctx context.Context, client *iam.Client, roleName, assumeRolePolicy string) {
	t.Helper()
	t.Logf("creating AWS IAM role: %s", roleName)
	_, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(assumeRolePolicy),
	})
	if err != nil {
		t.Fatalf("failed to create IAM role %s: %v", roleName, err)
	}
}

func createAWSRolePolicy(t *testing.T, ctx context.Context, client *iam.Client, roleName, policyName, policyDocument string) {
	t.Helper()
	t.Logf("creating AWS IAM role policy: %s/%s", roleName, policyName)
	_, err := client.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyName:     aws.String(policyName),
		PolicyDocument: aws.String(policyDocument),
	})
	if err != nil {
		t.Fatalf("failed to create IAM role policy %s/%s: %v", roleName, policyName, err)
	}
}

func deleteAWSRolePolicy(t *testing.T, ctx context.Context, client *iam.Client, roleName, policyName string) {
	t.Helper()
	t.Logf("deleting AWS IAM role policy: %s/%s", roleName, policyName)
	_, err := client.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
		RoleName:   aws.String(roleName),
		PolicyName: aws.String(policyName),
	})
	if err != nil {
		t.Logf("warning: failed to delete IAM role policy %s/%s: %v", roleName, policyName, err)
	}
}

func deleteAWSRole(t *testing.T, ctx context.Context, client *iam.Client, roleName string) {
	t.Helper()
	t.Logf("deleting AWS IAM role: %s", roleName)
	_, err := client.DeleteRole(ctx, &iam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		t.Logf("warning: failed to delete IAM role %s: %v", roleName, err)
	}
}

// --- Azure SDK helpers ---

func createAzureResourceGroup(t *testing.T, ctx context.Context, client *armresources.ResourceGroupsClient, name, location string) {
	t.Helper()
	t.Logf("creating Azure resource group: %s", name)
	_, err := client.CreateOrUpdate(ctx, name, armresources.ResourceGroup{
		Location: ptr(location),
	}, nil)
	if err != nil {
		t.Fatalf("failed to create Azure resource group %s: %v", name, err)
	}
}

func createAzureVNet(t *testing.T, ctx context.Context, client *armnetwork.VirtualNetworksClient, rgName, vnetName, location, addressPrefix string) {
	t.Helper()
	t.Logf("creating Azure VNet: %s/%s", rgName, vnetName)
	poller, err := client.BeginCreateOrUpdate(ctx, rgName, vnetName, armnetwork.VirtualNetwork{
		Location: ptr(location),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{
				AddressPrefixes: []*string{ptr(addressPrefix)},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("failed to begin creating Azure VNet %s/%s: %v", rgName, vnetName, err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("failed to create Azure VNet %s/%s: %v", rgName, vnetName, err)
	}
}

func createAzureSubnet(t *testing.T, ctx context.Context, client *armnetwork.SubnetsClient, rgName, vnetName, subnetName, addressPrefix string) {
	t.Helper()
	t.Logf("creating Azure Subnet: %s/%s/%s", rgName, vnetName, subnetName)
	poller, err := client.BeginCreateOrUpdate(ctx, rgName, vnetName, subnetName, armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix: ptr(addressPrefix),
		},
	}, nil)
	if err != nil {
		t.Fatalf("failed to begin creating Azure Subnet %s/%s/%s: %v", rgName, vnetName, subnetName, err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("failed to create Azure Subnet %s/%s/%s: %v", rgName, vnetName, subnetName, err)
	}
}

func deleteAzureSubnet(t *testing.T, ctx context.Context, client *armnetwork.SubnetsClient, rgName, vnetName, subnetName string) {
	t.Helper()
	t.Logf("deleting Azure Subnet: %s/%s/%s", rgName, vnetName, subnetName)
	poller, err := client.BeginDelete(ctx, rgName, vnetName, subnetName, nil)
	if err != nil {
		t.Logf("warning: failed to begin deleting Azure Subnet %s/%s/%s: %v", rgName, vnetName, subnetName, err)
		return
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Logf("warning: failed to delete Azure Subnet %s/%s/%s: %v", rgName, vnetName, subnetName, err)
	}
}

func deleteAzureVNet(t *testing.T, ctx context.Context, client *armnetwork.VirtualNetworksClient, rgName, vnetName string) {
	t.Helper()
	t.Logf("deleting Azure VNet: %s/%s", rgName, vnetName)
	poller, err := client.BeginDelete(ctx, rgName, vnetName, nil)
	if err != nil {
		t.Logf("warning: failed to begin deleting Azure VNet %s/%s: %v", rgName, vnetName, err)
		return
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Logf("warning: failed to delete Azure VNet %s/%s: %v", rgName, vnetName, err)
	}
}

func deleteAzureResourceGroup(t *testing.T, ctx context.Context, client *armresources.ResourceGroupsClient, name string) {
	t.Helper()
	t.Logf("deleting Azure resource group: %s", name)
	poller, err := client.BeginDelete(ctx, name, nil)
	if err != nil {
		t.Logf("warning: failed to begin deleting Azure resource group %s: %v", name, err)
		return
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Logf("warning: failed to delete Azure resource group %s: %v", name, err)
	}
}
