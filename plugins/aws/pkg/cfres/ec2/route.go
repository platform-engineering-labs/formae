// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ec2

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/prov"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/registry"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/utils"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
)

type Route struct {
	cfg *config.Config
}

var _ prov.Provisioner = &Route{}

func init() {
	registry.Register("AWS::EC2::Route",
		[]resource.Operation{
			resource.OperationRead,
			resource.OperationCreate,
			resource.OperationUpdate,
			resource.OperationCheckStatus,
			resource.OperationDelete},
		func(cfg *config.Config) prov.Provisioner {
			return &Route{cfg: cfg}
		})
}

func buildNativeID(props map[string]any) (string, string, error) {
	routeTableID, err := utils.GetStringProperty(props, "RouteTableId")
	if err != nil {
		return "", "", fmt.Errorf("invalid RouteTableId: %w", err)
	}
	destinationCidrBlock, err := utils.GetStringProperty(props, "DestinationCidrBlock")
	if err != nil {
		return "", "", fmt.Errorf("invalid DestinationCidrBlock: %w", err)
	}

	targetKeys := []string{
		"GatewayId",
		"NatGatewayId",
		"NetworkInterfaceId",
		"InstanceId",
		"TransitGatewayId",
		"VpcEndpointId",
		"VpcPeeringConnectionId",
	}
	var targetKey, targetValue string
	for _, key := range targetKeys {
		if val, _ := utils.GetStringProperty(props, key); val != "" {
			if targetKey != "" {
				return "", "", fmt.Errorf("multiple route targets set: %s and %s", targetKey, key)
			}
			targetKey = key
			targetValue = val
		}
	}
	if targetKey == "" {
		return "", "", fmt.Errorf("no route target set")
	}
	nativeID := fmt.Sprintf("%s|%s|%s=%s", routeTableID, destinationCidrBlock, targetKey, targetValue)
	return nativeID, targetKey, nil
}

func (r Route) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	cfg, err := r.cfg.ToAwsConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %w", err)
	}
	client := ec2.NewFromConfig(cfg)

	var props map[string]any
	if err := json.Unmarshal(request.DesiredState.Properties, &props); err != nil {
		return nil, fmt.Errorf("failed to parse properties: %w", err)
	}

	routeTableID, err := utils.GetStringProperty(props, "RouteTableId")
	if err != nil {
		return nil, fmt.Errorf("invalid RouteTableId: %w", err)
	}
	destinationCidrBlock, err := utils.GetStringProperty(props, "DestinationCidrBlock")
	if err != nil {
		return nil, fmt.Errorf("invalid DestinationCidrBlock: %w", err)
	}

	input := &ec2.CreateRouteInput{
		RouteTableId:         aws.String(routeTableID),
		DestinationCidrBlock: aws.String(destinationCidrBlock),
	}

	// Optional targets
	if gw, _ := utils.GetStringProperty(props, "GatewayId"); gw != "" {
		input.GatewayId = aws.String(gw)
	}
	if nat, _ := utils.GetStringProperty(props, "NatGatewayId"); nat != "" {
		input.NatGatewayId = aws.String(nat)
	}
	if eni, _ := utils.GetStringProperty(props, "NetworkInterfaceId"); eni != "" {
		input.NetworkInterfaceId = aws.String(eni)
	}
	if instance, _ := utils.GetStringProperty(props, "InstanceId"); instance != "" {
		input.InstanceId = aws.String(instance)
	}
	if transit, _ := utils.GetStringProperty(props, "TransitGatewayId"); transit != "" {
		input.TransitGatewayId = aws.String(transit)
	}
	if vpce, _ := utils.GetStringProperty(props, "VpcEndpointId"); vpce != "" {
		input.VpcEndpointId = aws.String(vpce)
	}
	if vpcPeering, _ := utils.GetStringProperty(props, "VpcPeeringConnectionId"); vpcPeering != "" {
		input.VpcPeeringConnectionId = aws.String(vpcPeering)
	}

	_, err = client.CreateRoute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to create route: %w", err)
	}

	nativeID, _, err := buildNativeID(props)
	if err != nil {
		return nil, err
	}

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        nativeID,
		},
	}, nil
}

func (r Route) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	// EC2 Route resources cannot be updated in-place; must delete and recreate.
	return nil, fmt.Errorf("update is not supported for AWS::EC2::Route resources; delete and recreate instead")
}

