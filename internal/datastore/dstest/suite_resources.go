// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

func RunStoreResource(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StoreResource", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := &pkgmodel.Target{
			Label:     "target-1",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		resource := &pkgmodel.Resource{
			NativeID: "native-1",
			Stack:    "stack-1",
			Type:     "type-1",
			Label:    "label-1",
			Target:   "target-1",
			Properties: json.RawMessage(`{
			"key": "value"
			}`),
			Managed: false,
		}

		_, err = ds.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		query := &datastore.ResourceQuery{
			NativeID: &datastore.QueryItem[string]{
				Item:       "native-1",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 1)

		assert.NotEmpty(t, results[0].Ksuid)
		assert.Equal(t, resource.URI(), results[0].URI())

		assert.Equal(t, resource.NativeID, results[0].NativeID)
		assert.Equal(t, resource.Stack, results[0].Stack)
		assert.Equal(t, resource.Type, results[0].Type)
		assert.Equal(t, resource.Label, results[0].Label)
		assert.Equal(t, resource.Target, results[0].Target)
		assert.Equal(t, resource.Managed, results[0].Managed)
	})
}

func RunUpdateResource(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateResource", func(t *testing.T) {
		t.Run("Should create a new version if the resource properties changed", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			originalResource := &pkgmodel.Resource{
				Ksuid:    util.NewID(),
				NativeID: "native-1",
				Stack:    "stack-1",
				Type:     "type-1",
				Label:    "label-1",
				Properties: json.RawMessage(`{
				"key": "value"
				}`),
			}
			originalVersion, err := ds.StoreResource(originalResource, "test-command-1")
			assert.NoError(t, err)

			newVersion, err := ds.StoreResource(&pkgmodel.Resource{
				Ksuid:    originalResource.Ksuid,
				NativeID: "native-1",
				Stack:    "stack-1",
				Type:     "type-1",
				Label:    "label-1",
				Properties: json.RawMessage(`{
				"key": "new-value"
				}`),
			}, "test-command-2")
			assert.NoError(t, err)

			newFromDb, err := ds.LoadResourceById(originalResource.Ksuid)
			assert.NoError(t, err)

			assert.NotEqual(t, originalVersion, newVersion)
			assert.JSONEq(t, `{"key": "new-value"}`, string(newFromDb.Properties))
		})

		t.Run("Should not create a new version if the read-only properties changed", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			originalResource := &pkgmodel.Resource{
				Ksuid:    util.NewID(),
				NativeID: "native-1",
				Stack:    "stack-1",
				Type:     "type-1",
				Label:    "label-1",
				Properties: json.RawMessage(`{
				"key": "value"
				}`),
				ReadOnlyProperties: json.RawMessage(`{
				"ro-key": "ro-value"
				}`),
			}
			originalVersion, err := ds.StoreResource(originalResource, "test-command-1")
			assert.NoError(t, err)

			newVersion, err := ds.StoreResource(&pkgmodel.Resource{
				Ksuid:    originalResource.Ksuid,
				NativeID: "native-1",
				Stack:    "stack-1",
				Type:     "type-1",
				Label:    "label-1",
				Properties: json.RawMessage(`{
				"key": "value"
				}`),
				ReadOnlyProperties: json.RawMessage(`{
				"ro-key": "new-ro-value"
				}`),
			}, "test-command-2")
			assert.NoError(t, err)

			newFromDb, err := ds.LoadResourceById(originalResource.Ksuid)
			assert.NoError(t, err)

			assert.Equal(t, originalVersion, newVersion)
			assert.JSONEq(t, `{"ro-key": "new-ro-value"}`, string(newFromDb.ReadOnlyProperties))
		})
	})
}

func RunDeleteResource(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteResource", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		resource := &pkgmodel.Resource{
			NativeID: "native-1",
			Stack:    "stack-1",
			Type:     "type-1",
			Label:    "label-1",
			Properties: json.RawMessage(`{
			"key": "value"
			}`),
		}

		_, err := ds.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		_, err = ds.DeleteResource(resource, "test-command")
		assert.NoError(t, err)

		query := &datastore.ResourceQuery{
			NativeID: &datastore.QueryItem[string]{
				Item:       "native-1",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Empty(t, results)
	})
}

