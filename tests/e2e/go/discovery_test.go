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
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin, WithDiscovery("30.s",
		"AWS::IAM::Role",
		"AWS::IAM::RolePolicy",
		"Azure::Resources::ResourceGroup",
		"Azure::Network::VirtualNetwork",
		"Azure::Network::Subnet",
	))
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	t.Run("AWS", func(t *testing.T) { testDiscoveryAWS(t, cli) })
	t.Run("Azure", func(t *testing.T) { testDiscoveryAzure(t, cli) })
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
func testDiscoveryAWS(t *testing.T, cli *FormaeCLI) {
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

	// Step 4: Force discovery and wait for all 6 specific unmanaged resources.
	expectedNames := []string{
		"formae-e2e-discovery-role-a",
		"formae-e2e-discovery-role-b",
		"formae-e2e-discovery-policy-managed",
		"formae-e2e-discovery-policy-a1",
		"formae-e2e-discovery-policy-b1",
		"formae-e2e-discovery-policy-b2",
	}
	discoveryTimeout := 2 * time.Minute
	unmanaged := FilterUnmanaged(WaitForDiscoveryByNames(t, cli, expectedNames, discoveryTimeout))
	t.Logf("discovered %d unmanaged resources", len(unmanaged))

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

	// Step 6: Verify each specific unmanaged resource exists with correct type.
	RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-role-a", "AWS::IAM::Role")
	RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-role-b", "AWS::IAM::Role")

	policyManaged := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-policy-managed", "AWS::IAM::RolePolicy")
	policyA1 := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-policy-a1", "AWS::IAM::RolePolicy")
	policyB1 := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-policy-b1", "AWS::IAM::RolePolicy")
	policyB2 := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-policy-b2", "AWS::IAM::RolePolicy")

	// Step 7: Verify RolePolicies have parent resolvable references.
	knownRoleNames := []string{"formae-e2e-discovery-managed", "formae-e2e-discovery-role-a", "formae-e2e-discovery-role-b"}
	for _, policy := range []Resource{policyManaged, policyA1, policyB1, policyB2} {
		roleName, ok := policy.Properties["RoleName"]
		if !ok {
			t.Errorf("unmanaged RolePolicy %s missing RoleName property", policy.Label)
			continue
		}
		assertResolvableWithKnownValues(t, policy.Label, "RoleName", roleName,
			"AWS::IAM::Role", "RoleName", knownRoleNames)
	}

	// Step 8: Extract unmanaged resources by type (Bluge queries are AND-only,
	// so we extract Roles and RolePolicies separately).
	extractDir := t.TempDir()
	rolesFile := filepath.Join(extractDir, "roles.pkl")
	policiesFile := filepath.Join(extractDir, "policies.pkl")
	cli.ExtractToFile(t, "type:AWS::IAM::Role managed:false", rolesFile)
	cli.ExtractToFile(t, "type:AWS::IAM::RolePolicy managed:false", policiesFile)

	// Step 9: Filter extracted files to keep only test resources.
	// The roles extract includes pre-existing AWS service-linked roles
	// that we must not import or destroy. Labels for IAM Roles are derived
	// from RoleName (via plugin LabelConfig), so our test resources all
	// have labels starting with the nativeIDPrefix.
	FilterExtractedPKLByLabelSubstring(t, rolesFile, nativeIDPrefix)
	FilterExtractedPKLByLabelSubstring(t, policiesFile, nativeIDPrefix)

	// Step 10: Set stack labels in extracted files. Each type gets its own
	// stack so that reconcile mode doesn't try to delete cross-file resources.
	const rolesStack = "e2e-discovery-aws-imported-roles"
	const policiesStack = "e2e-discovery-aws-imported-policies"
	SetExtractedStackLabel(t, rolesFile, rolesStack)
	SetExtractedStackLabel(t, policiesFile, policiesStack)

	// Step 11: Apply both extracted files to bring resources under management.
	rolesCmdID := cli.Apply(t, "reconcile", rolesFile)
	rolesResult := cli.WaitForCommand(t, rolesCmdID, commandTimeout)
	RequireCommandSuccess(t, rolesResult)

	policiesCmdID := cli.Apply(t, "reconcile", policiesFile)
	policiesResult := cli.WaitForCommand(t, policiesCmdID, commandTimeout)
	RequireCommandSuccess(t, policiesResult)

	// Step 12: Verify imported test resources across both stacks.
	importedRoles := cli.Inventory(t, "--query", "stack:"+rolesStack)
	importedPolicies := cli.Inventory(t, "--query", "stack:"+policiesStack)
	var allImported []Resource
	allImported = append(allImported, importedRoles...)
	allImported = append(allImported, importedPolicies...)
	importedTestResources := FilterByNativeIDContains(allImported, nativeIDPrefix)
	if len(importedTestResources) != 6 {
		t.Errorf("expected 6 test resources across import stacks, got %d", len(importedTestResources))
		for _, r := range importedTestResources {
			t.Logf("  imported: label=%s type=%s nativeID=%s", r.Label, r.Type, r.NativeID)
		}
	}

	// Step 13: Destroy imported resources (policies first, then roles,
	// to respect parent-child ordering).
	policiesDestroyID := cli.Destroy(t, policiesFile)
	policiesDestroyResult := cli.WaitForCommand(t, policiesDestroyID, commandTimeout)
	RequireCommandSuccess(t, policiesDestroyResult)

	rolesDestroyID := cli.Destroy(t, rolesFile)
	rolesDestroyResult := cli.WaitForCommand(t, rolesDestroyID, commandTimeout)
	RequireCommandSuccess(t, rolesDestroyResult)

	// Step 14: Destroy the original managed resources.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 15: Verify no test resources remain in any stack.
	for _, stack := range []string{"e2e-discovery-aws", rolesStack, policiesStack} {
		stackRemaining := cli.Inventory(t, "--query", "stack:"+stack)
		stackRemainingTest := FilterByNativeIDContains(stackRemaining, nativeIDPrefix)
		if len(stackRemainingTest) != 0 {
			t.Errorf("expected 0 test resources in stack %s after destroy, got %d", stack, len(stackRemainingTest))
		}
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
func testDiscoveryAzure(t *testing.T, cli *FormaeCLI) {
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if subscriptionID == "" {
		t.Skip("AZURE_SUBSCRIPTION_ID not set, skipping Azure tests")
	}

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

	// Step 4: Force discovery and wait for all 10 specific unmanaged resources.
	expectedNames := []string{
		"formae-e2e-discovery-rg-a",
		"formae-e2e-discovery-rg-b",
		"formae-e2e-discovery-vnet-managed",
		"formae-e2e-discovery-vnet-a1",
		"formae-e2e-discovery-vnet-b1",
		"formae-e2e-discovery-vnet-b2",
		"formae-e2e-discovery-subnet-managed",
		"formae-e2e-discovery-subnet-a1",
		"formae-e2e-discovery-subnet-b1",
		"formae-e2e-discovery-subnet-b2",
	}
	discoveryTimeout := 5 * time.Minute
	unmanaged := FilterUnmanaged(WaitForDiscoveryByNames(t, cli, expectedNames, discoveryTimeout))
	t.Logf("discovered %d unmanaged resources", len(unmanaged))

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

	// Step 6: Verify each specific unmanaged resource exists with correct type.
	RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-rg-a", "Azure::Resources::ResourceGroup")
	RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-rg-b", "Azure::Resources::ResourceGroup")

	vnetManaged := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-vnet-managed", "Azure::Network::VirtualNetwork")
	vnetA1 := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-vnet-a1", "Azure::Network::VirtualNetwork")
	vnetB1 := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-vnet-b1", "Azure::Network::VirtualNetwork")
	vnetB2 := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-vnet-b2", "Azure::Network::VirtualNetwork")

	subnetManaged := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-subnet-managed", "Azure::Network::Subnet")
	subnetA1 := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-subnet-a1", "Azure::Network::Subnet")
	subnetB1 := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-subnet-b1", "Azure::Network::Subnet")
	subnetB2 := RequireResourceByNativeID(t, unmanaged, "formae-e2e-discovery-subnet-b2", "Azure::Network::Subnet")

	// Step 7: Verify VNets have resolvable reference to parent RG.
	knownRGNames := []string{"formae-e2e-discovery-managed-rg", "formae-e2e-discovery-rg-a", "formae-e2e-discovery-rg-b"}
	for _, vnet := range []Resource{vnetManaged, vnetA1, vnetB1, vnetB2} {
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
	for _, subnet := range []Resource{subnetManaged, subnetA1, subnetB1, subnetB2} {
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

	// Step 9: Extract unmanaged resources by type (separate files for each
	// Azure type to control import/destroy ordering and avoid pulling in
	// unrelated resources from other namespaces).
	extractDir := t.TempDir()
	rgFile := filepath.Join(extractDir, "resource-groups.pkl")
	vnetFile := filepath.Join(extractDir, "vnets.pkl")
	subnetFile := filepath.Join(extractDir, "subnets.pkl")
	cli.ExtractToFile(t, "type:Azure::Resources::ResourceGroup managed:false", rgFile)
	cli.ExtractToFile(t, "type:Azure::Network::VirtualNetwork managed:false", vnetFile)
	cli.ExtractToFile(t, "type:Azure::Network::Subnet managed:false", subnetFile)

	// Step 10: Filter extracted files to keep only test resources.
	FilterExtractedPKLByLabelSubstring(t, rgFile, nativeIDPrefix)
	FilterExtractedPKLByLabelSubstring(t, vnetFile, nativeIDPrefix)
	FilterExtractedPKLByLabelSubstring(t, subnetFile, nativeIDPrefix)

	// Step 11: Set stack labels in extracted files. Each type gets its own
	// stack so that reconcile mode doesn't try to delete cross-file resources.
	const rgStack = "e2e-discovery-azure-imported-rgs"
	const vnetStack = "e2e-discovery-azure-imported-vnets"
	const subnetStack = "e2e-discovery-azure-imported-subnets"
	SetExtractedStackLabel(t, rgFile, rgStack)
	SetExtractedStackLabel(t, vnetFile, vnetStack)
	SetExtractedStackLabel(t, subnetFile, subnetStack)

	// Step 12: Apply extracted files in dependency order (RGs first, then
	// VNets, then Subnets) to bring resources under management.
	rgCmdID := cli.Apply(t, "reconcile", rgFile)
	rgResult := cli.WaitForCommand(t, rgCmdID, commandTimeout)
	RequireCommandSuccess(t, rgResult)

	vnetCmdID := cli.Apply(t, "reconcile", vnetFile)
	vnetResult := cli.WaitForCommand(t, vnetCmdID, commandTimeout)
	RequireCommandSuccess(t, vnetResult)

	subnetCmdID := cli.Apply(t, "reconcile", subnetFile)
	subnetResult := cli.WaitForCommand(t, subnetCmdID, commandTimeout)
	RequireCommandSuccess(t, subnetResult)

	// Step 13: Verify imported test resources across all import stacks.
	importedRGs := cli.Inventory(t, "--query", "stack:"+rgStack)
	importedVNets := cli.Inventory(t, "--query", "stack:"+vnetStack)
	importedSubnets := cli.Inventory(t, "--query", "stack:"+subnetStack)
	var allImported []Resource
	allImported = append(allImported, importedRGs...)
	allImported = append(allImported, importedVNets...)
	allImported = append(allImported, importedSubnets...)
	importedTestResources := FilterByNativeIDContains(allImported, nativeIDPrefix)
	if len(importedTestResources) != 10 {
		t.Errorf("expected 10 test resources across import stacks, got %d", len(importedTestResources))
		for _, r := range importedTestResources {
			t.Logf("  imported: label=%s type=%s nativeID=%s", r.Label, r.Type, r.NativeID)
		}
	}

	// Step 14: Destroy imported resources in reverse dependency order
	// (subnets first, then vnets, then RGs).
	subnetDestroyID := cli.Destroy(t, subnetFile)
	subnetDestroyResult := cli.WaitForCommand(t, subnetDestroyID, commandTimeout)
	RequireCommandSuccess(t, subnetDestroyResult)

	vnetDestroyID := cli.Destroy(t, vnetFile)
	vnetDestroyResult := cli.WaitForCommand(t, vnetDestroyID, commandTimeout)
	RequireCommandSuccess(t, vnetDestroyResult)

	rgDestroyID := cli.Destroy(t, rgFile)
	rgDestroyResult := cli.WaitForCommand(t, rgDestroyID, commandTimeout)
	RequireCommandSuccess(t, rgDestroyResult)

	// Step 15: Destroy the original managed resources.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 16: Verify no test resources remain in any stack.
	remaining := cli.Inventory(t, "--query", "stack:e2e-discovery-azure")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources in stack e2e-discovery-azure after destroy, got %d", len(remaining))
	}
	for _, stack := range []string{rgStack, vnetStack, subnetStack} {
		stackRemaining := cli.Inventory(t, "--query", "stack:"+stack)
		stackRemainingTest := FilterByNativeIDContains(stackRemaining, nativeIDPrefix)
		if len(stackRemainingTest) != 0 {
			t.Errorf("expected 0 test resources in stack %s after destroy, got %d", stack, len(stackRemainingTest))
		}
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
