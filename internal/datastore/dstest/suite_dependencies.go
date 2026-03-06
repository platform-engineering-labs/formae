// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/demula/mksuid/v2"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

func RunFindResourcesDependingOn(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesDependingOn", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Create target
		target := &pkgmodel.Target{
			Label:     "test-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		// Create the "parent" resource (the one being deleted)
		parentKsuid := mksuid.New().String()
		parentResource := &pkgmodel.Resource{
			Ksuid:      parentKsuid,
			NativeID:   "parent-bucket-native",
			Stack:      "test-stack",
			Label:      "parent-bucket",
			Type:       "AWS::S3::Bucket",
			Target:     "test-target",
			Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
		}
		_, err = ds.StoreResource(parentResource, "cmd-1")
		assert.NoError(t, err)

		// Create a "child" resource that references the parent via $ref
		childKsuid := mksuid.New().String()
		childProperties := fmt.Sprintf(`{"RoleArn":{"$ref":"formae://%s#/Arn","$value":"arn:aws:s3:::my-bucket"}}`, parentKsuid)
		childResource := &pkgmodel.Resource{
			Ksuid:      childKsuid,
			NativeID:   "child-role-native",
			Stack:      "test-stack",
			Label:      "child-role",
			Type:       "AWS::IAM::Role",
			Target:     "test-target",
			Properties: json.RawMessage(childProperties),
		}
		_, err = ds.StoreResource(childResource, "cmd-1")
		assert.NoError(t, err)

		// Create an unrelated resource (no reference)
		unrelatedKsuid := mksuid.New().String()
		unrelatedResource := &pkgmodel.Resource{
			Ksuid:      unrelatedKsuid,
			NativeID:   "unrelated-native",
			Stack:      "test-stack",
			Label:      "unrelated-resource",
			Type:       "AWS::Lambda::Function",
			Target:     "test-target",
			Properties: json.RawMessage(`{"FunctionName":"my-function"}`),
		}
		_, err = ds.StoreResource(unrelatedResource, "cmd-1")
		assert.NoError(t, err)

		// Find resources depending on the parent
		dependents, err := ds.FindResourcesDependingOn(parentKsuid)
		assert.NoError(t, err)
		assert.Len(t, dependents, 1, "Should find exactly one dependent resource")
		assert.Equal(t, childKsuid, dependents[0].Ksuid)
		assert.Equal(t, "child-role", dependents[0].Label)
	})
}

func RunFindResourcesDependingOnMultipleRefs(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesDependingOn_MultipleRefs", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Create target
		target := &pkgmodel.Target{
			Label:     "test-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		// Create the parent resource
		parentKsuid := mksuid.New().String()
		parentResource := &pkgmodel.Resource{
			Ksuid:      parentKsuid,
			NativeID:   "vpc-native",
			Stack:      "test-stack",
			Label:      "vpc",
			Type:       "AWS::EC2::VPC",
			Target:     "test-target",
			Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
		}
		_, err = ds.StoreResource(parentResource, "cmd-1")
		assert.NoError(t, err)

		// Create multiple resources that depend on the parent
		for i := 0; i < 3; i++ {
			childKsuid := mksuid.New().String()
			childProperties := fmt.Sprintf(`{"VpcId":{"$ref":"formae://%s#/VpcId","$value":"vpc-123"}}`, parentKsuid)
			childResource := &pkgmodel.Resource{
				Ksuid:      childKsuid,
				NativeID:   fmt.Sprintf("subnet-native-%d", i), // unique native ID required
				Stack:      "test-stack",
				Label:      fmt.Sprintf("subnet-%d", i),
				Type:       "AWS::EC2::Subnet",
				Target:     "test-target",
				Properties: json.RawMessage(childProperties),
			}
			_, err = ds.StoreResource(childResource, "cmd-1")
			assert.NoError(t, err)
		}

		// Find resources depending on the parent
		dependents, err := ds.FindResourcesDependingOn(parentKsuid)
		assert.NoError(t, err)
		assert.Len(t, dependents, 3, "Should find all three dependent subnets")
	})
}

func RunFindResourcesDependingOnNoRefs(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesDependingOn_NoRefs", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Create target
		target := &pkgmodel.Target{
			Label:     "test-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		// Create a resource with no dependents
		parentKsuid := mksuid.New().String()
		parentResource := &pkgmodel.Resource{
			Ksuid:      parentKsuid,
			NativeID:   "standalone-bucket-native",
			Stack:      "test-stack",
			Label:      "standalone-bucket",
			Type:       "AWS::S3::Bucket",
			Target:     "test-target",
			Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
		}
		_, err = ds.StoreResource(parentResource, "cmd-1")
		assert.NoError(t, err)

		// Find resources depending on the parent (should be empty)
		dependents, err := ds.FindResourcesDependingOn(parentKsuid)
		assert.NoError(t, err)
		assert.Empty(t, dependents, "Should find no dependent resources")
	})
}

func RunFindResourcesDependingOnDeletedResourcesExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesDependingOn_DeletedResourcesExcluded", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Create target
		target := &pkgmodel.Target{
			Label:     "test-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		// Create the parent resource
		parentKsuid := mksuid.New().String()
		parentResource := &pkgmodel.Resource{
			Ksuid:      parentKsuid,
			NativeID:   "deleted-parent-bucket-native",
			Stack:      "test-stack",
			Label:      "parent-bucket",
			Type:       "AWS::S3::Bucket",
			Target:     "test-target",
			Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
		}
		_, err = ds.StoreResource(parentResource, "cmd-1")
		assert.NoError(t, err)

		// Create a child resource that references the parent
		childKsuid := mksuid.New().String()
		childProperties := fmt.Sprintf(`{"RoleArn":{"$ref":"formae://%s#/Arn","$value":"arn:aws:s3:::my-bucket"}}`, parentKsuid)
		childResource := &pkgmodel.Resource{
			Ksuid:      childKsuid,
			NativeID:   "deleted-child-role-native",
			Stack:      "test-stack",
			Label:      "child-role",
			Type:       "AWS::IAM::Role",
			Target:     "test-target",
			Properties: json.RawMessage(childProperties),
		}
		_, err = ds.StoreResource(childResource, "cmd-1")
		assert.NoError(t, err)

		// Delete the child resource
		_, err = ds.DeleteResource(childResource, "cmd-2")
		assert.NoError(t, err)

		// Find resources depending on the parent - should be empty since child was deleted
		dependents, err := ds.FindResourcesDependingOn(parentKsuid)
		assert.NoError(t, err)
		assert.Empty(t, dependents, "Should not find deleted resources as dependents")
	})
}
