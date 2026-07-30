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

// RunFindResourcesDependingOnMany_MultipleFrontierRefs verifies that a single
// dependent referencing two frontier KSUIDs appears under each of them in the result map.
func RunFindResourcesDependingOnMany_MultipleFrontierRefs(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesDependingOnMany_MultipleFrontierRefs", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		_, err := ds.CreateTarget(&pkgmodel.Target{
			Label:     "test-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		})
		assert.NoError(t, err)

		// Two independent parent resources (both frontier members).
		parent1Ksuid := mksuid.New().String()
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: parent1Ksuid, NativeID: "p1-native", Stack: "s", Label: "p1",
			Type: "AWS::S3::Bucket", Target: "test-target",
			Properties: json.RawMessage(`{"BucketName":"b1"}`),
		}, "cmd-1")
		assert.NoError(t, err)

		parent2Ksuid := mksuid.New().String()
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: parent2Ksuid, NativeID: "p2-native", Stack: "s", Label: "p2",
			Type: "AWS::S3::Bucket", Target: "test-target",
			Properties: json.RawMessage(`{"BucketName":"b2"}`),
		}, "cmd-1")
		assert.NoError(t, err)

		// One child that references BOTH parents.
		childKsuid := mksuid.New().String()
		childProps := fmt.Sprintf(
			`{"A":{"$ref":"formae://%s#/Arn","$value":"v1"},"B":{"$ref":"formae://%s#/Arn","$value":"v2"}}`,
			parent1Ksuid, parent2Ksuid,
		)
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: childKsuid, NativeID: "child-native", Stack: "s", Label: "child",
			Type: "AWS::IAM::Role", Target: "test-target",
			Properties: json.RawMessage(childProps),
		}, "cmd-1")
		assert.NoError(t, err)

		result, err := ds.FindResourcesDependingOnMany([]string{parent1Ksuid, parent2Ksuid})
		assert.NoError(t, err)
		assert.Len(t, result[parent1Ksuid], 1, "child must appear under parent1")
		assert.Len(t, result[parent2Ksuid], 1, "child must appear under parent2")
		assert.Equal(t, childKsuid, result[parent1Ksuid][0].Ksuid)
		assert.Equal(t, childKsuid, result[parent2Ksuid][0].Ksuid)
	})
}

// RunFindResourcesDependingOnMany_RepeatedRef verifies that a resource whose data
// contains the same $ref in multiple properties appears exactly once under that KSUID
// (no duplicates in the result slice).
func RunFindResourcesDependingOnMany_RepeatedRef(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesDependingOnMany_RepeatedRef", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		_, err := ds.CreateTarget(&pkgmodel.Target{
			Label:     "test-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		})
		assert.NoError(t, err)

		parentKsuid := mksuid.New().String()
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: parentKsuid, NativeID: "p-rep-native", Stack: "s", Label: "p-rep",
			Type: "AWS::S3::Bucket", Target: "test-target",
			Properties: json.RawMessage(`{"BucketName":"rep"}`),
		}, "cmd-1")
		assert.NoError(t, err)

		// Two properties, both referencing the same parent KSUID.
		childKsuid := mksuid.New().String()
		childProps := fmt.Sprintf(
			`{"A":{"$ref":"formae://%s#/Arn","$value":"v"},"B":{"$ref":"formae://%s#/Region","$value":"us-east-1"}}`,
			parentKsuid, parentKsuid,
		)
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: childKsuid, NativeID: "c-rep-native", Stack: "s", Label: "c-rep",
			Type: "AWS::IAM::Role", Target: "test-target",
			Properties: json.RawMessage(childProps),
		}, "cmd-1")
		assert.NoError(t, err)

		result, err := ds.FindResourcesDependingOnMany([]string{parentKsuid})
		assert.NoError(t, err)
		assert.Len(t, result[parentKsuid], 1, "repeated $ref in different properties must yield exactly one dependent entry")
	})
}

// RunFindResourcesDependingOnMany_FrontierMemberOverlap verifies correct behaviour
// when one frontier member's dependent is itself another frontier member: each is
// returned under its referenced parent and there is no crash or duplication.
func RunFindResourcesDependingOnMany_FrontierMemberOverlap(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesDependingOnMany_FrontierMemberOverlap", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		_, err := ds.CreateTarget(&pkgmodel.Target{
			Label:     "test-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		})
		assert.NoError(t, err)

		// A ← B ← C: A and B are both in the frontier; B references A, C references B.
		aKsuid := mksuid.New().String()
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: aKsuid, NativeID: "a-native", Stack: "s", Label: "a",
			Type: "AWS::S3::Bucket", Target: "test-target",
			Properties: json.RawMessage(`{"BucketName":"a"}`),
		}, "cmd-1")
		assert.NoError(t, err)

		bKsuid := mksuid.New().String()
		bProps := fmt.Sprintf(`{"Ref":{"$ref":"formae://%s#/Arn","$value":"va"}}`, aKsuid)
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: bKsuid, NativeID: "b-native", Stack: "s", Label: "b",
			Type: "AWS::IAM::Role", Target: "test-target",
			Properties: json.RawMessage(bProps),
		}, "cmd-1")
		assert.NoError(t, err)

		cKsuid := mksuid.New().String()
		cProps := fmt.Sprintf(`{"Ref":{"$ref":"formae://%s#/Arn","$value":"vb"}}`, bKsuid)
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: cKsuid, NativeID: "c-native", Stack: "s", Label: "c",
			Type: "AWS::Lambda::Function", Target: "test-target",
			Properties: json.RawMessage(cProps),
		}, "cmd-1")
		assert.NoError(t, err)

		// Query with both A and B as frontier.
		result, err := ds.FindResourcesDependingOnMany([]string{aKsuid, bKsuid})
		assert.NoError(t, err)
		assert.Len(t, result[aKsuid], 1, "B must appear as dependent of A")
		assert.Equal(t, bKsuid, result[aKsuid][0].Ksuid)
		assert.Len(t, result[bKsuid], 1, "C must appear as dependent of B")
		assert.Equal(t, cKsuid, result[bKsuid][0].Ksuid)
	})
}

