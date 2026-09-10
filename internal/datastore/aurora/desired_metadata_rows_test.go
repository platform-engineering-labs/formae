//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package aurora

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/rdsdata"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
	"github.com/stretchr/testify/require"
)

type malformedDesiredMetadataClient struct {
	auroraDataAPIClient
	kind string
	row  []types.Field
}

func (c *malformedDesiredMetadataClient) ExecuteStatement(_ context.Context, in *rdsdata.ExecuteStatementInput, _ ...func(*rdsdata.Options)) (*rdsdata.ExecuteStatementOutput, error) {
	if strings.Contains(*in.Sql, "SELECT id, description, operation FROM stacks") {
		return &rdsdata.ExecuteStatementOutput{Records: [][]types.Field{{&types.FieldMemberStringValue{Value: "stack-id"}, &types.FieldMemberStringValue{Value: ""}, &types.FieldMemberStringValue{Value: "create"}}}}, nil
	}
	if (c.kind == "generator" && strings.Contains(*in.Sql, "WITH latest_generators")) || (c.kind == "policy" && strings.Contains(*in.Sql, "WITH latest_policies")) {
		return &rdsdata.ExecuteStatementOutput{Records: [][]types.Field{c.row}}, nil
	}
	return &rdsdata.ExecuteStatementOutput{}, nil
}
func TestDesiredMetadataRejectsMalformedDataAPIRows(t *testing.T) {
	for _, kind := range []string{"policy", "generator"} {
		for _, row := range [][]types.Field{{}, {&types.FieldMemberIsNull{Value: true}, &types.FieldMemberStringValue{Value: "ttl"}, &types.FieldMemberStringValue{Value: `{"TTLSeconds":60}`}}, {&types.FieldMemberLongValue{Value: 1}, &types.FieldMemberLongValue{Value: 2}, &types.FieldMemberLongValue{Value: 3}}} {
			ds := &DatastoreAuroraDataAPI{client: &malformedDesiredMetadataClient{kind: kind, row: row}}
			if kind == "policy" {
				result, err := ds.GetDesiredInlinePoliciesForStack("stack-id")
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				result, err := ds.LoadDesiredGeneratorsByStack("stack")
				require.Error(t, err)
				require.Nil(t, result)
			}
		}
	}
}
