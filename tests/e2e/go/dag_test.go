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

// TestDAG verifies that formae correctly handles a complex dependency graph
// with 7 resources across multiple EC2 types. The DAG exercises:
//
//	VPC (root)
//	├── Subnet → VPC
//	├── RouteTable → VPC
//	├── VPCGatewayAttachment → VPC, IGW
//	InternetGateway (independent root)
//	├── VPCGatewayAttachment → VPC, IGW
//	├── Route → RouteTable, IGW
//	SubnetRouteTableAssociation → Subnet, RouteTable
//
// This tests that formae resolves cross-resource references and orders
// create/destroy operations correctly across a real dependency graph.
func TestDAG(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	t.Run("AWS", func(t *testing.T) { testDAGAWS(t, cli) })
}

func testDAGAWS(t *testing.T, cli *FormaeCLI) {
	fixture := filepath.Join(fixturesDir(t), "dag_aws.pkl")
	commandTimeout := 5 * time.Minute

	// Step 1: Apply the full DAG.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify all 7 resources were created.
	resources := cli.Inventory(t, "--query", "stack:e2e-dag-aws")
	if len(resources) != 7 {
		t.Fatalf("expected 7 resources, got %d", len(resources))
	}

	// Step 3: Verify each resource exists with correct type.
	vpc := RequireResource(t, resources, "e2e-dag-vpc")
	if vpc.Type != "AWS::EC2::VPC" {
		t.Errorf("expected VPC type, got %s", vpc.Type)
	}
	AssertStringProperty(t, vpc, "CidrBlock", "10.99.0.0/16")

	subnet := RequireResource(t, resources, "e2e-dag-subnet")
	if subnet.Type != "AWS::EC2::Subnet" {
		t.Errorf("expected Subnet type, got %s", subnet.Type)
	}
	AssertStringProperty(t, subnet, "CidrBlock", "10.99.1.0/24")

	rt := RequireResource(t, resources, "e2e-dag-rt")
	if rt.Type != "AWS::EC2::RouteTable" {
		t.Errorf("expected RouteTable type, got %s", rt.Type)
	}

	igw := RequireResource(t, resources, "e2e-dag-igw")
	if igw.Type != "AWS::EC2::InternetGateway" {
		t.Errorf("expected InternetGateway type, got %s", igw.Type)
	}

	attachment := RequireResource(t, resources, "e2e-dag-igw-attachment")
	if attachment.Type != "AWS::EC2::VPCGatewayAttachment" {
		t.Errorf("expected VPCGatewayAttachment type, got %s", attachment.Type)
	}

	route := RequireResource(t, resources, "e2e-dag-route")
	if route.Type != "AWS::EC2::Route" {
		t.Errorf("expected Route type, got %s", route.Type)
	}
	AssertStringProperty(t, route, "DestinationCidrBlock", "0.0.0.0/0")

	assoc := RequireResource(t, resources, "e2e-dag-subnet-assoc")
	if assoc.Type != "AWS::EC2::SubnetRouteTableAssociation" {
		t.Errorf("expected SubnetRouteTableAssociation type, got %s", assoc.Type)
	}

	// Step 4: Verify cross-resource references are resolved.
	// Subnet.VpcId should be a resolvable pointing to the VPC.
	AssertResolvableProperty(t, subnet, "VpcId", "AWS::EC2::VPC")
	// RouteTable.VpcId should reference the VPC.
	AssertResolvableProperty(t, rt, "VpcId", "AWS::EC2::VPC")
	// Route.RouteTableId should reference the RouteTable.
	AssertResolvableProperty(t, route, "RouteTableId", "AWS::EC2::RouteTable")
	// Route.GatewayId should reference the IGW.
	AssertResolvableProperty(t, route, "GatewayId", "AWS::EC2::InternetGateway")
	// SubnetRouteTableAssociation.SubnetId should reference the Subnet.
	AssertResolvableProperty(t, assoc, "SubnetId", "AWS::EC2::Subnet")
	// SubnetRouteTableAssociation.RouteTableId should reference the RouteTable.
	AssertResolvableProperty(t, assoc, "RouteTableId", "AWS::EC2::RouteTable")

	// Step 5: Destroy — this tests reverse dependency ordering.
	// Resources with dependents must be destroyed last (VPC, IGW).
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 6: Verify cleanup.
	remaining := cli.Inventory(t, "--query", "stack:e2e-dag-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}
