// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit || integration

package datastore

import (
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatastore_StackTransition(t *testing.T) {
	t.Run("preserves KSUID when transitioning from unmanaged to managed", func(t *testing.T) {
		ds, err := prepareDatastore()
		require.NoError(t, err)
		defer cleanupDatastore(ds)

		// Use unique native ID to avoid any potential data collision across test runs
		nativeID := "vpc-" + util.NewID()
		unmanagedResource := &pkgmodel.Resource{
			NativeID: nativeID,
			Stack:    constants.UnmanagedStack,
			Type:     "AWS::EC2::VPC",
			Label:    "discovered-vpc",
			Target:   "aws-target",
			Properties: json.RawMessage(`{
				"CidrBlock": "10.0.0.0/16"
			}`),
			ReadOnlyProperties: json.RawMessage(`{
				"VpcId": "` + nativeID + `",
				"OwnerId": "123456789012"
			}`),
			Managed: false,
		}

		versionID1, err := ds.StoreResource(unmanagedResource, "discovery-command")
		require.NoError(t, err)

		stored1, err := ds.LoadResourceByNativeID(nativeID, "AWS::EC2::VPC")
		require.NoError(t, err)
		require.NotNil(t, stored1)
		originalKsuid := stored1.Ksuid

		managedResource := &pkgmodel.Resource{
			Ksuid:    util.NewID(),
			NativeID: nativeID,
			Stack:    "production",
			Type:     "AWS::EC2::VPC",
			Label:    "discovered-vpc",
			Target:   "aws-target",
			Properties: json.RawMessage(`{
				"CidrBlock": "10.0.0.0/16"
			}`),
			ReadOnlyProperties: json.RawMessage(`{
				"VpcId": "` + nativeID + `",
				"OwnerId": "123456789012"
			}`),
			Managed: true,
		}

		versionID2, err := ds.StoreResource(managedResource, "apply-command")
		require.NoError(t, err)

		stored2, err := ds.LoadResourceByNativeID(nativeID, "AWS::EC2::VPC")
		require.NoError(t, err)
		require.NotNil(t, stored2)

		assert.Equal(t, originalKsuid, stored2.Ksuid, "KSUID should be preserved during stack transition")
		assert.Equal(t, "production", stored2.Stack, "Stack should be updated")
		assert.True(t, stored2.Managed, "Resource should be marked as managed")
		assert.NotEqual(t, versionID1, versionID2, "Should create new version")
	})

	t.Run("preserves native ID during stack transition", func(t *testing.T) {
		ds, err := prepareDatastore()
		require.NoError(t, err)
		defer cleanupDatastore(ds)

		// Use unique native ID to avoid any potential data collision across test runs
		nativeID := "subnet-" + util.NewID()
		unmanagedResource := &pkgmodel.Resource{
			NativeID: nativeID,
			Stack:    constants.UnmanagedStack,
			Type:     "AWS::EC2::Subnet",
			Label:    "my-subnet",
			Target:   "aws-target",
			Properties: json.RawMessage(`{
				"CidrBlock": "10.0.1.0/24"
			}`),
			ReadOnlyProperties: json.RawMessage(`{
				"SubnetId": "` + nativeID + `"
			}`),
			Managed: false,
		}

		_, err = ds.StoreResource(unmanagedResource, "discovery-command")
		require.NoError(t, err)

		originalResource, err := ds.LoadResourceByNativeID(nativeID, "AWS::EC2::Subnet")
		require.NoError(t, err)
		require.NotNil(t, originalResource)

		managedResource := &pkgmodel.Resource{
			Ksuid:    util.NewID(),
			NativeID: nativeID,
			Stack:    "production",
			Type:     "AWS::EC2::Subnet",
			Label:    "my-subnet",
			Target:   "aws-target",
			Properties: json.RawMessage(`{
				"CidrBlock": "10.0.1.0/24"
			}`),
			ReadOnlyProperties: json.RawMessage(`{
				"SubnetId": "` + nativeID + `"
			}`),
			Managed: true,
		}

		_, err = ds.StoreResource(managedResource, "apply-command")
		require.NoError(t, err)

		transitionedResource, err := ds.LoadResourceByNativeID(nativeID, "AWS::EC2::Subnet")
		require.NoError(t, err)
		require.NotNil(t, transitionedResource)

		assert.Equal(t, nativeID, transitionedResource.NativeID, "Native ID should be preserved")
		assert.Equal(t, originalResource.Ksuid, transitionedResource.Ksuid, "KSUID should be preserved")
		assert.Equal(t, "production", transitionedResource.Stack)
	})

	t.Run("preserves read only properties during stack transition", func(t *testing.T) {
		ds, err := prepareDatastore()
		require.NoError(t, err)
		defer cleanupDatastore(ds)

		readOnlyProps := json.RawMessage(`{
			"VpcId": "vpc-readonly-test",
			"OwnerId": "123456789012",
			"Arn": "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-readonly-test"
		}`)

		unmanagedResource := &pkgmodel.Resource{
			NativeID:           "vpc-readonly-test",
			Stack:              constants.UnmanagedStack,
			Type:               "AWS::EC2::VPC",
			Label:              "vpc-with-readonly",
			Target:             "aws-target",
			Properties:         json.RawMessage(`{"CidrBlock": "10.2.0.0/16"}`),
			ReadOnlyProperties: readOnlyProps,
			Managed:            false,
		}

		_, err = ds.StoreResource(unmanagedResource, "discovery-command")
		require.NoError(t, err)

		original, err := ds.LoadResourceByNativeID("vpc-readonly-test", "AWS::EC2::VPC")
		require.NoError(t, err)

		managedResource := &pkgmodel.Resource{
			Ksuid:              util.NewID(),
			NativeID:           "vpc-readonly-test",
			Stack:              "app-stack",
			Type:               "AWS::EC2::VPC",
			Label:              "vpc-with-readonly",
			Target:             "aws-target",
			Properties:         json.RawMessage(`{"CidrBlock": "10.2.0.0/16"}`),
			ReadOnlyProperties: readOnlyProps,
			Managed:            true,
		}

		_, err = ds.StoreResource(managedResource, "apply-command")
		require.NoError(t, err)

		transitioned, err := ds.LoadResourceByNativeID("vpc-readonly-test", "AWS::EC2::VPC")
		require.NoError(t, err)

		assert.JSONEq(t, string(readOnlyProps), string(transitioned.ReadOnlyProperties),
			"ReadOnlyProperties should be preserved during stack transition")
		assert.Equal(t, original.Ksuid, transitioned.Ksuid)
		assert.Equal(t, "app-stack", transitioned.Stack)
	})

	t.Run("multiple unmanaged resources transition independently", func(t *testing.T) {
		ds, err := prepareDatastore()
		require.NoError(t, err)
		defer cleanupDatastore(ds)

		vpc1 := &pkgmodel.Resource{
			NativeID:           "vpc-independent-1",
			Stack:              constants.UnmanagedStack,
			Type:               "AWS::EC2::VPC",
			Label:              "vpc-1",
			Target:             "aws-target",
			Properties:         json.RawMessage(`{"CidrBlock": "10.7.0.0/16"}`),
			ReadOnlyProperties: json.RawMessage(`{"VpcId": "vpc-independent-1"}`),
			Managed:            false,
		}

		vpc2 := &pkgmodel.Resource{
			NativeID:           "vpc-independent-2",
			Stack:              constants.UnmanagedStack,
			Type:               "AWS::EC2::VPC",
			Label:              "vpc-2",
			Target:             "aws-target",
			Properties:         json.RawMessage(`{"CidrBlock": "10.8.0.0/16"}`),
			ReadOnlyProperties: json.RawMessage(`{"VpcId": "vpc-independent-2"}`),
			Managed:            false,
		}

		_, err = ds.StoreResource(vpc1, "discovery-1")
		require.NoError(t, err)
		_, err = ds.StoreResource(vpc2, "discovery-2")
		require.NoError(t, err)

		v1Unmanaged, err := ds.LoadResourceByNativeID("vpc-independent-1", "AWS::EC2::VPC")
		require.NoError(t, err)
		v2Unmanaged, err := ds.LoadResourceByNativeID("vpc-independent-2", "AWS::EC2::VPC")
		require.NoError(t, err)

		ksuid1 := v1Unmanaged.Ksuid
		ksuid2 := v2Unmanaged.Ksuid

		vpc1.Stack = "production"
		vpc1.Managed = true
		vpc1.Ksuid = util.NewID()
		_, err = ds.StoreResource(vpc1, "apply-1")
		require.NoError(t, err)

		vpc2.Stack = "development"
		vpc2.Managed = true
		vpc2.Ksuid = util.NewID()
		_, err = ds.StoreResource(vpc2, "apply-2")
		require.NoError(t, err)

		v1Managed, err := ds.LoadResourceByNativeID("vpc-independent-1", "AWS::EC2::VPC")
		require.NoError(t, err)
		v2Managed, err := ds.LoadResourceByNativeID("vpc-independent-2", "AWS::EC2::VPC")
		require.NoError(t, err)

		assert.Equal(t, ksuid1, v1Managed.Ksuid, "VPC 1 KSUID preserved")
		assert.Equal(t, ksuid2, v2Managed.Ksuid, "VPC 2 KSUID preserved")
		assert.Equal(t, "production", v1Managed.Stack)
		assert.Equal(t, "development", v2Managed.Stack)
		assert.NotEqual(t, ksuid1, ksuid2, "Different resources should have different KSUIDs")
	})

	t.Run("handles property changes during stack transition", func(t *testing.T) {
		ds, err := prepareDatastore()
		require.NoError(t, err)
		defer cleanupDatastore(ds)

		unmanagedResource := &pkgmodel.Resource{
			NativeID: "vpc-prop-change",
			Stack:    constants.UnmanagedStack,
			Type:     "AWS::EC2::VPC",
			Label:    "change-vpc",
			Target:   "aws-target",
			Properties: json.RawMessage(`{
				"CidrBlock": "10.4.0.0/16",
				"EnableDnsHostnames": false
			}`),
			ReadOnlyProperties: json.RawMessage(`{
				"VpcId": "vpc-prop-change"
			}`),
			Managed: false,
		}

		_, err = ds.StoreResource(unmanagedResource, "discovery-cmd")
		require.NoError(t, err)

		original, err := ds.LoadResourceByNativeID("vpc-prop-change", "AWS::EC2::VPC")
		require.NoError(t, err)

		managedResource := &pkgmodel.Resource{
			Ksuid:    util.NewID(),
			NativeID: "vpc-prop-change",
			Stack:    "config-stack",
			Type:     "AWS::EC2::VPC",
			Label:    "change-vpc",
			Target:   "aws-target",
			Properties: json.RawMessage(`{
				"CidrBlock": "10.4.0.0/16",
				"EnableDnsHostnames": true
			}`),
			ReadOnlyProperties: json.RawMessage(`{
				"VpcId": "vpc-prop-change"
			}`),
			Managed: true,
		}

		_, err = ds.StoreResource(managedResource, "apply-cmd")
		require.NoError(t, err)

		transitioned, err := ds.LoadResourceByNativeID("vpc-prop-change", "AWS::EC2::VPC")
		require.NoError(t, err)

		assert.Equal(t, original.Ksuid, transitioned.Ksuid, "KSUID preserved despite property change")
		assert.Equal(t, "config-stack", transitioned.Stack)
		assert.JSONEq(t, string(managedResource.Properties), string(transitioned.Properties),
			"Properties should be updated")
	})

	t.Run("batch get KSUIDs works for resources in different stacks", func(t *testing.T) {
		ds, err := prepareDatastore()
		require.NoError(t, err)
		defer cleanupDatastore(ds)

		vpc1 := &pkgmodel.Resource{
			NativeID:   "vpc-batch-1",
			Stack:      constants.UnmanagedStack,
			Type:       "AWS::EC2::VPC",
			Label:      "vpc-1",
			Target:     "aws-target",
			Properties: json.RawMessage(`{"CidrBlock": "10.5.0.0/16"}`),
			Managed:    false,
		}

		vpc2 := &pkgmodel.Resource{
			NativeID:   "vpc-batch-2",
			Stack:      "production",
			Type:       "AWS::EC2::VPC",
			Label:      "vpc-2",
			Target:     "aws-target",
			Properties: json.RawMessage(`{"CidrBlock": "10.6.0.0/16"}`),
			Managed:    true,
		}

		_, err = ds.StoreResource(vpc1, "cmd-1")
		require.NoError(t, err)
		_, err = ds.StoreResource(vpc2, "cmd-2")
		require.NoError(t, err)

		stored1, err := ds.LoadResourceByNativeID("vpc-batch-1", "AWS::EC2::VPC")
		require.NoError(t, err)
		stored2, err := ds.LoadResourceByNativeID("vpc-batch-2", "AWS::EC2::VPC")
		require.NoError(t, err)

		triplets := []pkgmodel.TripletKey{
			{Stack: constants.UnmanagedStack, Label: "vpc-1", Type: "AWS::EC2::VPC"},
			{Stack: "production", Label: "vpc-2", Type: "AWS::EC2::VPC"},
		}

		results, err := ds.BatchGetKSUIDsByTriplets(triplets)
		require.NoError(t, err)
		assert.Len(t, results, 2)

		assert.Equal(t, stored1.Ksuid, results[triplets[0]])
		assert.Equal(t, stored2.Ksuid, results[triplets[1]])
	})
}
