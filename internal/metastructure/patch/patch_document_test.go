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
	"github.com/platform-engineering-labs/formae/pkg/model"
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
			patches, err := createPatchDocument(tc.jsonA, tc.jsonB, []string{"label", "tags"}, jsonpatch.Collections{}, jsonpatch.PatchStrategyEnsureExists)
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
			patches, err := createPatchDocument(tc.jsonA, tc.jsonB, []string{"label", "tags"}, jsonpatch.Collections{EntitySets: jsonpatch.EntitySets{jsonpatch.Path("$.tags"): jsonpatch.Key("key")}}, jsonpatch.PatchStrategyEnsureExists)
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
			patches, err := createPatchDocument(tc.jsonA, tc.jsonB, []string{"label", "tags"}, jsonpatch.Collections{}, jsonpatch.PatchStrategyEnsureExists)
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

	schemaFields := []string{"stack", "label", "ips", "val1", "val2", "not_in_forma"}
	createOnlyFields := []string{"val2"}
	resProps := resolver.NewResolvableProperties()
	resProps.Add(resourceKsuid, "id", "def")

	patchDoc, needsReplacement, err := generatePatch(document, patch, resProps, schemaFields, createOnlyFields, model.FormaApplyModePatch)

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

	schemaFields := []string{"val1", "val2"}
	props := resolver.NewResolvableProperties()
	props.Add(resourceKsuid, "id", "abc")
	props.Add(resourceKsuid, "other-id", "def")
	props.Add(resourceKsuid, "notha-id", "ghi")

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schemaFields, nil, model.FormaApplyModePatch)
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

	schemaFields := []string{"Integration", "MethodResponses"}
	props := resolver.NewResolvableProperties()

	patchDoc, needsReplacement, err := generatePatch(document, patch, props, schemaFields, nil, model.FormaApplyModePatch)

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
