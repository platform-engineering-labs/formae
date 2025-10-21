// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e
// +build e2e

package ec2

import (
	"context"
	"encoding/json"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
)

func TestRoute_CreateAndDelete(t *testing.T) {
	t.Skip("Skipping Route because it requires an existing route table and gateway in AWS.")
	// Set these to valid values for your AWS environment
	routeTableID := "rtb-0c20c37af22a7a020"
	destinationCidrBlock := "10.0.0.0/24"
	gatewayID := "igw-0de5a8af83922bd8e"

	props := map[string]interface{}{
		"RouteTableId":         routeTableID,
		"DestinationCidrBlock": destinationCidrBlock,
		"GatewayId":            gatewayID,
	}
	propsBytes, err := json.Marshal(props)
	if err != nil {
		t.Fatalf("failed to marshal props: %v", err)
	}

	cfg := &config.Config{}
	route := &Route{cfg: cfg}

	// Create
	createReq := &resource.CreateRequest{
		Resource: &pkgmodel.Resource{
			Properties: propsBytes,
		},
	}
	createRes, err := route.Create(context.Background(), createReq)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if createRes == nil || createRes.ProgressResult == nil || createRes.ProgressResult.NativeID == "" {
		t.Fatalf("Create did not return a valid NativeID")
	}

	// Read
	readReq := &resource.ReadRequest{
		NativeID: createRes.ProgressResult.NativeID,
	}
	readRes, err := route.Read(context.Background(), readReq)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if readRes == nil || readRes.Properties == "" {
		t.Fatalf("Read did not return properties")
	}

	// Delete
	metaBytes, err := json.Marshal(props)
	if err != nil {
		t.Fatalf("failed to marshal metadata: %v", err)
	}
	deleteReq := &resource.DeleteRequest{
		Metadata: metaBytes,
	}
	deleteRes, err := route.Delete(context.Background(), deleteReq)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if deleteRes == nil || deleteRes.ProgressResult == nil || deleteRes.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("Delete did not succeed")
	}
}

func TestRoute_Delete(t *testing.T) {
	//t.Skip("Skipping Route Delete because it requires an existing route table and gateway in AWS.")
	// Set these to valid values for your AWS environment
	routeTableID := "rtb-04993e735431d3296"
	destinationCidrBlock := "0.0.0.0/0"
	gatewayID := "igw-07bd9a3a397fd2cf4"

	props := map[string]interface{}{
		"RouteTableId":         routeTableID,
		"DestinationCidrBlock": destinationCidrBlock,
		"GatewayId":            gatewayID,
	}

	metaBytes, err := json.Marshal(props)
	if err != nil {
		t.Fatalf("failed to marshal metadata: %v", err)
	}

	deleteReq := &resource.DeleteRequest{
		Metadata: metaBytes,
	}

	cfg := &config.Config{}
	route := &Route{cfg: cfg}

	_, err = route.Delete(context.Background(), deleteReq)
}

func TestRoute_Read(t *testing.T) {
	t.Skip("Skipping Route Read because it requires an existing route table and gateway in AWS.")
	// Test data from the comments
	routeTableID := "rtb-0e53ae2ac5ea71dc4"
	destinationCidrBlock := "0.0.0.0/0"
	gatewayID := "igw-09c441dbb44e8ec94"
	nativeID := "rtb-0e53ae2ac5ea71dc4|0.0.0.0/0|GatewayId=igw-09c441dbb44e8ec94"

	cfg := &config.Config{Region: "eu-central-1"}
	route := &Route{cfg: cfg}

	// Read the route
	readReq := &resource.ReadRequest{
		NativeID: nativeID,
	}

	readRes, err := route.Read(context.Background(), readReq)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if readRes == nil {
		t.Fatalf("Read returned nil result")
	}

	if readRes.ErrorCode == resource.OperationErrorCodeNotFound {
		t.Skip("Route not found in AWS - skipping test")
		return
	}

	if readRes.Properties == "" {
		t.Fatalf("Read did not return properties")
	}

	// Parse and validate the returned properties
	var props map[string]interface{}
	if err := json.Unmarshal([]byte(readRes.Properties), &props); err != nil {
		t.Fatalf("Failed to parse returned properties: %v", err)
	}

	// Validate expected properties
	if props["RouteTableId"] != routeTableID {
		t.Errorf("Expected RouteTableId %s, got %v", routeTableID, props["RouteTableId"])
	}

	if props["DestinationCidrBlock"] != destinationCidrBlock {
		t.Errorf("Expected DestinationCidrBlock %s, got %v", destinationCidrBlock, props["DestinationCidrBlock"])
	}

	if props["GatewayId"] != gatewayID {
		t.Errorf("Expected GatewayId %s, got %v", gatewayID, props["GatewayId"])
	}

	// Verify resource type
	if readRes.ResourceType != "AWS::EC2::Route" {
		t.Errorf("Expected ResourceType AWS::EC2::Route, got %s", readRes.ResourceType)
	}

	t.Logf("Successfully read route with properties: %s", readRes.Properties)
}
