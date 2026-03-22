// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/jsonpatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJsonEqualIgnoreArrayOrder(t *testing.T) {
	testCases := []struct {
		name     string
		jsonA    string
		jsonB    string
		expected bool
	}{
		{
			name:     "Simple object with string property",
			jsonA:    `{"name": "John"}`,
			jsonB:    `{"name": "John"}`,
			expected: true,
		},
		{
			name:     "Different simple objects",
			jsonA:    `{"name": "John"}`,
			jsonB:    `{"name": "Jane"}`,
			expected: false,
		},
		{
			name:     "Object with array - same order",
			jsonA:    `{"tags": ["a", "b", "c"]}`,
			jsonB:    `{"tags": ["a", "b", "c"]}`,
			expected: true,
		},
		{
			name:     "Object with array - different order",
			jsonA:    `{"tags": ["a", "b", "c"]}`,
			jsonB:    `{"tags": ["c", "a", "b"]}`,
			expected: true,
		},
		{
			name:     "Object with array - different elements",
			jsonA:    `{"tags": ["a", "b", "c"]}`,
			jsonB:    `{"tags": ["a", "b", "d"]}`,
			expected: false,
		},
		{
			name:     "Array with objects - same order",
			jsonA:    `{"data": [{"key": "a", "val": 1}, {"key": "b", "val": 2}]}`,
			jsonB:    `{"data": [{"key": "a", "val": 1}, {"key": "b", "val": 2}]}`,
			expected: true,
		},
		{
			name:     "Array with objects - different order",
			jsonA:    `{"data": [{"key": "a", "val": 1}, {"key": "b", "val": 2}]}`,
			jsonB:    `{"data": [{"key": "b", "val": 2}, {"key": "a", "val": 1}]}`,
			expected: true,
		},
		{
			name:     "Array with objects - different values",
			jsonA:    `{"data": [{"key": "a", "val": 1}, {"key": "b", "val": 2}]}`,
			jsonB:    `{"data": [{"key": "a", "val": 1}, {"key": "b", "val": 3}]}`,
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			a := json.RawMessage(tc.jsonA)
			b := json.RawMessage(tc.jsonB)

			result, err := util.JsonEqualIgnoreArrayOrder(a, b)
			if err != nil {
				t.Fatalf("Error comparing JSONs: %v", err)
			}

			if result != tc.expected {
				t.Errorf("Expected %v but got %v for:\nA: %s\nB: %s",
					tc.expected, result, tc.jsonA, tc.jsonB)
			}
		})
	}
}