func RunQueryResources(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("QueryResources", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		for i := range 7 {
			target := &pkgmodel.Target{
				Label:     fmt.Sprintf("target-%d", i),
				Namespace: "default",
				Config:    json.RawMessage(`{}`),
			}
			_, err := ds.CreateTarget(target)
			assert.NoError(t, err)
		}

		for i := range 10 {
			resource := &pkgmodel.Resource{
				NativeID: fmt.Sprintf("native-%d", i),
				Stack:    fmt.Sprintf("stack-%d", i%3),
				Type:     fmt.Sprintf("type-%d", i%4),
				Label:    fmt.Sprintf("label-%d", i%5),
				Target:   fmt.Sprintf("target-%d", i%7),
				Managed:  i%2 == 0,
				Properties: json.RawMessage(fmt.Sprintf(`{
				"key": "value-%d"
				}`, i)),
			}
			_, err := ds.StoreResource(resource, "test-command")
			assert.NoError(t, err)
		}

		query := &datastore.ResourceQuery{
			NativeID: &datastore.QueryItem[string]{
				Item:       "native-1",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 1)
		for _, result := range results {
			assert.Equal(t, "native-1", result.NativeID)
		}

		query = &datastore.ResourceQuery{
			Stack: &datastore.QueryItem[string]{
				Item:       "stack-2",
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "stack-2", result.Stack)
		}

		query = &datastore.ResourceQuery{
			Type: &datastore.QueryItem[string]{
				Item:       "type-3",
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "type-3", result.Type)
		}

		query = &datastore.ResourceQuery{
			Label: &datastore.QueryItem[string]{
				Item:       "label-4",
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "label-4", result.Label)
		}

		query = &datastore.ResourceQuery{
			Target: &datastore.QueryItem[string]{
				Item:       "target-4",
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "target-4", result.Target)
		}

		query = &datastore.ResourceQuery{}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		assert.Len(t, results, 10)

		query = &datastore.ResourceQuery{
			Managed: &datastore.QueryItem[bool]{
				Item:       true,
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 5)

		query = &datastore.ResourceQuery{
			Managed: &datastore.QueryItem[bool]{
				Item:       false,
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		assert.Len(t, results, 5)
	})
}

func RunStoreResourceSameResourceTwice(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StoreResource_SameResourceTwiceReturnsSameVersionId", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := &pkgmodel.Target{
			Label:     "test-target",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		resource := &pkgmodel.Resource{
			NativeID: "native-1",
			Stack:    "stack-1",
			Type:     "type-1",
			Label:    "label-1",
			Target:   "test-target",
			Managed:  true,
			Properties: json.RawMessage(`{
			"key": "value"
			}`),
		}

		firstVersionId, err := ds.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		secondVersionId, err := ds.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		assert.Equal(t, firstVersionId, secondVersionId)

		query := &datastore.ResourceQuery{
			NativeID: &datastore.QueryItem[string]{
				Item:       "native-1",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, resource.URI(), results[0].URI())
	})
}

func RunLoadResourceByNativeID(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("LoadResourceByNativeID", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		resource := &pkgmodel.Resource{
			NativeID: "native-123",
			Stack:    "stack-1",
			Type:     "type-1",
			Label:    "label-1",
			Properties: json.RawMessage(`{
			"key": "value"
		}`),
			Managed: false,
		}

		_, err := ds.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		loadedResource, err := ds.LoadResourceByNativeID("native-123", "type-1")
		assert.NoError(t, err)
		assert.NotNil(t, loadedResource)
		assert.Equal(t, resource.NativeID, loadedResource.NativeID)
		assert.Equal(t, resource.Stack, loadedResource.Stack)
		assert.Equal(t, resource.Type, loadedResource.Type)

		// Negative test - wrong type
		nonExistentResource, err := ds.LoadResourceByNativeID("native-123", "wrong-type")
		assert.NoError(t, err)
		assert.Nil(t, nonExistentResource)

		// Negative test - non-existent native ID
		nonExistentResource2, err := ds.LoadResourceByNativeID("non-existent", "type-1")
		assert.NoError(t, err)
		assert.Nil(t, nonExistentResource2)

		_, err = ds.DeleteResource(resource, "test-delete-command")
		assert.NoError(t, err)

		deletedResource, err := ds.LoadResourceByNativeID("native-123", "type-1")
		assert.NoError(t, err)
		assert.Nil(t, deletedResource, "Deleted resource should not be found")
	})
}

func RunLoadResourceByNativeIDDifferentTypes(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("LoadResourceByNativeID_DifferentTypes", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Store two resources with same native ID but different types
		resource1 := &pkgmodel.Resource{
			NativeID: "shared-id-123",
			Type:     "AWS::S3::Bucket",
			Stack:    "stack-1",
			Label:    "bucket-1",
			Properties: json.RawMessage(`{
				"BucketName": "my-bucket"
			}`),
			Managed: false,
		}
		resource2 := &pkgmodel.Resource{
			NativeID: "shared-id-123",
			Type:     "AWS::IAM::Role",
			Stack:    "stack-1",
			Label:    "role-1",
			Properties: json.RawMessage(`{
				"RoleName": "my-role"
			}`),
			Managed: false,
		}

		_, err := ds.StoreResource(resource1, "cmd-1")
		assert.NoError(t, err)
		_, err = ds.StoreResource(resource2, "cmd-2")
		assert.NoError(t, err)

		// Should return bucket when querying for bucket type
		loaded1, err := ds.LoadResourceByNativeID("shared-id-123", "AWS::S3::Bucket")
		assert.NoError(t, err)
		assert.NotNil(t, loaded1, "Bucket should be found")
		assert.Equal(t, "AWS::S3::Bucket", loaded1.Type)
		assert.Equal(t, "bucket-1", loaded1.Label)

		// Should return role when querying for role type
		loaded2, err := ds.LoadResourceByNativeID("shared-id-123", "AWS::IAM::Role")
		assert.NoError(t, err)
		assert.NotNil(t, loaded2, "Role should be found")
		assert.Equal(t, "AWS::IAM::Role", loaded2.Type)
		assert.Equal(t, "role-1", loaded2.Label)

		// Should return nil for wrong type
		loaded3, err := ds.LoadResourceByNativeID("shared-id-123", "AWS::EC2::Instance")
		assert.NoError(t, err)
		assert.Nil(t, loaded3, "Non-existent type should return nil")
	})
}

