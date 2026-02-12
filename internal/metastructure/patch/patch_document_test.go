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

	for _, tc := range testCases[1:2] {
		t.Run(tc.name, func(t *testing.T) {
			patches, err := createPatchDocument(tc.jsonA, tc.jsonB, []string{"label", "tags"}, nil, nil, jsonpatch.Collections{}, nil, jsonpatch.PatchStrategyEnsureExists)
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
			patches, err := createPatchDocument(tc.jsonA, tc.jsonB, []string{"label", "tags"}, nil, nil, jsonpatch.Collections{EntitySets: jsonpatch.EntitySets{jsonpatch.Path("$.tags"): jsonpatch.Key("key")}}, nil, jsonpatch.PatchStrategyEnsureExists)
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
			patches, err := createPatchDocument(tc.jsonA, tc.jsonB, []string{"label", "tags"}, nil, nil, jsonpatch.Collections{}, nil, jsonpatch.PatchStrategyEnsureExists)
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
