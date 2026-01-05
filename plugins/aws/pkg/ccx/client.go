// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ccx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	cctypes "github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"

	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
	helper "github.com/platform-engineering-labs/formae/plugins/aws/pkg/helper"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/props"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/ptr"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/status"
)

type Client struct {
	*cloudcontrol.Client
}

var IgnoredFields = map[string][]string{
	"AWS::EC2::SecurityGroup": {"$.SecurityGroupEgress", "$.SecurityGroupIngress"},
	"AWS::IAM::Role":          {"$.Policies"},
}

func NewClient(cfg *config.Config) (*Client, error) {
	awsCfg, err := cfg.ToAwsConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	// Create Cloud Control Client with custom retry configuration for throttling.
	// AWS CloudControl API has strict rate limits, so we use:
	// - Fewer max attempts (let PluginOperator handle retries at a higher level)
	// - Longer max backoff to give AWS time to recover from throttling
	retryer := retry.NewStandard(func(o *retry.StandardOptions) {
		o.MaxAttempts = 2            // Reduce from default 3 to fail faster to PluginOperator
		o.MaxBackoff = 30 * time.Second // Allow longer backoff for throttling
	})

	return &Client{
		Client: cloudcontrol.NewFromConfig(awsCfg, func(o *cloudcontrol.Options) {
			o.Retryer = retryer
		}),
	}, nil
}

// CreateResource creates a resource using CloudControl with full request handling
func (c *Client) CreateResource(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	resourceProps := request.Resource.Properties

	// Handle map tags transformation if required
	if props.RequiresMapTags(request.Resource.Type) {
		var properties map[string]any
		if err := json.Unmarshal(request.Resource.Properties, &properties); err != nil {
			return nil, err
		}

		if err := props.TransformTagsToMap(properties); err != nil {
			return nil, err
		}

		transformedProps, err := json.Marshal(properties)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal transformed properties: %w", err)
		}
		resourceProps = transformedProps
	}

	result, err := c.Client.CreateResource(ctx, &cloudcontrol.CreateResourceInput{
		DesiredState: ptr.Of(string(resourceProps)),
		TypeName:     &request.Resource.Type,
	})
	if err != nil {
		return nil, err
	}

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: status.FromOperationStatus(result.ProgressEvent.OperationStatus),
			RequestID:       *result.ProgressEvent.RequestToken,
			StatusMessage:   aws.ToString(result.ProgressEvent.StatusMessage),
			ErrorCode:       resource.OperationErrorCode(result.ProgressEvent.ErrorCode),
			ResourceType:    request.Resource.Type,
		},
	}, nil
}

// UpdateResource updates a resource using CloudControl with full request handling
func (c *Client) UpdateResource(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	// Check if resource exists first
	_, err := c.GetResource(ctx, &cloudcontrol.GetResourceInput{
		Identifier: request.NativeID,
		TypeName:   &request.Resource.Type,
	})
	if err != nil {
		return nil, err
	}

	// For resources where tags are maps, we do not support updates with patch documents
	if props.RequiresMapTags(request.Resource.Type) && request.PatchDocument != nil {
		errMsg := "Update operations for resources with map tags are not supported"
		return &resource.UpdateResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationUpdate,
				OperationStatus: resource.OperationStatusFailure,
				StatusMessage:   errMsg,
				ErrorCode:       resource.OperationErrorCodeInternalFailure,
			},
		}, errors.New(errMsg)
	}

	patchDoc := request.PatchDocument
	if request.Resource.Type == "AWS::SecretsManager::Secret" && patchDoc != nil {
		transformedPatch, err := transformSecretStringPatch([]byte(*patchDoc))
		if err != nil {
			return nil, fmt.Errorf("failed to transform SecretString patch: %w", err)
		}
		patchDoc = ptr.Of(string(transformedPatch))
	}

	result, err := c.Client.UpdateResource(ctx, &cloudcontrol.UpdateResourceInput{
		Identifier:    request.NativeID,
		PatchDocument: patchDoc,
		TypeName:      ptr.Of(request.Resource.Type),
	})
	if err != nil {
		return nil, err
	}

	return &resource.UpdateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationUpdate,
			OperationStatus: status.FromOperationStatus(result.ProgressEvent.OperationStatus),
			RequestID:       *result.ProgressEvent.RequestToken,
			StatusMessage:   aws.ToString(result.ProgressEvent.StatusMessage),
			ErrorCode:       resource.OperationErrorCode(result.ProgressEvent.ErrorCode),
			ResourceType:    request.Resource.Type,
		},
	}, nil
}