// RunFindResourcesDependingOnMany_DeepChain verifies that BFS-style per-level calls
// to FindResourcesDependingOnMany correctly traverse a deep dependency chain A←B←C←D.
func RunFindResourcesDependingOnMany_DeepChain(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesDependingOnMany_DeepChain", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		_, err := ds.CreateTarget(&pkgmodel.Target{
			Label:     "test-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		})
		assert.NoError(t, err)

		// Create chain: A ← B ← C ← D (D references C, C references B, B references A).
		aKsuid := mksuid.New().String()
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: aKsuid, NativeID: "chain-a", Stack: "s", Label: "a",
			Type: "AWS::S3::Bucket", Target: "test-target",
			Properties: json.RawMessage(`{"BucketName":"chain-a"}`),
		}, "cmd-1")
		assert.NoError(t, err)

		bKsuid := mksuid.New().String()
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: bKsuid, NativeID: "chain-b", Stack: "s", Label: "b",
			Type: "AWS::IAM::Role", Target: "test-target",
			Properties: json.RawMessage(fmt.Sprintf(`{"Ref":{"$ref":"formae://%s#/Arn","$value":"va"}}`, aKsuid)),
		}, "cmd-1")
		assert.NoError(t, err)

		cKsuid := mksuid.New().String()
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: cKsuid, NativeID: "chain-c", Stack: "s", Label: "c",
			Type: "AWS::Lambda::Function", Target: "test-target",
			Properties: json.RawMessage(fmt.Sprintf(`{"Ref":{"$ref":"formae://%s#/Arn","$value":"vb"}}`, bKsuid)),
		}, "cmd-1")
		assert.NoError(t, err)

		dKsuid := mksuid.New().String()
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: dKsuid, NativeID: "chain-d", Stack: "s", Label: "d",
			Type: "AWS::SNS::Topic", Target: "test-target",
			Properties: json.RawMessage(fmt.Sprintf(`{"Ref":{"$ref":"formae://%s#/Arn","$value":"vc"}}`, cKsuid)),
		}, "cmd-1")
		assert.NoError(t, err)

		// Level 1: dependents of A → {B}.
		r1, err := ds.FindResourcesDependingOnMany([]string{aKsuid})
		assert.NoError(t, err)
		assert.Len(t, r1[aKsuid], 1)
		assert.Equal(t, bKsuid, r1[aKsuid][0].Ksuid)

		// Level 2: dependents of B → {C}.
		r2, err := ds.FindResourcesDependingOnMany([]string{bKsuid})
		assert.NoError(t, err)
		assert.Len(t, r2[bKsuid], 1)
		assert.Equal(t, cKsuid, r2[bKsuid][0].Ksuid)

		// Level 3: dependents of C → {D}.
		r3, err := ds.FindResourcesDependingOnMany([]string{cKsuid})
		assert.NoError(t, err)
		assert.Len(t, r3[cKsuid], 1)
		assert.Equal(t, dKsuid, r3[cKsuid][0].Ksuid)

		// Level 4: dependents of D → empty (leaf node).
		r4, err := ds.FindResourcesDependingOnMany([]string{dKsuid})
		assert.NoError(t, err)
		assert.Empty(t, r4[dKsuid])
	})
}

// RunFindResourcesDependingOnMany_BroadFanOut verifies that many dependents (20)
// each referencing a single frontier KSUID are all returned under that KSUID.
func RunFindResourcesDependingOnMany_BroadFanOut(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesDependingOnMany_BroadFanOut", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		_, err := ds.CreateTarget(&pkgmodel.Target{
			Label:     "test-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		})
		assert.NoError(t, err)

		parentKsuid := mksuid.New().String()
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: parentKsuid, NativeID: "fanout-parent", Stack: "s", Label: "fanout-parent",
			Type: "AWS::VPC::VPC", Target: "test-target",
			Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/8"}`),
		}, "cmd-1")
		assert.NoError(t, err)

		const numDependents = 20
		childKsuids := make([]string, numDependents)
		for i := 0; i < numDependents; i++ {
			childKsuids[i] = mksuid.New().String()
			childProps := fmt.Sprintf(`{"VpcId":{"$ref":"formae://%s#/VpcId","$value":"vpc-x"}}`, parentKsuid)
			_, err = ds.StoreResource(&pkgmodel.Resource{
				Ksuid:      childKsuids[i],
				NativeID:   fmt.Sprintf("fanout-child-%d", i),
				Stack:      "s",
				Label:      fmt.Sprintf("fanout-child-%d", i),
				Type:       "AWS::EC2::Subnet",
				Target:     "test-target",
				Properties: json.RawMessage(childProps),
			}, "cmd-1")
			assert.NoError(t, err)
		}

		result, err := ds.FindResourcesDependingOnMany([]string{parentKsuid})
		assert.NoError(t, err)
		assert.Len(t, result[parentKsuid], numDependents, "all %d dependents must be returned", numDependents)
	})
}