func RunBatchGetKSUIDsByTriplets(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("BatchGetKSUIDsByTriplets", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		resource1 := &pkgmodel.Resource{
			Stack:      "test-stack",
			Label:      "resource-1",
			Type:       "AWS::S3::Bucket",
			NativeID:   "bucket-1",
			Properties: json.RawMessage(`{"BucketName": "test-bucket-1"}`),
			Managed:    true,
		}

		resource2 := &pkgmodel.Resource{
			Stack:      "test-stack",
			Label:      "resource-2",
			Type:       "AWS::EC2::VPC",
			NativeID:   "vpc-1",
			Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
			Managed:    true,
		}

		resource3 := &pkgmodel.Resource{
			Stack:      "other-stack",
			Label:      "resource-3",
			Type:       "AWS::IAM::Role",
			NativeID:   "role-1",
			Properties: json.RawMessage(`{"RoleName": "test-role"}`),
			Managed:    true,
		}

		// Store the resources
		_, err := ds.StoreResource(resource1, "test-command-1")
		assert.NoError(t, err)
		_, err = ds.StoreResource(resource2, "test-command-2")
		assert.NoError(t, err)
		_, err = ds.StoreResource(resource3, "test-command-3")
		assert.NoError(t, err)

		// Get the actual KSUIDs from the stored resources by querying them back
		storedResource1, err := ds.LoadResourceByNativeID("bucket-1", "AWS::S3::Bucket")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource1)

		storedResource2, err := ds.LoadResourceByNativeID("vpc-1", "AWS::EC2::VPC")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource2)

		storedResource3, err := ds.LoadResourceByNativeID("role-1", "AWS::IAM::Role")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource3)

		// Test basic batch lookup
		triplets := []pkgmodel.TripletKey{
			{Stack: "test-stack", Label: "resource-1", Type: "AWS::S3::Bucket"},
			{Stack: "test-stack", Label: "resource-2", Type: "AWS::EC2::VPC"},
			{Stack: "other-stack", Label: "resource-3", Type: "AWS::IAM::Role"},
		}

		results, err := ds.BatchGetKSUIDsByTriplets(triplets)
		assert.NoError(t, err)
		assert.Len(t, results, 3)

		// Verify the correct KSUIDs are returned
		assert.Equal(t, storedResource1.Ksuid, results[triplets[0]])
		assert.Equal(t, storedResource2.Ksuid, results[triplets[1]])
		assert.Equal(t, storedResource3.Ksuid, results[triplets[2]])

		// Test with non-existent triplets
		mixedTriplets := []pkgmodel.TripletKey{
			{Stack: "test-stack", Label: "resource-1", Type: "AWS::S3::Bucket"},         // exists
			{Stack: "non-existent", Label: "resource-x", Type: "AWS::Lambda::Function"}, // doesn't exist
			{Stack: "other-stack", Label: "resource-3", Type: "AWS::IAM::Role"},         // exists
		}

		mixedResults, err := ds.BatchGetKSUIDsByTriplets(mixedTriplets)
		assert.NoError(t, err)
		assert.Len(t, mixedResults, 2) // Only 2 should be found

		// Verify existing resources are found
		assert.Equal(t, storedResource1.Ksuid, mixedResults[mixedTriplets[0]])
		assert.Equal(t, storedResource3.Ksuid, mixedResults[mixedTriplets[2]])

		// Verify non-existent resource is not in results
		_, exists := mixedResults[mixedTriplets[1]]
		assert.False(t, exists, "Non-existent triplet should not be in results")

		// Test with empty input
		emptyResults, err := ds.BatchGetKSUIDsByTriplets([]pkgmodel.TripletKey{})
		assert.NoError(t, err)
		assert.Empty(t, emptyResults)

		// Test that deleted resources are NOT returned (they should be filtered out)
		_, err = ds.DeleteResource(storedResource1, "delete-command")
		assert.NoError(t, err)

		afterDeleteResults, err := ds.BatchGetKSUIDsByTriplets(triplets)
		assert.NoError(t, err)
		assert.Len(t, afterDeleteResults, 2, "Deleted resources should not be returned")

		// Verify deleted resource is NOT in results
		_, exists = afterDeleteResults[triplets[0]]
		assert.False(t, exists, "Deleted resource should not be in results")

		// Verify other resources are still there
		assert.Equal(t, storedResource2.Ksuid, afterDeleteResults[triplets[1]])
		assert.Equal(t, storedResource3.Ksuid, afterDeleteResults[triplets[2]])
	})
}