// DeleteResource deletes a resource using CloudControl with full request handling
func (c *Client) DeleteResource(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	result, err := c.Client.DeleteResource(ctx, &cloudcontrol.DeleteResourceInput{
		Identifier: request.NativeID,
		TypeName:   ptr.Of(request.ResourceType),
	})
	if err != nil {
		return nil, err
	}

	// If the resource is not found, we return a success status
	if result.ProgressEvent.ErrorCode == cctypes.HandlerErrorCodeNotFound {
		return &resource.DeleteResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: status.FromOperationStatus(cctypes.OperationStatusSuccess),
				RequestID:       *result.ProgressEvent.RequestToken,
				StatusMessage:   aws.ToString(result.ProgressEvent.StatusMessage),
				ErrorCode:       resource.OperationErrorCode(result.ProgressEvent.ErrorCode),
				ResourceType:    request.ResourceType,
			},
		}, nil
	}

	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: status.FromOperationStatus(result.ProgressEvent.OperationStatus),
			RequestID:       *result.ProgressEvent.RequestToken,
			StatusMessage:   aws.ToString(result.ProgressEvent.StatusMessage),
			ErrorCode:       resource.OperationErrorCode(result.ProgressEvent.ErrorCode),
			ResourceType:    request.ResourceType,
		},
	}, nil
}

// ReadResource reads a resource using CloudControl with full request handling
func (c *Client) ReadResource(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	result, err := c.GetResource(ctx, &cloudcontrol.GetResourceInput{
		Identifier: &request.NativeID,
		TypeName:   ptr.Of(request.ResourceType),
	})
	if err != nil {
		errorCode, isCloudControlError := helper.HandleCloudControlError(err)
		if isCloudControlError {
			return &resource.ReadResult{
				ResourceType: request.ResourceType,
				ErrorCode:    resource.OperationErrorCode(errorCode),
			}, nil
		}
		return nil, err
	}

	properties := *result.ResourceDescription.Properties
	var propsMap map[string]any
	if err = json.Unmarshal([]byte(properties), &propsMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resource properties: %w", err)
	}

	if props.RequiresMapTags(request.ResourceType) {
		if err = props.TransformTagsToArray(propsMap); err != nil {
			return nil, fmt.Errorf("failed to transform tags: %w", err)
		}
	}

	if err = stripIgnoredFields(propsMap, IgnoredFields[request.ResourceType]); err != nil {
		return nil, fmt.Errorf("failed to strip ignored fields: %w", err)
	}

	transformedProps, err := json.Marshal(propsMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transformed properties: %w", err)
	}

	properties = string(transformedProps)

	return &resource.ReadResult{
		ResourceType: *result.TypeName,
		Properties:   properties,
	}, nil
}

