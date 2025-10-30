// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update_test

import (
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareAndCompareResourceForUpdate(t *testing.T) {
	t.Run("identical resources - no changes", func(t *testing.T) {
		resource := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"prop": "value"}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(resource, resource)

		require.NoError(t, err)
		assert.False(t, hasChanges)
		assert.JSONEq(t, `{"prop": "value"}`, string(filteredProps))
	})

	t.Run("SetOnce property preserved - no changes", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"secret": {"$strategy": "SetOnce", "$value": "existing-secret"}
			}`),
		}
		new := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"secret": {"$strategy": "SetOnce", "$value": "new-secret"}
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new)

		require.NoError(t, err)
		assert.False(t, hasChanges)
		assert.JSONEq(t, `{"secret": {"$strategy": "SetOnce", "$value": "existing-secret"}}`, string(filteredProps))
	})

	t.Run("SetOnce property - first time setting allowed", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label:      "test-resource",
			Properties: json.RawMessage(`{}`),
		}
		new := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"secret": {"$strategy": "SetOnce", "$value": "new-secret"}
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new)

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.JSONEq(t, `{"secret": {"$strategy": "SetOnce", "$value": "new-secret"}}`, string(filteredProps))
	})

	t.Run("SetOnce property with no existing value - update allowed", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"secret": {"$strategy": "SetOnce"}
			}`),
		}
		new := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"secret": {"$strategy": "SetOnce", "$value": "new-secret"}
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new)

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.JSONEq(t, `{"secret": {"$strategy": "SetOnce", "$value": "new-secret"}}`, string(filteredProps))
	})

	t.Run("mixed properties - SetOnce preserved, regular updated", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"secret": {"$strategy": "SetOnce", "$value": "existing-secret"},
				"name": "old-name"
			}`),
		}
		new := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"secret": {"$strategy": "SetOnce", "$value": "new-secret"},
				"name": "new-name"
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new)

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.JSONEq(t, `{
			"secret": {"$strategy": "SetOnce", "$value": "existing-secret"},
			"name": "new-name"
		}`, string(filteredProps))
	})

	t.Run("multiple SetOnce properties in different states", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"existing_secret": {"$strategy": "SetOnce", "$value": "existing-value"},
				"empty_secret": {"$strategy": "SetOnce"}
			}`),
		}
		new := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"existing_secret": {"$strategy": "SetOnce", "$value": "new-value"},
				"empty_secret": {"$strategy": "SetOnce", "$value": "first-value"},
				"new_secret": {"$strategy": "SetOnce", "$value": "brand-new"}
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new)

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.JSONEq(t, `{
			"existing_secret": {"$strategy": "SetOnce", "$value": "existing-value"},
			"empty_secret": {"$strategy": "SetOnce", "$value": "first-value"},
			"new_secret": {"$strategy": "SetOnce", "$value": "brand-new"}
		}`, string(filteredProps))
	})

	t.Run("meaningful property changes detected", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"prop": "old-value"}`),
		}
		new := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"prop": "new-value"}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new)

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.JSONEq(t, `{"prop": "new-value"}`, string(filteredProps))
	})

	t.Run("empty arrays ignored - no changes", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"prop": "value"}`),
		}
		new := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"prop": "value", "emptyArray": []}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new)

		require.NoError(t, err)
		assert.False(t, hasChanges)
		assert.JSONEq(t, `{"prop": "value"}`, string(filteredProps))
	})

	t.Run("SetOnce with references", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"vpc_id": {
					"$ref": "formae://vpc.ec2.aws/stack/vpc#/VpcId",
					"$strategy": "SetOnce",
					"$value": "vpc-existing"
				}
			}`),
		}
		new := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"vpc_id": {
					"$ref": "formae://vpc.ec2.aws/stack/vpc#/VpcId",
					"$strategy": "SetOnce",
					"$value": "vpc-new"
				}
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new)

		require.NoError(t, err)
		assert.False(t, hasChanges)
		assert.JSONEq(t, `{
			"vpc_id": {
				"$ref": "formae://vpc.ec2.aws/stack/vpc#/VpcId",
				"$strategy": "SetOnce",
				"$value": "vpc-existing"
			}
		}`, string(filteredProps))
	})

	t.Run("Tags with SetOnce matched by Key not index", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"Tags": [
					{"Key": "FormaeStackLabel", "Value": "my-stack"},
					{"Key": "FormaeResourceLabel", "Value": "test-resource"},
					{"Key": "Name", "Value": "original-name-123"}
				]
			}`),
		}
		newRes := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"Tags": [
					{"Key": "FormaeStackLabel", "Value": "my-stack"},
					{"Key": "FormaeResourceLabel", "Value": "test-resource"},
					{"Key": "Name", "Value": {"$strategy": "SetOnce", "$value": "new-name-456"}}
				]
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes)

		require.NoError(t, err)
		assert.False(t, hasChanges)
		assert.Contains(t, string(filteredProps), "original-name-123")
	})

	t.Run("Tags with SetOnce preserves value when technical tags shift indices", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label: "vpc",
			Properties: json.RawMessage(`{
				"CidrBlock": "10.0.0.0/16",
				"Tags": [
					{"Key": "FormaeStackLabel", "Value": "lifeline"},
					{"Key": "Name", "Value": "lifeline-7484-vpc"},
					{"Key": "FormaeResourceLabel", "Value": "vpc"}
				]
			}`),
		}
		newRes := &pkgmodel.Resource{
			Label: "vpc",
			Properties: json.RawMessage(`{
				"CidrBlock": "10.0.0.0/16",
				"Tags": [
					{"Key": "FormaeStackLabel", "Value": "lifeline"},
					{"Key": "FormaeResourceLabel", "Value": "vpc"},
					{"Key": "Name", "Value": {"$strategy": "SetOnce", "$value": "lifeline-9999-vpc"}}
				]
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes)

		require.NoError(t, err)
		assert.False(t, hasChanges)
		assert.Contains(t, string(filteredProps), "lifeline-7484-vpc")
	})

	t.Run("Tags with SetOnce allows first-time value", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"Tags": [
					{"Key": "Environment", "Value": "prod"}
				]
			}`),
		}
		newRes := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"Tags": [
					{"Key": "Environment", "Value": "prod"},
					{"Key": "Name", "Value": {"$strategy": "SetOnce", "$value": "my-resource-123"}}
				]
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes)

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.Contains(t, string(filteredProps), "my-resource-123")
	})

	t.Run("Tags with multiple SetOnce values preserved independently", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"Tags": [
					{"Key": "Name", "Value": "original-name"},
					{"Key": "Owner", "Value": "original-owner"}
				]
			}`),
		}
		newRes := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"Tags": [
					{"Key": "Name", "Value": {"$strategy": "SetOnce", "$value": "new-name"}},
					{"Key": "Owner", "Value": {"$strategy": "SetOnce", "$value": "new-owner"}},
					{"Key": "Environment", "Value": "prod"}
				]
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes)

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.Contains(t, string(filteredProps), "original-name")
		assert.Contains(t, string(filteredProps), "original-owner")
		assert.Contains(t, string(filteredProps), "prod")
	})

	t.Run("Tags without SetOnce updates normally", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"Tags": [
					{"Key": "Name", "Value": "old-value"}
				]
			}`),
		}
		newRes := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"Tags": [
					{"Key": "Name", "Value": "new-value"}
				]
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes)

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.JSONEq(t, `{
			"Tags": [
				{"Key": "Name", "Value": "new-value"}
			]
		}`, string(filteredProps))
	})

	t.Run("Tags empty in existing resource", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Label:      "test-resource",
			Properties: json.RawMessage(`{"Tags": []}`),
		}
		newRes := &pkgmodel.Resource{
			Label: "test-resource",
			Properties: json.RawMessage(`{
				"Tags": [
					{"Key": "Name", "Value": {"$strategy": "SetOnce", "$value": "new-name"}}
				]
			}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes)

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.Contains(t, string(filteredProps), "new-name")
	})
}
