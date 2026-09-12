//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package aurora

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
)

func (d *DatastoreAuroraDataAPI) SetPolicyTypeForTesting(label, policyType string) error {
	_, err := d.executeStatement(context.Background(), `UPDATE policies SET policy_type=:policy_type WHERE label=:label`, []types.SqlParameter{
		{Name: aws.String("policy_type"), Value: &types.FieldMemberStringValue{Value: policyType}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
	})
	return err
}