// StatusResource gets the status of a resource request using CloudControl with full request handling
func (c *Client) StatusResource(ctx context.Context, request *resource.StatusRequest, readFunc func(context.Context, *resource.ReadRequest) (*resource.ReadResult, error)) (*resource.StatusResult, error) {
	result, err := c.GetResourceRequestStatus(ctx, &cloudcontrol.GetResourceRequestStatusInput{
		RequestToken: &request.RequestID,
	})
	if err != nil {
		return nil, err
	}

	operation, operationStatus := status.FromProgress(result.ProgressEvent)
	identifier := ""
	if result.ProgressEvent.Identifier != nil {
		identifier = *result.ProgressEvent.Identifier
	}

	// If the resource is not found, we return a success status when it is a delete operation
	if result.ProgressEvent.Operation == cctypes.OperationDelete && result.ProgressEvent.ErrorCode == cctypes.HandlerErrorCodeNotFound {
		return &resource.StatusResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       operation,
				OperationStatus: resource.OperationStatusSuccess,
				RequestID:       request.RequestID,
				NativeID:        identifier,
				ResourceType:    *result.ProgressEvent.TypeName,
				StatusMessage:   aws.ToString(result.ProgressEvent.StatusMessage),
				ErrorCode:       resource.OperationErrorCode(result.ProgressEvent.ErrorCode)},
		}, nil
	}

	statusResult := &resource.StatusResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       operation,
			OperationStatus: operationStatus,
			RequestID:       request.RequestID,
			NativeID:        identifier,
			ResourceType:    aws.ToString(result.ProgressEvent.TypeName),
			StatusMessage:   aws.ToString(result.ProgressEvent.StatusMessage),
			ErrorCode:       resource.OperationErrorCode(result.ProgressEvent.ErrorCode),
		},
	}

	// If operation status is success, run a Read to get the latest properties
	if operationStatus == resource.OperationStatusSuccess && result.ProgressEvent.Operation != cctypes.OperationDelete {
		readResult, readErr := readFunc(ctx, &resource.ReadRequest{
			NativeID:     identifier,
			ResourceType: *result.ProgressEvent.TypeName,
			Target:       request.Target,
		})
		if readErr == nil && readResult != nil {
			statusResult.ProgressResult.ResourceProperties = json.RawMessage(readResult.Properties)
		}
	}

	return statusResult, nil
}

// ListResources lists resources using CloudControl
func (c *Client) ListResources(ctx context.Context, input *cloudcontrol.ListResourcesInput) (*cloudcontrol.ListResourcesOutput, error) {
	return c.Client.ListResources(ctx, input)
}

// transformSecretStringPatch transforms replace operations to add operations for SecretString
// AWS CloudControl requires writeOnlyProperties like SecretString to use 'add' operation
func transformSecretStringPatch(patchDoc []byte) ([]byte, error) {
	if len(patchDoc) == 0 {
		return patchDoc, nil
	}

	var patches []map[string]any
	if err := json.Unmarshal(patchDoc, &patches); err != nil {
		return patchDoc, err
	}

	modified := false
	for i, patch := range patches {
		if op, ok := patch["op"].(string); ok && op == "replace" {
			if path, ok := patch["path"].(string); ok && path == "/SecretString" {
				patches[i]["op"] = "add"
				modified = true
			}
		}
	}

	if !modified {
		return patchDoc, nil
	}

	return json.Marshal(patches)
}

func stripIgnoredFields(data map[string]any, fields []string) error {
	for _, field := range fields {
		if strings.HasPrefix(field, "$") {
			field = strings.TrimPrefix(field, "$")
			field = strings.TrimPrefix(field, ".")
		}

		components := strings.Split(field, ".")
		if len(components) == 0 {
			continue
		}

		parent, keyToRemove, err := findParentAndKey(data, components)
		if err != nil {
			return err
		}

		if parentMap, ok := parent.(map[string]any); ok {
			delete(parentMap, keyToRemove)
		}
	}
	return nil
}

func findParentAndKey(data map[string]any, components []string) (any, string, error) {
	current := data

	for _, key := range components[:len(components)-1] {
		if next, found := current[key]; found {
			if m, ok := next.(map[string]any); ok {
				current = m
				continue
			}
		}
		return nil, "", fmt.Errorf("path not found: '%s'", key)
	}

	keyToRemove := components[len(components)-1]
	return current, keyToRemove, nil
}