func TestCreatePatchDocument_PrimitiveArray(t *testing.T) {
	testCases := []struct {
		name        string
		jsonA       []byte
		jsonB       []byte
		expectedOps []struct {
			op   string
			path string
			val  any
		}
	}{
		{
			name:  "Add element to an empty list - should yield an add operation at index 0",
			jsonA: []byte(`{"label": "a", "tags": []}`),
			jsonB: []byte(`{"label": "a", "tags": ["a"]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{{op: "add", path: "/tags/0", val: "a"}},
		},
		{
			name:  "Add element to a singleton list - should yield an add operation at the end of the array",
			jsonA: []byte(`{"label": "a", "tags": ["a"]}`),
			jsonB: []byte(`{"label": "a", "tags": ["b"]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{{op: "add", path: "/tags/1", val: "b"}},
		},
		{
			name:  "Add element to a list with multiple items - should yield an add operation at the end of the array",
			jsonA: []byte(`{"label": "a", "tags": ["a", "b", "c"]}`),
			jsonB: []byte(`{"label": "a", "tags": ["d"]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{{op: "add", path: "/tags/3", val: "d"}},
		},
		{
			name:  "Add element that already exists - should not yield any patch operations",
			jsonA: []byte(`{"label": "a", "tags": ["a", "b", "c"]}`),
			jsonB: []byte(`{"label": "a", "tags": ["b"]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{},
		},
		{
			name:  "Change label while changing element order - should only yield a replace operation for the label",
			jsonA: []byte(`{"label": "a", "tags": ["a", "b", "c"]}`),
			jsonB: []byte(`{"label": "b", "tags": ["c", "b", "a"]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{{op: "replace", path: "/label", val: "b"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches, err := createPatchDocument(tc.jsonA, tc.jsonB, []string{"label", "tags"}, nil, nil, nil, jsonpatch.Collections{}, nil, jsonpatch.PatchStrategyEnsureExists)
			if err != nil {
				t.Fatalf("Error comparing JSONs: %v", err)
			}

			assert.NoError(t, err)
			assert.Len(t, patches, len(tc.expectedOps))

			for i, expectedOp := range tc.expectedOps {
				assert.Equal(t, expectedOp.op, patches[i].Operation)
				assert.Equal(t, expectedOp.path, patches[i].Path)
				assert.Equal(t, expectedOp.val, patches[i].Value)
			}
		})
	}
}

func TestCreatePatchDocument_ObjectArrayWithKeyValues(t *testing.T) {
	testCases := []struct {
		name        string
		jsonA       []byte
		jsonB       []byte
		expectedOps []struct {
			op   string
			path string
			val  any
		}
	}{
		{
			name:  "Add tag to an empty list - should yield an add operation",
			jsonA: []byte(`{"label": "a", "tags": []}`),
			jsonB: []byte(`{"label": "a", "tags": [{"key":"a", "value":"1"}]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{{op: "add", path: "/tags/0", val: `{"key":"a","value":"1"}`}},
		},
		{
			name:  "Add tag to a singleton list - should yield an add operation",
			jsonA: []byte(`{"label": "a", "tags": [{"key":"a", "value":"1"}]}`),
			jsonB: []byte(`{"label": "a", "tags": [{"key":"b", "value":"3"}]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{{op: "add", path: "/tags/1", val: `{"key":"b","value":"3"}`}},
		},
		{
			name:  "Add element that already exists - should not yield any patch operations",
			jsonA: []byte(`{"label": "a", "tags": [{"key":"a", "value":"1"}]}`),
			jsonB: []byte(`{"label": "a", "tags": [{"key":"a", "value":"1"}]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{},
		},
		{
			name:  "Update an existing key - should yield a replace operation at the index of the existing item",
			jsonA: []byte(`{"label": "a", "tags": [{"key":"a", "value":"1"},{"key":"b", "value":"2"}]}`),
			jsonB: []byte(`{"label": "a", "tags": [{"key":"b", "value":"3"}]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{{op: "replace", path: "/tags/1/value", val: `"3"`}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches, err := createPatchDocument(tc.jsonA, tc.jsonB, []string{"label", "tags"}, nil, nil, nil, jsonpatch.Collections{EntitySets: jsonpatch.EntitySets{jsonpatch.Path("$.tags"): jsonpatch.Key("key")}}, nil, jsonpatch.PatchStrategyEnsureExists)
			if err != nil {
				t.Fatalf("Error comparing JSONs: %v", err)
			}

			assert.NoError(t, err)
			assert.Len(t, patches, len(tc.expectedOps))

			for i, expectedOp := range tc.expectedOps {
				assert.Equal(t, expectedOp.op, patches[i].Operation)
				assert.Equal(t, expectedOp.path, patches[i].Path)
				patchJson, err := json.Marshal(patches[i].Value)
				assert.NoError(t, err)
				assert.Equal(t, expectedOp.val, string(patchJson))
			}
		})
	}
}

func TestCreatePatchDocument_ObjectArrayWithValues(t *testing.T) {
	testCases := []struct {
		name        string
		jsonA       []byte
		jsonB       []byte
		expectedOps []struct {
			op   string
			path string
			val  any
		}
	}{
		{
			name:  "Add tag to the empty list - should yield an add operation",
			jsonA: []byte(`{"label": "a", "tags": []}`),
			jsonB: []byte(`{"label": "a", "tags": [{"key":"a", "value":"1"}]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{{op: "add", path: "/tags/0", val: `{"key":"a","value":"1"}`}},
		},
		{
			name:  "Add tag to a singleton list - should yield an add operation",
			jsonA: []byte(`{"label": "a", "tags": [{"key":"a", "value":"1"}]}`),
			jsonB: []byte(`{"label": "a", "tags": [{"key":"b", "value":"2"}]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{{op: "add", path: "/tags/1", val: `{"key":"b","value":"2"}`}},
		},
		{
			name:  "Add element that already exists - should not yield any patch operations",
			jsonA: []byte(`{"label": "a", "tags": [{"key":"a", "value":"1"}]}`),
			jsonB: []byte(`{"label": "a", "tags": [{"key":"a", "value":"1"}]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{},
		},
		{
			name:  "Add element with overlapping property - should yield an add operation at the end of the array",
			jsonA: []byte(`{"label": "a", "tags": [{"key":"a", "value":"1"},{"key":"b", "value":"2"}]}`),
			jsonB: []byte(`{"label": "a", "tags": [{"key":"b", "value":"3"}]}`),
			expectedOps: []struct {
				op   string
				path string
				val  any
			}{{op: "add", path: "/tags/2", val: `{"key":"b","value":"3"}`}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patches, err := createPatchDocument(tc.jsonA, tc.jsonB, []string{"label", "tags"}, nil, nil, nil, jsonpatch.Collections{}, nil, jsonpatch.PatchStrategyEnsureExists)
			if err != nil {
				t.Fatalf("Error comparing JSONs: %v", err)
			}

			assert.NoError(t, err)
			assert.Len(t, patches, len(tc.expectedOps))

			for i, expectedOp := range tc.expectedOps {
				assert.Equal(t, expectedOp.op, patches[i].Operation)
				assert.Equal(t, expectedOp.path, patches[i].Path)
				patchJson, err := json.Marshal(patches[i].Value)
				assert.NoError(t, err)
				assert.Equal(t, expectedOp.val, string(patchJson))
			}
		})
	}
}

func TestGeneratePatch(t *testing.T) {
	resourceKsuid := util.NewID()

	document := []byte(`{
		"stack": "s",
		"label": "l",
		"ips": [1, 2, 3],
		"val1": {
			"$ref": "somepath",
			"$value": "123"
		},
		"val2": "abc",
		"not_in_forma": "xyz"
	}
	`)
	patch := fmt.Appendf(nil, `{
		"stack": "s1",
		"label": "l1",
		"Tags": [2, 1, 3],
		"val1": "456",
		"val2": {
			"$ref": "formae://%s#/id"
		}
	}`, resourceKsuid)

	schema := pkgmodel.Schema{
		Fields: []string{"stack", "label", "ips", "val1", "val2", "not_in_forma"},
		Hints: map[string]pkgmodel.FieldHint{
			"val2": {
				CreateOnly: true,
			},
		},
	}
	resProps := resolver.NewResolvableProperties()
	resProps.Add(resourceKsuid, "id", "def")

	patchDoc, needsReplacement, err := generatePatch(document, patch, resProps, schema, pkgmodel.FormaApplyModePatch)

	assert.NoError(t, err)
	assert.True(t, needsReplacement)

	var patches []jsonpatch.JsonPatchOperation
	err = json.Unmarshal(patchDoc, &patches)

	assert.NoError(t, err)
	assert.Len(t, patches, 4)
}

// Test that createPatch will resolve references in json objects amd arrays of json objects
func TestGeneratePatch_ShouldResolveRefs(t *testing.T) {
	resourceKsuid := util.NewID()

	document := []byte(`
	{
		"val1": "123",
		"val2": []
	}
	`)
	patch := fmt.Appendf(nil, `
	{
		"val1": {
			"$ref": "formae://%s#/id"
		},
		"val2": [
		{
			"$ref": "formae://%s#/other-id"
		},
		{
			"$ref": "formae://%s#/notha-id"
		}
		]
	}`, resourceKsuid, resourceKsuid, resourceKsuid)

	schema := pkgmodel.Schema{
		Fields: []string{"val1", "val2"},
	}
	props := resolver.NewResolvableProperties()
	props.Add(resourceKsuid, "id", "abc")
	props.Add(resourceKsuid, "other-id", "def")
	props.Add(resourceKsuid, "notha-id", "ghi")

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModePatch)
	assert.NoError(t, err)
	assert.False(t, needsReplacement)

	var patches []jsonpatch.JsonPatchOperation
	err = json.Unmarshal(patchDoc, &patches)
	assert.NoError(t, err)
	require.Len(t, patches, 3)

	assert.ElementsMatch(t, []string{"/val1", "/val2/0", "/val2/1"}, []string{patches[0].Path, patches[1].Path, patches[2].Path})
	assert.ElementsMatch(t, []string{"replace", "add", "add"}, []string{patches[0].Operation, patches[1].Operation, patches[2].Operation})
}

func TestRemoveNonSchemaFields_ThreeFieldsTotalTwoSchemaFields_RemovesNonSchemaField(t *testing.T) {
	document := []byte(`{"a": "a", "b": "b", "c": "c"}`)
	schemaFields := []string{"a", "c"}

	result, err := removeNonSchemaFields(document, schemaFields)
	assert.NoError(t, err)

	var deserialized map[string]string
	err = json.Unmarshal(result, &deserialized)

	assert.NoError(t, err)
	assert.Len(t, deserialized, 2)

	a, ok := deserialized["a"]
	assert.True(t, ok)
	assert.Equal(t, "a", a)

	c, ok := deserialized["c"]
	assert.True(t, ok)
	assert.Equal(t, "c", c)
}

// This reproduces an API Gateway method response issue where the database
// contains objects with both nested structure and flattened keys
func TestGeneratePatch_MixedNestedAndFlattenedStructures(t *testing.T) {
	document := []byte(`{
		"Integration": {
			"IntegrationResponses": [
			{
				"ResponseParameters": {
					"method": {
						"response": {
							"header": {
								"Access-Control-Allow-Headers": "'Content-Type,X-Amz-Date,Authorization,X-Api-Key,X-Amz-Security-Token'",
								"Access-Control-Allow-Methods": "'POST,OPTIONS'",
								"Access-Control-Allow-Origin": "'*'"
							}
						}
					},
					"method.response.header.Access-Control-Allow-Headers": "'Content-Type,X-Amz-Date,Authorization,X-Api-Key,X-Amz-Security-Token'",
					"method.response.header.Access-Control-Allow-Methods": "'POST,OPTIONS'",
					"method.response.header.Access-Control-Allow-Origin": "'*'"
				},
				"StatusCode": "200"
			}
			]
		},
		"MethodResponses": [
		{
			"ResponseParameters": {
				"method": {
					"response": {
						"header": {
							"Access-Control-Allow-Headers": false,
							"Access-Control-Allow-Methods": false,
							"Access-Control-Allow-Origin": false
						}
					}
				},
				"method.response.header.Access-Control-Allow-Headers": false,
				"method.response.header.Access-Control-Allow-Methods": false,
				"method.response.header.Access-Control-Allow-Origin": false
			},
			"StatusCode": "200"
		}
		]
	}`)

	patch := []byte(`{
		"Integration": {
			"IntegrationResponses": [
			{
				"ResponseParameters": {
					"method.response.header.Access-Control-Allow-Headers": "'Content-Type,X-Amz-Date,Authorization,X-Api-Key,X-Amz-Security-Token'",
					"method.response.header.Access-Control-Allow-Methods": "'POST,OPTIONS'",
					"method.response.header.Access-Control-Allow-Origin": "'*'"
				},
				"StatusCode": "200"
			}
			]
		},
		"MethodResponses": [
		{
			"ResponseParameters": {
				"method.response.header.Access-Control-Allow-Headers": false,
				"method.response.header.Access-Control-Allow-Methods": false,
				"method.response.header.Access-Control-Allow-Origin": false
			},
			"StatusCode": "200"
		}
		]
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"Integration", "MethodResponses"},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModePatch)

	assert.NoError(t, err)
	assert.False(t, needsReplacement)

	// There should be no patch operations since the documents represent the same data
	if len(patchDoc) > 0 {
		var patches []jsonpatch.JsonPatchOperation
		err = json.Unmarshal(patchDoc, &patches)
		assert.NoError(t, err)

		t.Logf("Generated %d patch operations (should be 0):", len(patches))
		for _, patch := range patches {
			t.Logf("  %s %s: %v", patch.Operation, patch.Path, patch.Value)
		}
	}

	assert.Empty(t, patchDoc, "Expected no patch operations for logically equivalent documents")
}

func TestCollectionSemanticsFromFieldHints(t *testing.T) {
	hints := map[string]pkgmodel.FieldHint{
		"simpleArray": {
			UpdateMethod: pkgmodel.FieldUpdateMethodArray,
		},
		"entitySet": {
			UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet,
			IndexField:   "id",
		},
		"setField": {
			UpdateMethod: pkgmodel.FieldUpdateMethodSet,
		},
		"normalField": {
			UpdateMethod: pkgmodel.FieldUpdateMethodNone,
		},
	}

	collections := collectionSemanticsFromFieldHints(hints)

	expectedCollections := jsonpatch.Collections{
		Arrays:     []jsonpatch.Path{jsonpatch.Path("$.simpleArray")},
		EntitySets: jsonpatch.EntitySets{jsonpatch.Path("$.entitySet"): jsonpatch.Key("id")},
		Atomics:    []jsonpatch.Path{},
	}

	assert.Equal(t, expectedCollections, collections)
}

func TestGeneratePatch_WriteOnlyFieldsGenerateAddOperation(t *testing.T) {
	// This simulates an AWS CloudControl scenario where:
	// - Password is a writeOnly field (AWS never returns it)
	// - Formae stores the password in its own state
	// - When generating a patch, we need to always include writeOnly fields
	//   even if they haven't changed, because AWS doesn't have them

	// Existing state (what Formae has stored - includes Password)
	document := []byte(`{
		"LoginProfile": {
			"Password": "secret123",
			"PasswordResetRequired": false
		},
		"UserName": "testuser"
	}`)

	// Desired state (from PKL file - same password, but adding tags)
	patch := []byte(`{
		"LoginProfile": {
			"Password": "secret123",
			"PasswordResetRequired": false
		},
		"UserName": "testuser",
		"Tags": [{"Key": "Env", "Value": "test"}]
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"LoginProfile", "UserName", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"LoginProfile.Password": {
				WriteOnly: true,
			},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.False(t, needsReplacement)

	var patches []jsonpatch.JsonPatchOperation
	err = json.Unmarshal(patchDoc, &patches)
	require.NoError(t, err)

	// We expect:
	// 1. An "add" operation for Tags
	// 2. An "add" operation for LoginProfile/Password (because it's writeOnly and
	//    must be re-added since AWS CloudControl won't have it in current state)
	require.Len(t, patches, 2, "Expected 2 operations: one for Tags, one for writeOnly Password")

	// Find the operations by path
	var tagsOp, passwordOp *jsonpatch.JsonPatchOperation
	for i := range patches {
		switch patches[i].Path {
		case "/Tags", "/Tags/0":
			tagsOp = &patches[i]
		case "/LoginProfile/Password":
			passwordOp = &patches[i]
		}
	}

	assert.NotNil(t, tagsOp, "Should have an operation for Tags")
	assert.Equal(t, "add", tagsOp.Operation)

	assert.NotNil(t, passwordOp, "Should have an add operation for writeOnly Password")
	assert.Equal(t, "add", passwordOp.Operation, "WriteOnly fields should use 'add' operation")
	assert.Equal(t, "secret123", passwordOp.Value, "Password value should be preserved")
}

func TestGeneratePatch_AddTagsWhileRetainingExisting(t *testing.T) {
	// This is the existing resource state (from the database) - VPC with 1 tag
	document := []byte(`{
		"Tags": [{"Key": "Name", "Value": "ecg-core"}],
		"CidrBlock": "10.192.0.0/16",
		"InstanceTenancy": "default",
		"EnableDnsSupport": true,
		"EnableDnsHostnames": true
	}`)

	// This is the desired state (from PKL file) - VPC with 3 tags
	// The Name tag is retained, FormaeResourceLabel and FormaeStackLabel are added
	patch := []byte(`{
		"Tags": [
			{"Key": "Name", "Value": "ecg-core"},
			{"Key": "FormaeResourceLabel", "Value": "ecg-core-1"},
			{"Key": "FormaeStackLabel", "Value": "network-stack"}
		],
		"CidrBlock": "10.192.0.0/16",
		"InstanceTenancy": "default",
		"EnableDnsSupport": true,
		"EnableDnsHostnames": true
	}`)

	// Schema with NO EntitySet hints for Tags - this is the key!
	// Old resources discovered before the schema update have empty hints.
	schema := pkgmodel.Schema{
		Fields: []string{"Tags", "CidrBlock", "InstanceTenancy", "EnableDnsSupport", "EnableDnsHostnames"},
		Hints: map[string]pkgmodel.FieldHint{
			// Tags has empty hints (no IndexField, no UpdateMethod)
			// This causes Tags to be treated as a set, not an EntitySet
			"Tags": {},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.False(t, needsReplacement)

	var patches []jsonpatch.JsonPatchOperation
	err = json.Unmarshal(patchDoc, &patches)
	require.NoError(t, err)

	// We expect exactly 2 add operations for the two new tags
	require.Len(t, patches, 2, "Expected 2 add operations for FormaeResourceLabel and FormaeStackLabel")

	// First add should be at /Tags/1 (not /Tags/2!)
	assert.Equal(t, "add", patches[0].Operation)
	assert.Equal(t, "/Tags/1", patches[0].Path, "First new tag should be added at index 1, not 2")

	// Second add should be at /Tags/2 (not /Tags/3!)
	assert.Equal(t, "add", patches[1].Operation)
	assert.Equal(t, "/Tags/2", patches[1].Path, "Second new tag should be added at index 2, not 3")

	// Verify the values are correct
	tag1, ok := patches[0].Value.(map[string]any)
	require.True(t, ok, "First patch value should be a map")
	assert.Equal(t, "FormaeResourceLabel", tag1["Key"])
	assert.Equal(t, "ecg-core-1", tag1["Value"])

	tag2, ok := patches[1].Value.(map[string]any)
	require.True(t, ok, "Second patch value should be a map")
	assert.Equal(t, "FormaeStackLabel", tag2["Key"])
	assert.Equal(t, "network-stack", tag2["Value"])
}

func TestGeneratePatch_HasProviderDefaultFieldsNotRemoved(t *testing.T) {
	// This simulates a scenario where:
	// - BucketEncryption is a field with HasProviderDefault (AWS assigns a default)
	// - The user's PKL file doesn't specify BucketEncryption
	// - The actual state from AWS has BucketEncryption with provider-assigned default
	// - We should NOT generate a "remove" operation for BucketEncryption

	// Existing state (what AWS has - includes provider-assigned BucketEncryption)
	document := []byte(`{
		"BucketName": "my-bucket",
		"BucketEncryption": {
			"ServerSideEncryptionConfiguration": [{
				"ServerSideEncryptionByDefault": {
					"SSEAlgorithm": "AES256"
				}
			}]
		}
	}`)

	// Desired state (from PKL file - user didn't specify BucketEncryption)
	patch := []byte(`{
		"BucketName": "my-bucket"
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"BucketName", "BucketEncryption"},
		Hints: map[string]pkgmodel.FieldHint{
			"BucketEncryption": {
				HasProviderDefault: true,
			},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.False(t, needsReplacement)

	// There should be NO patch operations since BucketEncryption has a provider default
	// and the user didn't specify it (so we accept the provider's default)
	assert.Empty(t, patchDoc, "Expected no patch operations when provider default field is not specified by user")
}

func TestGeneratePatch_HasProviderDefaultFieldsOverridden(t *testing.T) {
	// This simulates a scenario where:
	// - BucketEncryption is a field with HasProviderDefault
	// - The user's PKL file DOES specify BucketEncryption (they want to override the default)
	// - The actual state from AWS has a different BucketEncryption value
	// - We SHOULD generate a patch operation to apply the user's override

	// Existing state (what AWS has - provider-assigned default using AES256)
	document := []byte(`{
		"BucketName": "my-bucket",
		"BucketEncryption": {
			"SSEAlgorithm": "AES256"
		}
	}`)

	// Desired state (from PKL file - user wants KMS encryption instead)
	patch := []byte(`{
		"BucketName": "my-bucket",
		"BucketEncryption": {
			"SSEAlgorithm": "aws:kms",
			"KMSMasterKeyID": "arn:aws:kms:us-east-1:123456789012:key/my-key"
		}
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"BucketName", "BucketEncryption"},
		Hints: map[string]pkgmodel.FieldHint{
			"BucketEncryption": {
				HasProviderDefault: true,
			},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.False(t, needsReplacement)

	// patchDoc should not be empty when there are actual differences
	require.NotEmpty(t, patchDoc, "Expected patch document when user overrides provider default")

	var patches []jsonpatch.JsonPatchOperation
	err = json.Unmarshal(patchDoc, &patches)
	require.NoError(t, err)

	// We expect patch operations to change the encryption settings
	assert.NotEmpty(t, patches, "Expected patch operations when user overrides provider default")

	// Verify that there's at least one operation related to encryption
	hasEncryptionOp := false
	for _, p := range patches {
		if p.Path == "/BucketEncryption" || (len(p.Path) > len("/BucketEncryption") && p.Path[:len("/BucketEncryption")] == "/BucketEncryption") {
			hasEncryptionOp = true
			break
		}
	}
	assert.True(t, hasEncryptionOp, "Expected patch operation for BucketEncryption")
}

func TestRemoveProviderDefaultFields_FieldNotInDesired(t *testing.T) {
	// When field is in document but NOT in patch, it should be removed from document
	document := []byte(`{
		"Name": "test",
		"Encryption": {
			"Algorithm": "AES256"
		}
	}`)

	patch := []byte(`{
		"Name": "test"
	}`)

	result, err := removeProviderDefaultFields(document, patch, []string{"Encryption"})
	require.NoError(t, err)

	var resultMap map[string]any
	err = json.Unmarshal(result, &resultMap)
	require.NoError(t, err)

	assert.Equal(t, "test", resultMap["Name"])
	_, hasEncryption := resultMap["Encryption"]
	assert.False(t, hasEncryption, "Encryption should be removed when not in desired state")
}

func TestRemoveProviderDefaultFields_FieldInDesired(t *testing.T) {
	// When field is in BOTH document and patch, it should NOT be removed from document
	document := []byte(`{
		"Name": "test",
		"Encryption": {
			"Algorithm": "AES256"
		}
	}`)

	patch := []byte(`{
		"Name": "test",
		"Encryption": {
			"Algorithm": "aws:kms"
		}
	}`)

	result, err := removeProviderDefaultFields(document, patch, []string{"Encryption"})
	require.NoError(t, err)

	var resultMap map[string]any
	err = json.Unmarshal(result, &resultMap)
	require.NoError(t, err)

	assert.Equal(t, "test", resultMap["Name"])
	encryption, hasEncryption := resultMap["Encryption"]
	assert.True(t, hasEncryption, "Encryption should NOT be removed when in desired state")
	encMap, ok := encryption.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "AES256", encMap["Algorithm"], "Original encryption value should be preserved")
}

func TestFieldExistsInMap_TopLevel(t *testing.T) {
	obj := map[string]any{
		"Name": "test",
		"Tags": []any{},
	}

	assert.True(t, fieldExistsInMap(obj, []string{"Name"}))
	assert.True(t, fieldExistsInMap(obj, []string{"Tags"}))
	assert.False(t, fieldExistsInMap(obj, []string{"NotPresent"}))
}

func TestFieldExistsInMap_Nested(t *testing.T) {
	obj := map[string]any{
		"Name": "test",
		"Config": map[string]any{
			"Encryption": map[string]any{
				"Algorithm": "AES256",
			},
		},
	}

	assert.True(t, fieldExistsInMap(obj, []string{"Config"}))
	assert.True(t, fieldExistsInMap(obj, []string{"Config", "Encryption"}))
	assert.True(t, fieldExistsInMap(obj, []string{"Config", "Encryption", "Algorithm"}))
	assert.False(t, fieldExistsInMap(obj, []string{"Config", "NotPresent"}))
	assert.False(t, fieldExistsInMap(obj, []string{"Config", "Encryption", "NotPresent"}))
}

func TestFieldExistsInMap_EmptyPath(t *testing.T) {
	obj := map[string]any{
		"Name": "test",
	}

	assert.False(t, fieldExistsInMap(obj, []string{}))
}

func TestRemoveProviderDefaultFields_NestedField(t *testing.T) {
	// Test nested field path like "BucketEncryption.ServerSideEncryptionConfiguration"
	document := []byte(`{
		"Name": "test",
		"BucketEncryption": {
			"ServerSideEncryptionConfiguration": [{
				"SSEAlgorithm": "AES256"
			}],
			"OtherSetting": "value"
		}
	}`)

	patch := []byte(`{
		"Name": "test",
		"BucketEncryption": {
			"OtherSetting": "value"
		}
	}`)

	// Only remove the nested field, not the parent
	result, err := removeProviderDefaultFields(document, patch, []string{"BucketEncryption.ServerSideEncryptionConfiguration"})
	require.NoError(t, err)

	var resultMap map[string]any
	err = json.Unmarshal(result, &resultMap)
	require.NoError(t, err)

	assert.Equal(t, "test", resultMap["Name"])

	bucketEncryption, hasBE := resultMap["BucketEncryption"]
	assert.True(t, hasBE, "BucketEncryption should still exist")

	beMap, ok := bucketEncryption.(map[string]any)
	require.True(t, ok)

	_, hasSSEC := beMap["ServerSideEncryptionConfiguration"]
	assert.False(t, hasSSEC, "ServerSideEncryptionConfiguration should be removed")

	assert.Equal(t, "value", beMap["OtherSetting"], "OtherSetting should be preserved")
}

func TestRemoveProviderDefaultFields_EmptyList(t *testing.T) {
	// When no fields are specified, document should be unchanged
	document := []byte(`{
		"Name": "test",
		"Encryption": "AES256"
	}`)

	result, err := removeProviderDefaultFields(document, []byte(`{"Name": "test"}`), []string{})
	require.NoError(t, err)

	assert.Equal(t, string(document), string(result))
}

func TestGeneratePatch_ReconcileRemovesOOBTagsWhenDesiredIsEmptyArray(t *testing.T) {
	// Existing state: resource has an OOB tag added outside of formae
	document := []byte(`{
		"Name": "test-resource",
		"Tags": [{"Key": "OOBTag", "Value": "oob-value"}]
	}`)

	// Desired state: user's PKL has no tags, which should produce an empty array
	// (once PKL nullability is fixed; for now we test with explicit empty array)
	patch := []byte(`{
		"Name": "test-resource",
		"Tags": []
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {UpdateMethod: "EntitySet", IndexField: "Key"},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.False(t, needsReplacement)

	// The patch must not be nil — there IS a difference (OOB tag needs removal)
	require.NotNil(t, patchDoc, "expected a patch to remove the OOB tag, got nil")

	var patches []jsonpatch.JsonPatchOperation
	err = json.Unmarshal(patchDoc, &patches)
	require.NoError(t, err)

	require.NotEmpty(t, patches, "expected at least one patch operation to remove the OOB tag")

	// We expect a remove operation for the OOB tag
	assert.Equal(t, "remove", patches[0].Operation)
}

func TestGeneratePatch_ReconcileRemovesOOBTagsWhenDesiredIsNull(t *testing.T) {
	document := []byte(`{"Name": "test-resource", "Tags": [{"Key": "OOBTag", "Value": "oob-value"}]}`)
	patch := []byte(`{"Name": "test-resource", "Tags": null}`)
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints:  map[string]pkgmodel.FieldHint{"Tags": {UpdateMethod: "EntitySet", IndexField: "Key"}},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.False(t, needsReplacement)
	require.NotNil(t, patchDoc, "expected a patch to handle the OOB tag, got nil")

	var patches []jsonpatch.JsonPatchOperation
	err = json.Unmarshal(patchDoc, &patches)
	require.NoError(t, err)
	require.NotEmpty(t, patches)
	// jsonpatch generates "replace" (null replaces the array) rather than "remove",
	// both achieve the same result via CloudControl.
	assert.Equal(t, "replace", patches[0].Operation)
	assert.Equal(t, "/Tags", patches[0].Path)
}

func TestHasValue(t *testing.T) {
	tests := []struct {
		name     string
		val      any
		expected bool
	}{
		{name: "nil", val: nil, expected: true},
		{name: "empty string", val: "", expected: false},
		{name: "non-empty string", val: "hello", expected: true},
		{name: "empty array", val: []any{}, expected: true},
		{name: "non-empty array", val: []any{"a"}, expected: true},
		{name: "empty map", val: map[string]any{}, expected: true},
		{name: "non-empty map", val: map[string]any{"k": "v"}, expected: true},
		{name: "integer", val: 42, expected: true},
		{name: "boolean false", val: false, expected: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, hasValue(tc.val))
		})
	}
}

func TestRemoveProviderDefaultFields_NestedFieldInsideArray(t *testing.T) {
	// Simulates ECS ContainerDefinitions: provider default fields (Cpu, Essential)
	// are nested inside an array of sub-resources. The path "ContainerDefinitions.Cpu"
	// must traverse the array and remove Cpu from each element.
	document := []byte(`{
		"Family": "my-task",
		"ContainerDefinitions": [
			{"Name": "app", "Image": "nginx", "Cpu": 0, "Essential": true},
			{"Name": "sidecar", "Image": "envoy", "Cpu": 0, "Essential": false}
		]
	}`)

	patch := []byte(`{
		"Family": "my-task",
		"ContainerDefinitions": [
			{"Name": "app", "Image": "nginx", "Essential": true},
			{"Name": "sidecar", "Image": "envoy", "Essential": false}
		]
	}`)

	// Cpu has provider default and is NOT in desired state → should be removed from document
	// Essential has provider default but IS in desired state → should NOT be removed
	result, err := removeProviderDefaultFields(document, patch, []string{"ContainerDefinitions.Cpu", "ContainerDefinitions.Essential"})
	require.NoError(t, err)

	var resultMap map[string]any
	err = json.Unmarshal(result, &resultMap)
	require.NoError(t, err)

	containers := resultMap["ContainerDefinitions"].([]any)
	require.Len(t, containers, 2)

	// Cpu should be removed from both containers (not in desired state)
	container0 := containers[0].(map[string]any)
	_, hasCpu0 := container0["Cpu"]
	assert.False(t, hasCpu0, "Cpu should be removed from first container when not in desired state")

	container1 := containers[1].(map[string]any)
	_, hasCpu1 := container1["Cpu"]
	assert.False(t, hasCpu1, "Cpu should be removed from second container when not in desired state")

	// Essential should be kept (present in desired state)
	_, hasEssential0 := container0["Essential"]
	assert.True(t, hasEssential0, "Essential should be kept when present in desired state")
	_, hasEssential1 := container1["Essential"]
	assert.True(t, hasEssential1, "Essential should be kept when present in desired state")
}

func TestFieldExistsInMap_ArrayTraversal(t *testing.T) {
	obj := map[string]any{
		"ContainerDefinitions": []any{
			map[string]any{"Name": "app", "Image": "nginx", "Cpu": float64(0)},
			map[string]any{"Name": "sidecar", "Image": "envoy"},
		},
	}

	// Cpu exists in at least one array element
	assert.True(t, fieldExistsInMap(obj, []string{"ContainerDefinitions", "Cpu"}))
	// Name exists in array elements
	assert.True(t, fieldExistsInMap(obj, []string{"ContainerDefinitions", "Name"}))
	// NotPresent doesn't exist in any element
	assert.False(t, fieldExistsInMap(obj, []string{"ContainerDefinitions", "NotPresent"}))
}

func TestRemoveNestedField_ArrayTraversal(t *testing.T) {
	obj := map[string]any{
		"Items": []any{
			map[string]any{"Name": "a", "Secret": "x"},
			map[string]any{"Name": "b", "Secret": "y"},
		},
	}

	removeNestedField(obj, []string{"Items", "Secret"})

	items := obj["Items"].([]any)
	item0 := items[0].(map[string]any)
	item1 := items[1].(map[string]any)

	assert.Equal(t, "a", item0["Name"])
	_, hasSecret0 := item0["Secret"]
	assert.False(t, hasSecret0, "Secret should be removed from first element")

	assert.Equal(t, "b", item1["Name"])
	_, hasSecret1 := item1["Secret"]
	assert.False(t, hasSecret1, "Secret should be removed from second element")
}

func TestGeneratePatch_ProviderDefaultInsideArray_NoPatch(t *testing.T) {
	// End-to-end test: provider default fields inside arrays should not generate
	// patch operations when the user hasn't specified them.
	document := []byte(`{
		"Family": "my-task",
		"ContainerDefinitions": [
			{"Name": "app", "Image": "nginx", "Cpu": 0, "DockerLabels": {}}
		]
	}`)

	patch := []byte(`{
		"Family": "my-task",
		"ContainerDefinitions": [
			{"Name": "app", "Image": "nginx", "DockerLabels": {}}
		]
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"Family", "ContainerDefinitions"},
		Hints: map[string]pkgmodel.FieldHint{
			"ContainerDefinitions":     {CreateOnly: true},
			"ContainerDefinitions.Cpu": {HasProviderDefault: true},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, _, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, patchDoc, "Expected no patch when only difference is provider default Cpu inside array")
}

func TestRemoveNonSchemaFields_PreservesEmptyArraysAndMaps(t *testing.T) {
	document := []byte(`{"Name": "test", "Tags": [], "Metadata": {}}`)
	schemaFields := []string{"Name", "Tags", "Metadata"}

	result, err := removeNonSchemaFields(document, schemaFields)
	require.NoError(t, err)

	var deserialized map[string]any
	err = json.Unmarshal(result, &deserialized)
	require.NoError(t, err)

	assert.Len(t, deserialized, 3)
	assert.Equal(t, "test", deserialized["Name"])
	assert.Equal(t, []any{}, deserialized["Tags"])
	assert.Equal(t, map[string]any{}, deserialized["Metadata"])
}

func TestGeneratePatch_AtomicField_SingleReplace(t *testing.T) {
	// Actual state has a policy document with different Statement content
	document := []byte(`{"PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}]}, "Queue": "my-queue"}`)
	// Desired state has updated Statement
	patch := []byte(`{"PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "s3:PutObject", "Resource": "arn:aws:s3:::my-bucket"}]}, "Queue": "my-queue"}`)

	schema := pkgmodel.Schema{
		Identifier: "Ref",
		Fields:     []string{"PolicyDocument", "Queue"},
		Hints: map[string]pkgmodel.FieldHint{
			"PolicyDocument": {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic},
			"Queue":          {CreateOnly: true},
		},
	}

	patchDoc, needsReplacement, err := generatePatch(document, patch, resolver.NewResolvableProperties(), schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.False(t, needsReplacement)

	var ops []jsonpatch.JsonPatchOperation
	err = json.Unmarshal(patchDoc, &ops)
	require.NoError(t, err)

	// Should be a single replace on /PolicyDocument, not recursive ops on /PolicyDocument/Statement/0/Action etc.
	assert.Len(t, ops, 1, "Expected single atomic replace operation")
	assert.Equal(t, "replace", ops[0].Operation)
	assert.Equal(t, "/PolicyDocument", ops[0].Path)
}

func TestGeneratePatch_AtomicField_NoDiffNoPatch(t *testing.T) {
	doc := []byte(`{"PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow"}]}, "Queue": "q"}`)

	schema := pkgmodel.Schema{
		Identifier: "Ref",
		Fields:     []string{"PolicyDocument", "Queue"},
		Hints: map[string]pkgmodel.FieldHint{
			"PolicyDocument": {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic},
			"Queue":          {},
		},
	}

	patchDoc, needsReplacement, err := generatePatch(doc, doc, resolver.NewResolvableProperties(), schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.False(t, needsReplacement)
	assert.Empty(t, patchDoc, "Expected no patch when atomic field is identical")
}

func TestGeneratePatch_EmptyArrayOnCreateOnlyField_NoPatch(t *testing.T) {
	// Simulates the 0.83.0 PKL schema rendering unset nullable Listing fields
	// as []. For createOnly fields this should not generate a patch operation,
	// since the user can't modify them after creation.
	document := []byte(`{
		"DomainName": "example.com"
	}`)

	// Desired state has empty arrays for createOnly Listing fields (PKL rendering)
	patch := []byte(`{
		"DomainName": "example.com",
		"DomainNameServers": [],
		"NtpServers": []
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"DomainName", "DomainNameServers", "NtpServers"},
		Hints: map[string]pkgmodel.FieldHint{
			"DomainNameServers": {CreateOnly: true},
			"NtpServers":        {CreateOnly: true},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.False(t, needsReplacement, "Empty arrays on createOnly fields should not trigger replacement")
	assert.Empty(t, patchDoc, "Expected no patch when only difference is empty arrays on createOnly fields")
}

func TestGeneratePatch_NonEmptyArrayOnCreateOnlyField_TriggersReplacement(t *testing.T) {
	// A real change to a createOnly field should still trigger replacement.
	document := []byte(`{
		"DomainName": "example.com"
	}`)

	patch := []byte(`{
		"DomainName": "example.com",
		"DomainNameServers": ["8.8.8.8"]
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"DomainName", "DomainNameServers"},
		Hints: map[string]pkgmodel.FieldHint{
			"DomainNameServers": {CreateOnly: true},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.True(t, needsReplacement, "Non-empty change to createOnly field should trigger replacement")
	assert.NotEmpty(t, patchDoc)
}

func TestGeneratePatch_EmptyArrayOnNonCreateOnlyField_NoPatch(t *testing.T) {
	// Empty arrays on non-createOnly fields should also be filtered. The PKL
	// schema renders unset nullable Listings as []. An "add" of [] is never
	// user intent — it's PKL rendering noise.
	document := []byte(`{
		"GroupName": "my-group"
	}`)

	patch := []byte(`{
		"GroupName": "my-group",
		"ManagedPolicyArns": [],
		"Policies": []
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"GroupName", "ManagedPolicyArns", "Policies"},
		Hints:  map[string]pkgmodel.FieldHint{},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, _, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.Empty(t, patchDoc, "Expected no patch when only difference is empty arrays on non-createOnly fields")
}

func TestGeneratePatch_ReplaceNonEmptyArrayPreserved(t *testing.T) {
	// A "replace" of an existing field with [] should be preserved (user
	// clearing a collection).
	document := []byte(`{
		"Name": "test",
		"Tags": [{"Key": "a", "Value": "1"}]
	}`)

	patch := []byte(`{
		"Name": "test",
		"Tags": []
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints:  map[string]pkgmodel.FieldHint{},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, _, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.NotEmpty(t, patchDoc, "Expected patch when user clears a collection via replace")
}

func TestEntitySetProviderDefaultsFromHints(t *testing.T) {
	hints := map[string]pkgmodel.FieldHint{
		"LoadBalancerAttributes": {
			HasProviderDefault: true,
			UpdateMethod:       pkgmodel.FieldUpdateMethodEntitySet,
			IndexField:         "Key",
		},
		"Tags": {
			UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet,
			IndexField:   "Key",
		},
		"BucketEncryption": {
			HasProviderDefault: true,
		},
		"NormalField": {},
	}

	result := entitySetProviderDefaultsFromHints(hints)

	assert.Len(t, result, 1)
	assert.Equal(t, "Key", result["LoadBalancerAttributes"])
	_, hasTags := result["Tags"]
	assert.False(t, hasTags, "Tags should not be included (no hasProviderDefault)")
	_, hasBE := result["BucketEncryption"]
	assert.False(t, hasBE, "BucketEncryption should not be included (not an EntitySet)")
}

func TestRemoveProviderDefaultEntitySetElements_FiltersUnmatchedElements(t *testing.T) {
	// Simulates AWS ELBv2 LoadBalancer: AWS returns ~22 default attributes,
	// user only specifies 2. Unmatched elements should be removed from document.
	document := []byte(`{
		"LoadBalancerName": "my-lb",
		"LoadBalancerAttributes": [
			{"Key": "idle_timeout.timeout_seconds", "Value": "60"},
			{"Key": "routing.http2.enabled", "Value": "true"},
			{"Key": "access_logs.s3.enabled", "Value": "false"},
			{"Key": "deletion_protection.enabled", "Value": "false"}
		]
	}`)

	patch := []byte(`{
		"LoadBalancerName": "my-lb",
		"LoadBalancerAttributes": [
			{"Key": "idle_timeout.timeout_seconds", "Value": "120"}
		]
	}`)

	entitySetDefaults := map[string]string{
		"LoadBalancerAttributes": "Key",
	}

	result, err := removeProviderDefaultEntitySetElements(document, patch, entitySetDefaults)
	require.NoError(t, err)

	var resultMap map[string]any
	err = json.Unmarshal(result, &resultMap)
	require.NoError(t, err)

	attrs := resultMap["LoadBalancerAttributes"].([]any)
	require.Len(t, attrs, 1, "Should only retain elements whose key matches desired state")

	attr0 := attrs[0].(map[string]any)
	assert.Equal(t, "idle_timeout.timeout_seconds", attr0["Key"])
	assert.Equal(t, "60", attr0["Value"], "Original value should be preserved for comparison")
}

func TestRemoveProviderDefaultEntitySetElements_DesiredFieldAbsent(t *testing.T) {
	// When desired state doesn't have the field at all, remove entire array from document
	document := []byte(`{
		"Name": "test",
		"Attributes": [
			{"Key": "a", "Value": "1"},
			{"Key": "b", "Value": "2"}
		]
	}`)

	patch := []byte(`{"Name": "test"}`)

	entitySetDefaults := map[string]string{
		"Attributes": "Key",
	}

	result, err := removeProviderDefaultEntitySetElements(document, patch, entitySetDefaults)
	require.NoError(t, err)

	var resultMap map[string]any
	err = json.Unmarshal(result, &resultMap)
	require.NoError(t, err)

	_, hasAttrs := resultMap["Attributes"]
	assert.False(t, hasAttrs, "Attributes should be removed when not in desired state")
}

func TestRemoveProviderDefaultEntitySetElements_EmptyMap(t *testing.T) {
	document := []byte(`{"Name": "test", "Attrs": [{"Key": "a"}]}`)

	result, err := removeProviderDefaultEntitySetElements(document, document, nil)
	require.NoError(t, err)
	assert.Equal(t, string(document), string(result), "Should be unchanged with nil entitySetDefaults")
}

func TestGeneratePatch_EntitySetProviderDefaults_PatchMode(t *testing.T) {
	// End-to-end test: ELBv2 LoadBalancer with provider-default attributes.
	// AWS returns 4 attributes, user specifies 1. In patch mode, only the
	// user-specified attribute should be compared; the rest should be ignored.
	document := []byte(`{
		"LoadBalancerName": "my-lb",
		"LoadBalancerAttributes": [
			{"Key": "idle_timeout.timeout_seconds", "Value": "60"},
			{"Key": "routing.http2.enabled", "Value": "true"},
			{"Key": "access_logs.s3.enabled", "Value": "false"},
			{"Key": "deletion_protection.enabled", "Value": "false"}
		]
	}`)

	patch := []byte(`{
		"LoadBalancerName": "my-lb",
		"LoadBalancerAttributes": [
			{"Key": "idle_timeout.timeout_seconds", "Value": "60"}
		]
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"LoadBalancerName", "LoadBalancerAttributes"},
		Hints: map[string]pkgmodel.FieldHint{
			"LoadBalancerAttributes": {
				HasProviderDefault: true,
				UpdateMethod:       pkgmodel.FieldUpdateMethodEntitySet,
				IndexField:         "Key",
			},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.False(t, needsReplacement)
	assert.Empty(t, patchDoc, "Expected no patch when user-specified attribute matches actual")
}

func TestGeneratePatch_EntitySetProviderDefaults_WithUserChange(t *testing.T) {
	// User changes idle_timeout from 60 to 120. Only that attribute should appear in patch.
	document := []byte(`{
		"LoadBalancerName": "my-lb",
		"LoadBalancerAttributes": [
			{"Key": "idle_timeout.timeout_seconds", "Value": "60"},
			{"Key": "routing.http2.enabled", "Value": "true"},
			{"Key": "access_logs.s3.enabled", "Value": "false"},
			{"Key": "deletion_protection.enabled", "Value": "false"}
		]
	}`)

	patch := []byte(`{
		"LoadBalancerName": "my-lb",
		"LoadBalancerAttributes": [
			{"Key": "idle_timeout.timeout_seconds", "Value": "120"}
		]
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"LoadBalancerName", "LoadBalancerAttributes"},
		Hints: map[string]pkgmodel.FieldHint{
			"LoadBalancerAttributes": {
				HasProviderDefault: true,
				UpdateMethod:       pkgmodel.FieldUpdateMethodEntitySet,
				IndexField:         "Key",
			},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.False(t, needsReplacement)
	require.NotEmpty(t, patchDoc)

	var ops []jsonpatch.JsonPatchOperation
	err = json.Unmarshal(patchDoc, &ops)
	require.NoError(t, err)

	// Should get a replace on the Value of the matched element
	require.Len(t, ops, 1, "Expected exactly one patch operation for the changed attribute")
	assert.Equal(t, "replace", ops[0].Operation)
	assert.Contains(t, ops[0].Path, "Value")
}

func TestGeneratePatch_EntitySetProviderDefaults_ReconcileMode(t *testing.T) {
	// In reconcile mode, unmatched provider-default elements should also be stripped
	// to prevent generating remove operations for provider defaults.
	document := []byte(`{
		"Name": "my-lb",
		"Attributes": [
			{"Key": "a", "Value": "1"},
			{"Key": "b", "Value": "2"},
			{"Key": "c", "Value": "3"}
		]
	}`)

	patch := []byte(`{
		"Name": "my-lb",
		"Attributes": [
			{"Key": "a", "Value": "1"}
		]
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Attributes"},
		Hints: map[string]pkgmodel.FieldHint{
			"Attributes": {
				HasProviderDefault: true,
				UpdateMethod:       pkgmodel.FieldUpdateMethodEntitySet,
				IndexField:         "Key",
			},
		},
	}
	props := resolver.NewResolvableProperties()

	patchDoc, _, err := generatePatch(document, patch, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, patchDoc, "Expected no patch when only provider-default elements differ in reconcile mode")
}