func RunBatchGetKSUIDsByTripletsPatchScenario(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("BatchGetKSUIDsByTriplets_PatchScenario", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		initialResource := &pkgmodel.Resource{
			Stack:      "test-stack",
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			NativeID:   "bucket-123",
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Managed:    true,
		}

		// Store the initial resource
		_, err := ds.StoreResource(initialResource, "initial-command")
		assert.NoError(t, err)

		// Get the stored resource to see what KSUID it got
		storedResource, err := ds.LoadResourceByNativeID("bucket-123", "FakeAWS::S3::Bucket")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource)
		assert.NotEmpty(t, storedResource.Ksuid)

		t.Logf("Initial resource stored with KSUID: %s", storedResource.Ksuid)

		// Now simulate the patch scenario - try to look up the same triplet
		triplet := pkgmodel.TripletKey{
			Stack: "test-stack",
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
		}

		t.Logf("Looking up triplet: %+v", triplet)

		results, err := ds.BatchGetKSUIDsByTriplets([]pkgmodel.TripletKey{triplet})
		assert.NoError(t, err)

		t.Logf("Batch lookup results: %+v", results)

		// Verify the lookup found the resource
		ksuid, exists := results[triplet]
		assert.True(t, exists, "Batch lookup should find the existing resource")
		assert.Equal(t, storedResource.Ksuid, ksuid, "Should return the same KSUID")
	})
}

