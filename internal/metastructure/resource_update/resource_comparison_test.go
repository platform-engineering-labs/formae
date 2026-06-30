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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(resource, resource, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new, pkgmodel.Schema{})

		require.NoError(t, err)
		assert.False(t, hasChanges)
		assert.JSONEq(t, `{"prop": "value", "emptyArray": []}`, string(filteredProps))
	})

	t.Run("null field vs absent - no changes", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"prop": "value"}`),
		}
		new := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"prop": "value", "Tags": null}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new, pkgmodel.Schema{})

		require.NoError(t, err)
		assert.False(t, hasChanges)
		assert.JSONEq(t, `{"prop": "value", "Tags": null}`, string(filteredProps))
	})

	t.Run("null field vs non-empty array - changes detected", func(t *testing.T) {
		existing := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"Tags": [{"Key":"k","Value":"v"}]}`),
		}
		new := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"Tags": null}`),
		}

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new, pkgmodel.Schema{})

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.JSONEq(t, `{"Tags": null}`, string(filteredProps))
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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, new, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, pkgmodel.Schema{})

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

		hasChanges, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, pkgmodel.Schema{})

		require.NoError(t, err)
		assert.True(t, hasChanges)
		assert.Contains(t, string(filteredProps), "new-name")
	})
}

func TestGate_SerializationOnlyConfigJson_NoChange(t *testing.T) {
	schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"configJson": {Format: "json"}}}
	existing := &pkgmodel.Resource{Properties: json.RawMessage(`{"configJson":"{\"a\":1,\"b\":2}"}`)}
	// same content, different serialization (pretty + reordered keys)
	newRes := &pkgmodel.Resource{Properties: json.RawMessage(`{"configJson":"{\n  \"b\": 2,\n  \"a\": 1\n}"}`)}

	hasChanges, _, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, schema)
	if err != nil {
		t.Fatal(err)
	}
	if hasChanges {
		t.Fatal("serialization-only difference must not be a change")
	}
}

func TestGate_GenuineConfigJsonChange_IsChange(t *testing.T) {
	schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"configJson": {Format: "json"}}}
	existing := &pkgmodel.Resource{Properties: json.RawMessage(`{"configJson":"{\"a\":1}"}`)}
	newRes := &pkgmodel.Resource{Properties: json.RawMessage(`{"configJson":"{\"a\":2}"}`)}

	hasChanges, _, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, schema)
	if err != nil {
		t.Fatal(err)
	}
	if !hasChanges {
		t.Fatal("genuine content change must be a change")
	}
}

func TestGate_NoHint_UnchangedBehavior(t *testing.T) {
	schema := pkgmodel.Schema{} // no Format hint
	existing := &pkgmodel.Resource{Properties: json.RawMessage(`{"configJson":"{\"a\":1}"}`)}
	newRes := &pkgmodel.Resource{Properties: json.RawMessage(`{"configJson":"{ \"a\": 1 }"}`)}

	hasChanges, _, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, schema)
	if err != nil {
		t.Fatal(err)
	}
	if !hasChanges {
		t.Fatal("without a Format hint, a serialization difference is still a raw string change")
	}
}

func TestGate_HintedResolvableLeaf_SkippedNoPanic(t *testing.T) {
	schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"configJson": {Format: "json"}}}
	// configJson is itself a resolvable envelope (object), not a plain string.
	props := json.RawMessage(`{"configJson":{"$ref":"formae://x","$value":"{\"a\":1}"}}`)
	existing := &pkgmodel.Resource{Properties: props}
	newRes := &pkgmodel.Resource{Properties: props}
	if _, _, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, schema); err != nil {
		t.Fatalf("must not panic/error on a resolvable leaf: %v", err)
	}
}

// TestGate_HintedResolvableLeaf_SameContent_NoChange asserts that when a hinted
// field is a resolvable ENVELOPE (object, not a plain string) the canonicalize
// skip (val.Type != gjson.String) leaves comparison to the raw/structural
// compare — which sees identical envelopes and reports no change.
func TestGate_HintedResolvableLeaf_SameContent_NoChange(t *testing.T) {
	schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"configJson": {Format: "json"}}}
	existing := &pkgmodel.Resource{Properties: json.RawMessage(`{"configJson":{"$ref":"formae://x","$value":"{\"a\":1}"}}`)}
	newRes := &pkgmodel.Resource{Properties: json.RawMessage(`{"configJson":{"$ref":"formae://x","$value":"{\"a\":1}"}}`)}

	hasChanges, _, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, schema)
	require.NoError(t, err)
	assert.False(t, hasChanges, "identical resolvable envelopes must not be a change")
}

// TestGate_HintedResolvableLeaf_DifferentContent_IsChange is the behavioural
// counterpart to the skip guard. The hinted field is a resolvable ENVELOPE whose
// $value differs between the two sides. Because the field is an object (not a
// gjson.String), canonicalization is skipped and the raw/structural compare must
// still detect the genuine difference. Deleting or inverting the
// `val.Type != gjson.String` guard (canonicalizing the envelope object instead)
// would let this real change slip through — this test catches that.
func TestGate_HintedResolvableLeaf_DifferentContent_IsChange(t *testing.T) {
	schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"configJson": {Format: "json"}}}
	existing := &pkgmodel.Resource{Properties: json.RawMessage(`{"configJson":{"$ref":"formae://x","$value":"{\"a\":1}"}}`)}
	newRes := &pkgmodel.Resource{Properties: json.RawMessage(`{"configJson":{"$ref":"formae://x","$value":"{\"a\":2}"}}`)}

	hasChanges, _, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes, schema)
	require.NoError(t, err)
	assert.True(t, hasChanges, "differing resolvable envelope $value must be detected as a change")
}
