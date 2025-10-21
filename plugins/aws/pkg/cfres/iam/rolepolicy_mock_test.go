// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package iam

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/stretchr/testify/mock"
)

type mockIAMClient struct {
	mock.Mock
}

func (m *mockIAMClient) ListRolePolicies(ctx context.Context, input *iam.ListRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	args := m.Called(ctx, input)
	return args.Get(0).(*iam.ListRolePoliciesOutput), args.Error(1)
}

func matchRoleAndNoMarker(roleName string) any {
	return mock.MatchedBy(func(input *iam.ListRolePoliciesInput) bool {
		return input.RoleName != nil && *input.RoleName == roleName && input.Marker == nil
	})
}

func matchRoleAndMarker(roleName, marker string) any {
	return mock.MatchedBy(func(input *iam.ListRolePoliciesInput) bool {
		return input.RoleName != nil && *input.RoleName == roleName &&
			input.Marker != nil && *input.Marker == marker
	})
}
