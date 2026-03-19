// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestGenerateResourceUpdates_TargetReplace_Reconcile_StackInForma(t *testing.T) {
	ds := newMockDatastore()
	ksuidA := util.NewID()

	bucketSchema := pkgmodel.Schema{
		Fields:   []string{"BucketName"},
		Portable: true,
	}

	// Resource A exists in DB on target aws-prod
	existingA := &pkgmodel.Resource{
		Label:      "bucket-a",
		Type:       "AWS::S3::Bucket",
		Stack:      "web",
		Target:     "aws-prod",
		Ksuid:      ksuidA,
		Managed:    true,
		Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
		Schema:     bucketSchema,
	}
	ds.resourcesByStack["web"] = []*pkgmodel.Resource{existingA}
	ds.triplet[pkgmodel.TripletKey{Stack: "web", Label: "bucket-a", Type: "AWS::S3::Bucket"}] = ksuidA

	existingTargets := []*pkgmodel.Target{
		{Label: "aws-prod", Namespace: "aws", Config: json.RawMessage(`{"region":"us-east-1"}`)},
	}

	forma := &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: "web"}},
		Targets: []pkgmodel.Target{{Label: "aws-prod", Namespace: "aws", Config: json.RawMessage(`{"region":"us-west-2"}`)}},
		Resources: []pkgmodel.Resource{
			{
				Label:      "bucket-a",
				Type:       "AWS::S3::Bucket",
				Stack:      "web",
				Target:     "aws-prod",
				Ksuid:      ksuidA,
				Managed:    true,
				Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
				Schema:     bucketSchema,
			},
		},
	}

	replacedTargets := map[string]bool{"aws-prod": true}

	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, replacedTargets, nil)

	require.NoError(t, err)

	// Even though properties didn't change, resource should be replaced because target is replaced
	// Replace = delete + create = 2 operations
	deleteCount := 0
	createCount := 0
	for _, u := range updates {
		if u.Operation == OperationDelete && u.DesiredState.Label == "bucket-a" {
			deleteCount++
		}
		if u.Operation == OperationCreate && u.DesiredState.Label == "bucket-a" {
			createCount++
		}
	}
	assert.Equal(t, 1, deleteCount, "should have 1 delete for bucket-a")
	assert.Equal(t, 1, createCount, "should have 1 create for bucket-a")
}

func TestGenerateResourceUpdates_TargetReplace_Reconcile_StackNotInForma(t *testing.T) {
	ds := newMockDatastore()
	ksuidD := util.NewID()

	lambdaSchema := pkgmodel.Schema{
		Fields:   []string{"FunctionName"},
		Portable: true,
	}

	// Resource D exists on stack "api" which is NOT in the forma
	existingD := &pkgmodel.Resource{
		Label:      "api-func",
		Type:       "AWS::Lambda::Function",
		Stack:      "api",
		Target:     "aws-prod",
		Ksuid:      ksuidD,
		Managed:    true,
		Properties: json.RawMessage(`{"FunctionName":"my-api"}`),
		Schema:     lambdaSchema,
	}
	ds.resourcesByStack["api"] = []*pkgmodel.Resource{existingD}
	ds.triplet[pkgmodel.TripletKey{Stack: "api", Label: "api-func", Type: "AWS::Lambda::Function"}] = ksuidD

	existingTargets := []*pkgmodel.Target{
		{Label: "aws-prod", Namespace: "aws", Config: json.RawMessage(`{"region":"us-east-1"}`)},
	}

	// Forma only has an empty target, no stacks or resources
	forma := &pkgmodel.Forma{
		Targets: []pkgmodel.Target{{Label: "aws-prod", Namespace: "aws", Config: json.RawMessage(`{"region":"us-west-2"}`)}},
	}

	replacedTargets := map[string]bool{"aws-prod": true}

	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, replacedTargets, nil)

	require.NoError(t, err)

	// Resource D on non-forma stack should be replaced (delete + create)
	deleteCount := 0
	createCount := 0
	for _, u := range updates {
		if u.Operation == OperationDelete && u.DesiredState.Label == "api-func" {
			deleteCount++
		}
		if u.Operation == OperationCreate && u.DesiredState.Label == "api-func" {
			createCount++
		}
	}
	assert.Equal(t, 1, deleteCount, "should have 1 delete for api-func")
	assert.Equal(t, 1, createCount, "should have 1 create for api-func")
}

func TestGenerateResourceUpdates_TargetReplace_Patch(t *testing.T) {
	ds := newMockDatastore()
	ksuidA := util.NewID()
	ksuidB := util.NewID()

	bucketSchema := pkgmodel.Schema{
		Fields:   []string{"BucketName"},
		Portable: true,
	}

	// Resource A in forma on stack "web"
	existingA := &pkgmodel.Resource{
		Label:      "bucket-a",
		Type:       "AWS::S3::Bucket",
		Stack:      "web",
		Target:     "aws-prod",
		Ksuid:      ksuidA,
		Managed:    true,
		Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
		Schema:     bucketSchema,
	}
	// Resource B NOT in forma but on same stack/target
	existingB := &pkgmodel.Resource{
		Label:      "bucket-b",
		Type:       "AWS::S3::Bucket",
		Stack:      "web",
		Target:     "aws-prod",
		Ksuid:      ksuidB,
		Managed:    true,
		Properties: json.RawMessage(`{"BucketName":"other-bucket"}`),
		Schema:     bucketSchema,
	}
	ds.resourcesByStack["web"] = []*pkgmodel.Resource{existingA, existingB}
	ds.triplet[pkgmodel.TripletKey{Stack: "web", Label: "bucket-a", Type: "AWS::S3::Bucket"}] = ksuidA
	ds.triplet[pkgmodel.TripletKey{Stack: "web", Label: "bucket-b", Type: "AWS::S3::Bucket"}] = ksuidB

	existingTargets := []*pkgmodel.Target{
		{Label: "aws-prod", Namespace: "aws", Config: json.RawMessage(`{"region":"us-east-1"}`)},
	}

	forma := &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: "web"}},
		Targets: []pkgmodel.Target{{Label: "aws-prod", Namespace: "aws", Config: json.RawMessage(`{"region":"us-west-2"}`)}},
		Resources: []pkgmodel.Resource{
			{
				Label:      "bucket-a",
				Type:       "AWS::S3::Bucket",
				Stack:      "web",
				Target:     "aws-prod",
				Ksuid:      ksuidA,
				Managed:    true,
				Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
				Schema:     bucketSchema,
			},
		},
	}

	replacedTargets := map[string]bool{"aws-prod": true}

	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModePatch,
		FormaCommandSourceUser, existingTargets, ds, replacedTargets, nil)

	require.NoError(t, err)

	// Both A and B should be replaced (A from forma, B from DB)
	ops := make(map[string]map[OperationType]int)
	for _, u := range updates {
		if ops[u.DesiredState.Label] == nil {
			ops[u.DesiredState.Label] = make(map[OperationType]int)
		}
		ops[u.DesiredState.Label][u.Operation]++
	}

	assert.Equal(t, 1, ops["bucket-a"][OperationDelete], "bucket-a should have 1 delete")
	assert.Equal(t, 1, ops["bucket-a"][OperationCreate], "bucket-a should have 1 create")
	assert.Equal(t, 1, ops["bucket-b"][OperationDelete], "bucket-b should have 1 delete")
	assert.Equal(t, 1, ops["bucket-b"][OperationCreate], "bucket-b should have 1 create")
}
