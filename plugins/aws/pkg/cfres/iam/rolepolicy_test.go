// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package iam

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
)

func TestRolePolicy_List_Success(t *testing.T) {
	ctx := context.Background()
	client := &mockIAMClient{}
	client.On("ListRolePolicies", ctx, matchRoleAndNoMarker("role-1")).Return(
		&iam.ListRolePoliciesOutput{
			PolicyNames: []string{"policy-1", "policy-2"},
		}, nil,
	)

	rp := &RolePolicy{cfg: &config.Config{}}
	result, err := rp.listWithClient(ctx, client, &resource.ListRequest{
		ResourceType: "AWS::IAM::RolePolicy",
		PageSize:     10,
		AdditionalProperties: map[string]string{
			"RoleName": "role-1",
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, "AWS::IAM::RolePolicy", result.ResourceType)
	assert.Len(t, result.Resources, 2)
	assert.Nil(t, result.NextPageToken, "Should be done when no marker returned")

	assertResourceContains(t, result.Resources[0], "role-1", "policy-1")
	assertResourceContains(t, result.Resources[1], "role-1", "policy-2")

	client.AssertExpectations(t)
}

func TestRolePolicy_List_WithPagination(t *testing.T) {
	ctx := context.Background()
	client := &mockIAMClient{}

	client.On("ListRolePolicies", ctx, matchRoleAndNoMarker("role-1")).Return(
		&iam.ListRolePoliciesOutput{
			PolicyNames: []string{"policy-1", "policy-2"},
			Marker:      stringPtr("next-page-marker"),
		}, nil,
	)

	rp := &RolePolicy{cfg: &config.Config{}}
	result, err := rp.listWithClient(ctx, client, &resource.ListRequest{
		ResourceType: "AWS::IAM::RolePolicy",
		PageSize:     2,
		AdditionalProperties: map[string]string{
			"RoleName": "role-1",
		},
	})

	assert.NoError(t, err)
	assert.Len(t, result.Resources, 2)
	assert.NotNil(t, result.NextPageToken, "missing next token when marker returned")
	assert.Equal(t, "next-page-marker", *result.NextPageToken)

	client.AssertExpectations(t)
}

func TestRolePolicy_List_ContinuePagination(t *testing.T) {
	ctx := context.Background()
	client := &mockIAMClient{}

	pageToken := "continue-from-here"
	client.On("ListRolePolicies", ctx, matchRoleAndMarker("role-1", pageToken)).Return(
		&iam.ListRolePoliciesOutput{
			PolicyNames: []string{"policy-3", "policy-4"},
		}, nil,
	)

	rp := &RolePolicy{cfg: &config.Config{}}
	result, err := rp.listWithClient(ctx, client, &resource.ListRequest{
		ResourceType: "AWS::IAM::RolePolicy",
		PageSize:     2,
		PageToken:    &pageToken,
		AdditionalProperties: map[string]string{
			"RoleName": "role-1",
		},
	})

	assert.NoError(t, err)
	assert.Len(t, result.Resources, 2)
	assert.Nil(t, result.NextPageToken, "not nil when no marker returned")

	assertResourceContains(t, result.Resources[0], "role-1", "policy-3")
	assertResourceContains(t, result.Resources[1], "role-1", "policy-4")

	client.AssertExpectations(t)
}

func TestRolePolicy_List_MissingRoleName(t *testing.T) {
	ctx := context.Background()
	rp := &RolePolicy{cfg: &config.Config{}}

	result, err := rp.List(ctx, &resource.ListRequest{
		PageSize: 10,
		// No RoleName
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "rolename required")
}

func TestRolePolicy_List_EmptyRoleName(t *testing.T) {
	ctx := context.Background()
	rp := &RolePolicy{cfg: &config.Config{}}

	result, err := rp.List(ctx, &resource.ListRequest{
		PageSize: 10,
		AdditionalProperties: map[string]string{
			"RoleName": "",
		},
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "rolename must be provided")
}

func TestRolePolicy_List_NoPermissions(t *testing.T) {
	ctx := context.Background()
	client := &mockIAMClient{}

	client.On("ListRolePolicies", ctx, matchRoleAndNoMarker("role-1")).Return(
		(*iam.ListRolePoliciesOutput)(nil), fmt.Errorf("mock error"),
	)

	rp := &RolePolicy{cfg: &config.Config{}}
	result, err := rp.listWithClient(ctx, client, &resource.ListRequest{
		ResourceType: "AWS::IAM::RolePolicy",
		PageSize:     10,
		AdditionalProperties: map[string]string{
			"RoleName": "role-1",
		},
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to list role policies for role role-1")

	client.AssertExpectations(t)
}

func stringPtr(s string) *string {
	return &s
}

func assertResourceContains(t *testing.T, res resource.Resource, wantRole, wantPolicy string) {
	var props map[string]any
	err := json.Unmarshal([]byte(res.Properties), &props)
	assert.NoError(t, err)

	assert.Equal(t, wantPolicy, props["PolicyName"])
	assert.Equal(t, wantRole, props["RoleName"])
	assert.Equal(t, fmt.Sprintf("%s|%s", wantPolicy, wantRole), res.NativeID)
}
