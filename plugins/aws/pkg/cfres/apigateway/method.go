// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package apigateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/ccx"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/prov"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/registry"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
)

type Method struct {
	cfg *config.Config
}

var _ prov.Provisioner = &Method{}

func init() {
	registry.Register("AWS::ApiGateway::Method",
		[]resource.Operation{
			resource.OperationRead,
			resource.OperationCreate,
			resource.OperationUpdate,
			resource.OperationDelete},
		func(cfg *config.Config) prov.Provisioner {
			return &Method{cfg: cfg}
		})
}

func (m *Method) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	transformedProperties, err := m.handleLambdaIntegration(request.Resource.Properties)
	if err != nil {
		slog.Error("ApiGateway::Method: Failed to transform Lambda integration", "error", err)
		return nil, err
	}

	request.Resource.Properties = transformedProperties

	ccxClient, err := ccx.NewClient(m.cfg)
	if err != nil {
		return nil, err
	}

	return ccxClient.CreateResource(ctx, request)
}

func (m *Method) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	transformedProperties, err := m.handleLambdaIntegration(request.Resource.Properties)
	if err != nil {
		return nil, err
	}
	request.Resource.Properties = transformedProperties

	ccxClient, err := ccx.NewClient(m.cfg)
	if err != nil {
		return nil, err
	}
	return ccxClient.UpdateResource(ctx, request)
}

func (m *Method) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	ccxClient, err := ccx.NewClient(m.cfg)
	if err != nil {
		return nil, err
	}
	return ccxClient.ReadResource(ctx, request)
}

func (m *Method) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	ccxClient, err := ccx.NewClient(m.cfg)
	if err != nil {
		return nil, err
	}
	return ccxClient.DeleteResource(ctx, request)
}

func (m *Method) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	ccxClient, err := ccx.NewClient(m.cfg)
	if err != nil {
		return nil, err
	}
	return ccxClient.StatusResource(ctx, request, m.Read)
}

func (m *Method) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	return nil, fmt.Errorf("apiGateway::Method: list operation not supported")
}

// handleLambdaIntegration transforms the Lambda integration properties for API Gateway.
// This solves the issue of using Lambda ARNs directly in API Gateway integrations and
// allows the API Gateway to $ref the Lambda function.
func (m *Method) handleLambdaIntegration(properties []byte) ([]byte, error) {
	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return properties, err
	}

	integration, ok := props["Integration"].(map[string]any)
	if !ok {
		return properties, nil
	}

	lambdaArn, hasLambdaArn := integration["LambdaFunctionArn"]
	if !hasLambdaArn {
		return properties, nil
	}

	lambdaArnStr, ok := lambdaArn.(string)
	if !ok {
		return nil, fmt.Errorf("expected LambdaFunctionArn to be resolved string, got %T", lambdaArn)
	}

	region, err := m.extractRegionFromLambdaArn(lambdaArnStr)
	if err != nil {
		return nil, fmt.Errorf("failed to extract region from Lambda ARN: %w", err)
	}

	uri := fmt.Sprintf("arn:aws:apigateway:%s:lambda:path/2015-03-31/functions/%s/invocations",
		region, lambdaArnStr)
	delete(integration, "LambdaFunctionArn")
	integration["Uri"] = uri

	transformedProps, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transformed properties: %w", err)
	}

	slog.Debug("ApiGateway::Method: Transformed Lambda integration",
		"lambdaArn", lambdaArnStr, "uri", uri)

	return transformedProps, nil
}

func (m *Method) extractRegionFromLambdaArn(arn string) (string, error) {

	// Note: Lambda ARN format = arn:aws:lambda:region:account:function:name
	parts := strings.Split(arn, ":")
	if len(parts) >= 4 && parts[0] == "arn" && parts[1] == "aws" && parts[2] == "lambda" {
		return parts[3], nil
	}
	return "", fmt.Errorf("invalid Lambda ARN format: %s", arn)
}