func RunDifferentResourceTypesSameNativeId(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DifferentResourceTypesSameNativeId", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		bucket := &pkgmodel.Resource{
			Stack:    "static-website-stack",
			Label:    "WebsiteBucket",
			Type:     "AWS::S3::Bucket",
			NativeID: "my-unique-bucket-name",
			Target:   "aws-target",
			Properties: json.RawMessage(`{
				"BucketName": "my-unique-bucket-name",
				"WebsiteConfiguration": {"IndexDocument": "index.html"}
			}`),
			Managed: true,
		}

		bucketPolicy := &pkgmodel.Resource{
			Stack:    "static-website-stack",
			Label:    "WebsiteBucketPolicy",
			Type:     "AWS::S3::BucketPolicy",
			NativeID: "my-unique-bucket-name", // Same NativeId as bucket!
			Target:   "aws-target",
			Properties: json.RawMessage(`{
				"Bucket": "my-unique-bucket-name",
				"PolicyDocument": {"Version": "2012-10-17", "Statement": []}
			}`),
			Managed: true,
		}

		_, err := ds.StoreResource(bucket, "test-command-1")
		assert.NoError(t, err)
		assert.NotEmpty(t, bucket.Ksuid, "Bucket should have a KSUID assigned")

		_, err = ds.StoreResource(bucketPolicy, "test-command-1")
		assert.NoError(t, err)
		assert.NotEmpty(t, bucketPolicy.Ksuid, "BucketPolicy should have a KSUID assigned")

		assert.NotEqual(t, bucket.Ksuid, bucketPolicy.Ksuid,
			"Bucket and BucketPolicy should have different KSUIDs even though they share the same NativeId")

		loadedBucket, err := ds.LoadResource(bucket.URI())
		assert.NoError(t, err)
		assert.NotNil(t, loadedBucket)
		assert.Equal(t, "AWS::S3::Bucket", loadedBucket.Type)
		assert.Equal(t, "WebsiteBucket", loadedBucket.Label)
		assert.Equal(t, bucket.Ksuid, loadedBucket.Ksuid)

		loadedBucketPolicy, err := ds.LoadResource(bucketPolicy.URI())
		assert.NoError(t, err)
		assert.NotNil(t, loadedBucketPolicy)
		assert.Equal(t, "AWS::S3::BucketPolicy", loadedBucketPolicy.Type)
		assert.Equal(t, "WebsiteBucketPolicy", loadedBucketPolicy.Label)
		assert.Equal(t, bucketPolicy.Ksuid, loadedBucketPolicy.Ksuid)

		bucket.Properties = json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"WebsiteConfiguration": {"IndexDocument": "index.html", "ErrorDocument": "error.html"}
		}`)
		_, err = ds.StoreResource(bucket, "test-command-2")
		assert.NoError(t, err)

		reloadedBucketPolicy, err := ds.LoadResource(bucketPolicy.URI())
		assert.NoError(t, err)
		assert.NotNil(t, reloadedBucketPolicy)
		assert.JSONEq(t, string(bucketPolicy.Properties), string(reloadedBucketPolicy.Properties),
			"BucketPolicy should remain unchanged after Bucket update")

		_, err = ds.DeleteResource(bucketPolicy, "test-delete-command")
		assert.NoError(t, err)

		query := &datastore.ResourceQuery{
			NativeID: &datastore.QueryItem[string]{
				Item:       "my-unique-bucket-name",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(results), "Should find only the Bucket after BucketPolicy deletion")
		assert.Equal(t, "AWS::S3::Bucket", results[0].Type, "Remaining resource should be the Bucket")
	})
}

func RunGetResourceModificationsSinceLastReconcile(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourceModificationsSinceLastReconcile_WithIntermediateReconcileCommand", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stackReconcile := &forma_command.FormaCommand{
			ID:              "stack-reconcile-id",
			Command:         pkgmodel.CommandApply,
			Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			StartTs:         util.TimeNow().Add(-10 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{{StackLabel: "test-stack"}},
		}
		_, err := ds.StoreResource(&pkgmodel.Resource{
			NativeID: "bucket-1",
			Stack:    "test-stack",
			Type:     "AWS::S3::Bucket",
			Label:    "bucket-1",
			Target:   "default-target",
		}, stackReconcile.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(stackReconcile, stackReconcile.ID)
		assert.NoError(t, err)

		stackPatchA := &forma_command.FormaCommand{
			ID:              "stack-patch-a-id",
			Command:         pkgmodel.CommandApply,
			Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
			StartTs:         util.TimeNow().Add(-8 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{{StackLabel: "test-stack"}},
		}
		_, err = ds.StoreResource(&pkgmodel.Resource{
			NativeID: "bucket-2",
			Stack:    "test-stack",
			Type:     "AWS::S3::Bucket",
			Label:    "bucket-2",
			Target:   "default-target",
		}, stackPatchA.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(stackPatchA, stackPatchA.ID)
		assert.NoError(t, err)

		intermediateReconcile := &forma_command.FormaCommand{
			ID:              "intermediate-reconcile-id",
			Command:         pkgmodel.CommandApply,
			Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			StartTs:         util.TimeNow().Add(-6 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{},
		}
		err = ds.StoreFormaCommand(intermediateReconcile, intermediateReconcile.ID)
		assert.NoError(t, err)

		stackPatchB := &forma_command.FormaCommand{
			ID:              "stack-patch-b-id",
			Command:         pkgmodel.CommandApply,
			Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
			StartTs:         util.TimeNow().Add(-4 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{{StackLabel: "test-stack"}},
		}
		_, err = ds.StoreResource(&pkgmodel.Resource{
			NativeID: "bucket-3",
			Stack:    "test-stack",
			Type:     "AWS::S3::Bucket",
			Label:    "bucket-3",
			Target:   "default-target",
		}, stackPatchB.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(stackPatchB, stackPatchB.ID)
		assert.NoError(t, err)

		modifications, err := ds.GetResourceModificationsSinceLastReconcile("test-stack")
		assert.NoError(t, err)

		assert.Len(t, modifications, 2, "Should include both patches despite intermediate reconcile")
		labels := make(map[string]bool)
		for _, mod := range modifications {
			labels[mod.Label] = true
		}
		assert.True(t, labels["bucket-2"], "Should include first patch")
		assert.True(t, labels["bucket-3"], "Should include second patch")
	})
}

// RunStoreResourceAfterDeleteWithSameNativeID verifies that when a resource is deleted
// and then recreated with the same NativeID but a different KSUID (as happens during destroy → re-apply),
// the new resource is stored under the new KSUID, not the old one.
//
// This is a regression test for a bug where storeResource's NativeID lookup did not filter out deleted
// resources, causing the new resource to inherit the old (deleted) resource's KSUID.
func RunStoreResourceAfterDeleteWithSameNativeID(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StoreResource_AfterDeleteWithSameNativeID", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		nativeID := "/subscriptions/test-sub/resourceGroups/test-rg"
		resourceType := "Azure::Resources::ResourceGroup"

		target := &pkgmodel.Target{
			Label:     "target-1",
			Namespace: "AZURE",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		// Step 1: Create the resource with KSUID-A
		ksuidA := util.NewID()
		resourceA := &pkgmodel.Resource{
			Ksuid:      ksuidA,
			NativeID:   nativeID,
			Stack:      "test-stack",
			Type:       resourceType,
			Label:      "rg",
			Target:     "target-1",
			Managed:    true,
			Properties: json.RawMessage(`{"name": "test-rg", "location": "eastus"}`),
		}
		_, err = ds.StoreResource(resourceA, "cmd-create-1")
		assert.NoError(t, err)

		// Verify the resource is stored under KSUID-A
		loaded, err := ds.LoadResourceById(ksuidA)
		assert.NoError(t, err)
		assert.NotNil(t, loaded, "resource should exist under KSUID-A after create")

		// Step 2: Delete the resource (simulating destroy)
		_, err = ds.DeleteResource(resourceA, "cmd-delete")
		assert.NoError(t, err)

		// Step 3: Recreate with the SAME NativeID but a NEW KSUID-B (simulating re-apply)
		time.Sleep(1100 * time.Millisecond) // KSUID has 1-second precision
		ksuidB := util.NewID()
		resourceB := &pkgmodel.Resource{
			Ksuid:      ksuidB,
			NativeID:   nativeID,
			Stack:      "test-stack",
			Type:       resourceType,
			Label:      "rg",
			Target:     "target-1",
			Managed:    true,
			Properties: json.RawMessage(`{"name": "test-rg", "location": "eastus"}`),
		}
		versionB, err := ds.StoreResource(resourceB, "cmd-create-2")
		assert.NoError(t, err)

		// Step 4: Verify the return value of StoreResource contains the NEW KSUID-B, not the old KSUID-A.
		assert.True(t, strings.HasPrefix(versionB, ksuidB+"_"),
			"StoreResource version should start with KSUID-B (%s), got: %s", ksuidB, versionB)
		assert.False(t, strings.HasPrefix(versionB, ksuidA+"_"),
			"StoreResource version must NOT start with old KSUID-A (%s), got: %s", ksuidA, versionB)

		// Step 5: Verify the new resource is stored under KSUID-B, not the old KSUID-A.
		loaded, err = ds.LoadResourceById(ksuidB)
		assert.NoError(t, err)
		assert.NotNil(t, loaded, "resource should be loadable under KSUID-B after recreate")
		assert.Equal(t, ksuidB, loaded.Ksuid, "resource should have KSUID-B, not the old KSUID-A")
		assert.Equal(t, nativeID, loaded.NativeID)
	})
}
