// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func suppressedDiffsForTest(t *testing.T, oldProps, newProps, desired string, schema pkgmodel.Schema) []SuppressedFieldDiff {
	t.Helper()
	diffs, err := SuppressedFieldDiffs(json.RawMessage(oldProps), json.RawMessage(newProps), json.RawMessage(desired), schema)
	require.NoError(t, err)
	return diffs
}

func TestSuppressedFieldDiffs_ScalarOmittedChanged_ReportsMovement(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "EnableKeyRotation"},
		Hints:  map[string]pkgmodel.FieldHint{"EnableKeyRotation": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "k", "EnableKeyRotation": false}`,
		`{"Name": "k", "EnableKeyRotation": true}`,
		`{"Name": "k"}`,
		schema)

	require.Len(t, diffs, 1)
	assert.Equal(t, "EnableKeyRotation", diffs[0].Path)
	assert.JSONEq(t, `false`, string(diffs[0].From))
	assert.JSONEq(t, `true`, string(diffs[0].To))
	assert.False(t, diffs[0].Opaque)
}

func TestSuppressedFieldDiffs_ScalarOmittedUnchanged_NoDiff(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "EnableKeyRotation"},
		Hints:  map[string]pkgmodel.FieldHint{"EnableKeyRotation": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "k", "EnableKeyRotation": false}`,
		`{"Name": "k", "EnableKeyRotation": false}`,
		`{"Name": "k"}`,
		schema)

	assert.Empty(t, diffs)
}

func TestSuppressedFieldDiffs_DeclaredField_NeverReported(t *testing.T) {
	// A field the user declares is plan territory, not suppressed content,
	// no matter how it moved.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "EnableKeyRotation"},
		Hints:  map[string]pkgmodel.FieldHint{"EnableKeyRotation": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "k", "EnableKeyRotation": false}`,
		`{"Name": "k", "EnableKeyRotation": true}`,
		`{"Name": "k", "EnableKeyRotation": true}`,
		schema)

	assert.Empty(t, diffs)
}

func TestSuppressedFieldDiffs_NullDesired_TreatedAsOmitted(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "EnableKeyRotation"},
		Hints:  map[string]pkgmodel.FieldHint{"EnableKeyRotation": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "k", "EnableKeyRotation": false}`,
		`{"Name": "k", "EnableKeyRotation": true}`,
		`{"Name": "k", "EnableKeyRotation": null}`,
		schema)

	require.Len(t, diffs, 1)
	assert.Equal(t, "EnableKeyRotation", diffs[0].Path)
}

func TestSuppressedFieldDiffs_EntitySetOmitted_OOBAddReported(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {HasProviderDefault: true, UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "Tags": []}`,
		`{"Name": "r", "Tags": [{"Key": "oob", "Value": "x"}]}`,
		`{"Name": "r"}`,
		schema)

	require.Len(t, diffs, 1)
	assert.Equal(t, "Tags", diffs[0].Path)
	assert.JSONEq(t, `[]`, string(diffs[0].From))
	assert.JSONEq(t, `[{"Key": "oob", "Value": "x"}]`, string(diffs[0].To))
}

func TestSuppressedFieldDiffs_EntitySetPartialDeclaration_OnlySuppressedElementsCompared(t *testing.T) {
	// Desired declares one key; the suppressed content is every element with
	// an undeclared key. A change on a declared-key element is plan
	// territory; a change on an undeclared-key element is suppressed
	// movement and reported with only the suppressed elements as values.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {HasProviderDefault: true, UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}
	desired := `{"Name": "r", "Tags": [{"Key": "mine", "Value": "v1"}]}`

	// Declared element changed, suppressed element unchanged: no diff.
	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "Tags": [{"Key": "mine", "Value": "old"}, {"Key": "aws:sys", "Value": "s"}]}`,
		`{"Name": "r", "Tags": [{"Key": "mine", "Value": "new"}, {"Key": "aws:sys", "Value": "s"}]}`,
		desired, schema)
	assert.Empty(t, diffs)

	// Suppressed element changed: reported, and only suppressed elements
	// appear in the values.
	diffs = suppressedDiffsForTest(t,
		`{"Name": "r", "Tags": [{"Key": "mine", "Value": "v1"}, {"Key": "aws:sys", "Value": "s1"}]}`,
		`{"Name": "r", "Tags": [{"Key": "mine", "Value": "v1"}, {"Key": "aws:sys", "Value": "s2"}]}`,
		desired, schema)
	require.Len(t, diffs, 1)
	assert.Equal(t, "Tags", diffs[0].Path)
	assert.JSONEq(t, `[{"Key": "aws:sys", "Value": "s1"}]`, string(diffs[0].From))
	assert.JSONEq(t, `[{"Key": "aws:sys", "Value": "s2"}]`, string(diffs[0].To))
}

func TestSuppressedFieldDiffs_EntitySetExplicitEmpty_NotSuppressed(t *testing.T) {
	// An explicit empty declaration is a user-initiated clear: the drain is
	// planned by the diff, so nothing here is suppressed content.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {HasProviderDefault: true, UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "Tags": []}`,
		`{"Name": "r", "Tags": [{"Key": "oob", "Value": "x"}]}`,
		`{"Name": "r", "Tags": []}`,
		schema)

	assert.Empty(t, diffs)
}

