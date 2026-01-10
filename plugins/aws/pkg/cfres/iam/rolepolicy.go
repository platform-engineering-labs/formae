// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package iam

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/prov"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/registry"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
)

type RolePolicy struct {
	cfg *config.Config
}

type iamClientInterface interface {
	ListRolePolicies(ctx context.Context, params *iam.ListRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error)
}

var _ prov.Provisioner = &RolePolicy{}

func init() {
	registry.Register("AWS::IAM::RolePolicy", []resource.Operation{resource.OperationList}, func(cfg *config.Config) prov.Provisioner {
		return &RolePolicy{cfg: cfg}
	})
}

func (r *RolePolicy) List(ctx context.Context, request *resource.ListRequest) (result *resource.ListResult, err error) {
	cfg, err := r.cfg.ToAwsConfig(ctx)
	if err != nil {
		slog.Error("Failed to load AWS config", "error", err)
		return nil, fmt.Errorf("unable to load aws config: %w", err)
	}
	client := iam.NewFromConfig(cfg)

	return r.listWithClient(ctx, client, request)
}

// listWithClient allows for DI of the IAM client for testing
func (r *RolePolicy) listWithClient(ctx context.Context, client iamClientInterface, request *resource.ListRequest) (*resource.ListResult, error) {
	if request == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	if request.AdditionalProperties == nil {
		return nil, fmt.Errorf("rolename required for listing role policies")
	}

	roleName, ok := request.AdditionalProperties["RoleName"]
	if !ok || roleName == "" {
		return nil, fmt.Errorf("rolename must be provided in additional properties for listing role policies")
	}

	pageSize := request.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}

	input := &iam.ListRolePoliciesInput{
		RoleName: &roleName,
		MaxItems: &pageSize,
	}

	if request.PageToken != nil && *request.PageToken != "" {
		input.Marker = request.PageToken
	}

	res, err := client.ListRolePolicies(ctx, input)
	if err != nil {
		// If the role doesn't exist (deleted during destroy), return an empty list
		var noSuchEntity *iamtypes.NoSuchEntityException
		if errors.As(err, &noSuchEntity) {
			return &resource.ListResult{
				NativeIDs:     []string{},
				NextPageToken: nil,
			}, nil
		}
		return nil, fmt.Errorf("failed to list role policies for role %s: %w", roleName, err)
	}

	var nativeIDs []string
	for _, policyName := range res.PolicyNames {
		nativeIDs = append(nativeIDs, fmt.Sprintf("%s|%s", policyName, roleName))
	}

	return &resource.ListResult{
		NativeIDs:     nativeIDs,
		NextPageToken: res.Marker,
	}, nil
}

func (r *RolePolicy) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	return nil, fmt.Errorf("create not implemented - cloudcontrol handles this operation")
}

func (r *RolePolicy) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return nil, fmt.Errorf("update not implemented - cloudcontrol handles this operation")
}

func (r *RolePolicy) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	return nil, fmt.Errorf("delete not implemented - cloudcontrol handles this operation")
}

func (r *RolePolicy) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	return nil, fmt.Errorf("status not implemented - cloudcontrol handles this operation")
}

func (r *RolePolicy) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	return nil, fmt.Errorf("read not implemented - cloudcontrol handles this operation")
}