func (r Route) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	cfg, err := r.cfg.ToAwsConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %w", err)
	}
	client := ec2.NewFromConfig(cfg)

	readRes, err := r.Read(ctx, &resource.ReadRequest{
		NativeID: *request.NativeID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read route before delete: %w", err)
	}
	if readRes.ErrorCode == resource.OperationErrorCodeNotFound {
		// Route does not exist, nothing to delete
		return &resource.DeleteResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusSuccess,
				NativeID:        *request.NativeID,
			},
		}, nil
	}

	parts := strings.SplitN(*request.NativeID, "|", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid NativeID format: expected RouteTableId|DestinationCidrBlock|target, got: %s", *request.NativeID)
	}

	routeTableID := parts[0]
	destinationCidrBlock := parts[1]

	input := &ec2.DeleteRouteInput{
		RouteTableId:         aws.String(routeTableID),
		DestinationCidrBlock: aws.String(destinationCidrBlock),
	}

	_, err = client.DeleteRoute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to delete route: %w", err)
	}

	nativeID := fmt.Sprintf("%s|%s", routeTableID, destinationCidrBlock)
	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        nativeID,
		},
	}, nil
}

func (r Route) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	return nil, fmt.Errorf("status check is not implemented for AWS::EC2::Route resources")
}

func (r Route) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	cfg, err := r.cfg.ToAwsConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %w", err)
	}
	client := ec2.NewFromConfig(cfg)

	// Parse NativeID
	var routeTableID, destinationCidrBlock string

	parts := strings.SplitN(request.NativeID, "|", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid NativeID format: expected RouteTableId|DestinationCidrBlock|target, got: %s", request.NativeID)
	}

	routeTableID = parts[0]
	destinationCidrBlock = parts[1]
	//target = parts[2]

	resp, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		RouteTableIds: []string{routeTableID},
	})

	if err != nil {
		//return nil, fmt.Errorf("failed to describe route table: %w", err)
		return &resource.ReadResult{
			ResourceType: "AWS::EC2::Route",
			ErrorCode:    resource.OperationErrorCodeNotFound,
		}, nil
	}
	if len(resp.RouteTables) == 0 {
		//return nil, fmt.Errorf("route table %s not found", routeTableID)
		return &resource.ReadResult{
			ResourceType: "AWS::EC2::Route",
			ErrorCode:    resource.OperationErrorCodeNotFound,
		}, nil
	}

	var matchedRoute ec2types.Route
	found := false
	for _, route := range resp.RouteTables[0].Routes {
		if route.DestinationCidrBlock != nil && *route.DestinationCidrBlock == destinationCidrBlock {
			matchedRoute = route
			found = true
			break
		}
	}
	if !found {
		//return nil, fmt.Errorf("route for %s not found in route table %s", destinationCidrBlock, routeTableID)
		return &resource.ReadResult{
			ResourceType: "AWS::EC2::Route",
			ErrorCode:    resource.OperationErrorCodeNotFound,
		}, nil
	}

	// Build properties map
	props := map[string]any{
		"RouteTableId":         routeTableID,
		"DestinationCidrBlock": destinationCidrBlock,
	}

	// Add the target (only one is allowed)
	switch {
	case matchedRoute.GatewayId != nil:
		props["GatewayId"] = *matchedRoute.GatewayId
	case matchedRoute.NatGatewayId != nil:
		props["NatGatewayId"] = *matchedRoute.NatGatewayId
	case matchedRoute.NetworkInterfaceId != nil:
		props["NetworkInterfaceId"] = *matchedRoute.NetworkInterfaceId
	case matchedRoute.InstanceId != nil:
		props["InstanceId"] = *matchedRoute.InstanceId
	case matchedRoute.TransitGatewayId != nil:
		props["TransitGatewayId"] = *matchedRoute.TransitGatewayId
	case matchedRoute.VpcPeeringConnectionId != nil:
		props["VpcPeeringConnectionId"] = *matchedRoute.VpcPeeringConnectionId
	}

	propBytes, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal route properties: %w", err)
	}

	return &resource.ReadResult{
		ResourceType: "AWS::EC2::Route",
		Properties:   string(propBytes),
	}, nil
}

func (r Route) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	// Future feature
	return &resource.ListResult{
		Resources: []resource.Resource{},
	}, nil
}