func TestSuppressedFieldDiffs_EntitySetReorderOnly_NoDiff(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {HasProviderDefault: true, UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "Tags": [{"Key": "a", "Value": "1"}, {"Key": "b", "Value": "2"}]}`,
		`{"Name": "r", "Tags": [{"Key": "b", "Value": "2"}, {"Key": "a", "Value": "1"}]}`,
		`{"Name": "r"}`,
		schema)

	assert.Empty(t, diffs)
}

func TestSuppressedFieldDiffs_SetReorderOnly_NoDiff(t *testing.T) {
	// Without an index field, an omitted provider-default collection compares
	// as an unordered multiset: a provider returning the same members in a
	// different order is not movement.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Groups"},
		Hints:  map[string]pkgmodel.FieldHint{"Groups": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "Groups": ["sg-1", "sg-2"]}`,
		`{"Name": "r", "Groups": ["sg-2", "sg-1"]}`,
		`{"Name": "r"}`,
		schema)

	assert.Empty(t, diffs)
}

func TestSuppressedFieldDiffs_ArrayHint_OrderIsMovement(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Steps"},
		Hints:  map[string]pkgmodel.FieldHint{"Steps": {HasProviderDefault: true, UpdateMethod: pkgmodel.FieldUpdateMethodArray}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "Steps": ["a", "b"]}`,
		`{"Name": "r", "Steps": ["b", "a"]}`,
		`{"Name": "r"}`,
		schema)

	require.Len(t, diffs, 1)
	assert.Equal(t, "Steps", diffs[0].Path)
}

func TestSuppressedFieldDiffs_OpaqueField_PathOnlyNoValues(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "MasterPassword"},
		Hints:  map[string]pkgmodel.FieldHint{"MasterPassword": {HasProviderDefault: true, Opaque: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "MasterPassword": "hash-one"}`,
		`{"Name": "r", "MasterPassword": "hash-two"}`,
		`{"Name": "r"}`,
		schema)

	require.Len(t, diffs, 1)
	assert.Equal(t, "MasterPassword", diffs[0].Path)
	assert.True(t, diffs[0].Opaque)
	assert.Nil(t, diffs[0].From, "opaque movement must carry no values")
	assert.Nil(t, diffs[0].To, "opaque movement must carry no values")
}

func TestSuppressedFieldDiffs_OpaqueEnvelopeValue_PathOnlyNoValues(t *testing.T) {
	// Opacity can also arrive via the stored envelope, not only the schema
	// hint. Either side carrying a $visibility: Opaque marker sanitizes the
	// diff.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Secret"},
		Hints:  map[string]pkgmodel.FieldHint{"Secret": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "Secret": {"$value": "h1", "$visibility": "Opaque"}}`,
		`{"Name": "r", "Secret": {"$value": "h2", "$visibility": "Opaque"}}`,
		`{"Name": "r"}`,
		schema)

	require.Len(t, diffs, 1)
	assert.True(t, diffs[0].Opaque)
	assert.Nil(t, diffs[0].From)
	assert.Nil(t, diffs[0].To)
}

func TestSuppressedFieldDiffs_DottedArrayNestedLeaf_MultisetComparison(t *testing.T) {
	// Array-nested provider-default leaves (the ContainerDefinitions.Cpu
	// shape) compare as a multiset of leaf values: pairing across set-based
	// array elements is unstable, so order and element identity are not
	// movement, but a changed leaf value is.
	schema := pkgmodel.Schema{
		Fields: []string{"Family", "ContainerDefinitions"},
		Hints:  map[string]pkgmodel.FieldHint{"ContainerDefinitions.Cpu": {HasProviderDefault: true}},
	}
	desired := `{"Family": "f", "ContainerDefinitions": [{"Name": "app"}]}`

	// Same leaf values, different element order: no movement.
	diffs := suppressedDiffsForTest(t,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Cpu": 0}, {"Name": "sidecar", "Cpu": 128}]}`,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "sidecar", "Cpu": 128}, {"Name": "app", "Cpu": 0}]}`,
		desired, schema)
	assert.Empty(t, diffs)

	// A leaf value changed: movement.
	diffs = suppressedDiffsForTest(t,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Cpu": 0}]}`,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Cpu": 256}]}`,
		desired, schema)
	require.Len(t, diffs, 1)
	assert.Equal(t, "ContainerDefinitions.Cpu", diffs[0].Path)
}

func TestSuppressedFieldDiffs_FieldAppearsOrDisappears_ReportsMovement(t *testing.T) {
	// A suppressed field present on only one side is movement between
	// absence and a value.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "KmsKeyId"},
		Hints:  map[string]pkgmodel.FieldHint{"KmsKeyId": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r"}`,
		`{"Name": "r", "KmsKeyId": "key-123"}`,
		`{"Name": "r"}`,
		schema)

	require.Len(t, diffs, 1)
	assert.Equal(t, "KmsKeyId", diffs[0].Path)
	assert.Nil(t, diffs[0].From)
	assert.JSONEq(t, `"key-123"`, string(diffs[0].To))
}

func TestSuppressedFieldDiffs_MultipleFields_SortedByPath(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "B", "A"},
		Hints: map[string]pkgmodel.FieldHint{
			"B": {HasProviderDefault: true},
			"A": {HasProviderDefault: true},
		},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "A": 1, "B": 1}`,
		`{"Name": "r", "A": 2, "B": 2}`,
		`{"Name": "r"}`,
		schema)

	require.Len(t, diffs, 2)
	assert.Equal(t, "A", diffs[0].Path)
	assert.Equal(t, "B", diffs[1].Path)
}
