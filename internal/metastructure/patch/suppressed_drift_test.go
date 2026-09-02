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

// suppressedDiffsForTest uses the old properties as the write witness: the
// common fixture shape where formae's last write observed the same state the
// drift window starts from. No prior ownership record.
func suppressedDiffsForTest(t *testing.T, oldProps, newProps, desired string, schema pkgmodel.Schema) []SuppressedFieldDiff {
	t.Helper()
	return suppressedDiffsWithWitness(t, oldProps, newProps, desired, oldProps, schema)
}

func suppressedDiffsWithWitness(t *testing.T, oldProps, newProps, desired, witness string, schema pkgmodel.Schema) []SuppressedFieldDiff {
	t.Helper()
	return suppressedDiffsWithWitnessAndRecord(t, oldProps, newProps, desired, witness, nil, schema)
}

// suppressedDiffsForCoOwned drives the CoOwned regime, which never consults
// the witness: an empty witness stands in for "irrelevant here".
func suppressedDiffsForCoOwned(t *testing.T, oldProps, newProps, desired string, priorOwned pkgmodel.OwnedMembers, schema pkgmodel.Schema) []SuppressedFieldDiff {
	t.Helper()
	return suppressedDiffsWithWitnessAndRecord(t, oldProps, newProps, desired, "", priorOwned, schema)
}

func suppressedDiffsWithWitnessAndRecord(t *testing.T, oldProps, newProps, desired, witness string, priorOwned pkgmodel.OwnedMembers, schema pkgmodel.Schema) []SuppressedFieldDiff {
	t.Helper()
	var w json.RawMessage
	if witness != "" {
		w = json.RawMessage(witness)
	}
	diffs, err := SuppressedFieldDiffs(json.RawMessage(oldProps), json.RawMessage(newProps), json.RawMessage(desired), w, priorOwned, schema)
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

func TestSuppressedFieldDiffs_EntitySetOmitted_EmptyBaseline_NotReported(t *testing.T) {
	// An omitted collection that was empty at the baseline has no witnessed
	// content: entries appearing on it (a co-actor registering, a first
	// out-of-band add) are initialization from the note's point of view and
	// stay in the drift list only.
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

	assert.Empty(t, diffs)
}

func TestSuppressedFieldDiffs_EntitySetOmitted_WitnessedEntryChanged_Reported(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {HasProviderDefault: true, UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "Tags": [{"Key": "sys", "Value": "v1"}]}`,
		`{"Name": "r", "Tags": [{"Key": "sys", "Value": "v2"}]}`,
		`{"Name": "r"}`,
		schema)

	require.Len(t, diffs, 1)
	assert.Equal(t, "Tags", diffs[0].Path)
	assert.JSONEq(t, `[{"Key": "sys", "Value": "v1"}]`, string(diffs[0].From))
	assert.JSONEq(t, `[{"Key": "sys", "Value": "v2"}]`, string(diffs[0].To))
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

func TestSuppressedFieldDiffs_FieldAppears_NotReported(t *testing.T) {
	// A suppressed field absent at the baseline that now holds a value is
	// the cloud (or a co-actor) populating it for the first time:
	// initialization, not movement. It stays in the drift list only.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "KmsKeyId"},
		Hints:  map[string]pkgmodel.FieldHint{"KmsKeyId": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r"}`,
		`{"Name": "r", "KmsKeyId": "key-123"}`,
		`{"Name": "r"}`,
		schema)

	assert.Empty(t, diffs, "first-time population is not movement of a witnessed value")
}

func TestSuppressedFieldDiffs_WitnessedFieldDisappears_Reported(t *testing.T) {
	// A witnessed value that vanished out of band is movement worth seeing
	// (a deleted encryption config is a regression, not initialization).
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "KmsKeyId"},
		Hints:  map[string]pkgmodel.FieldHint{"KmsKeyId": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "KmsKeyId": "key-123"}`,
		`{"Name": "r"}`,
		`{"Name": "r"}`,
		schema)

	require.Len(t, diffs, 1)
	assert.Equal(t, "KmsKeyId", diffs[0].Path)
	assert.JSONEq(t, `"key-123"`, string(diffs[0].From))
	assert.Nil(t, diffs[0].To)
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

func TestSuppressedFieldDiffs_DottedLeafWithOpaqueEnvelope_PathOnlyNoValues(t *testing.T) {
	// An opaque envelope nested inside array elements must sanitize the
	// diff even though the schema hint carries no Opaque flag: the envelope
	// marker is the authority.
	schema := pkgmodel.Schema{
		Fields: []string{"Family", "ContainerDefinitions"},
		Hints:  map[string]pkgmodel.FieldHint{"ContainerDefinitions.Token": {HasProviderDefault: true}},
	}
	desired := `{"Family": "f", "ContainerDefinitions": [{"Name": "app"}]}`

	diffs := suppressedDiffsForTest(t,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Token": {"$value": "hash-one", "$visibility": "Opaque"}}]}`,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Token": {"$value": "hash-two", "$visibility": "Opaque"}}]}`,
		desired, schema)

	require.Len(t, diffs, 1)
	assert.True(t, diffs[0].Opaque)
	assert.Nil(t, diffs[0].From, "envelope-opaque leaf values must never appear in a note")
	assert.Nil(t, diffs[0].To)
}

func TestSuppressedFieldDiffs_ArrayNestedLeaf_DeclaredElsewhere_StillCompared(t *testing.T) {
	// The strip removes an array-nested provider-default leaf from every
	// element on both sides regardless of desired declaring it somewhere,
	// so the classifier must not skip the path just because one desired
	// element declares the leaf: another element's suppressed movement is
	// still invisible to the plan and must be noted.
	schema := pkgmodel.Schema{
		Fields: []string{"Family", "ContainerDefinitions"},
		Hints:  map[string]pkgmodel.FieldHint{"ContainerDefinitions.Cpu": {HasProviderDefault: true}},
	}
	desired := `{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Cpu": 512}, {"Name": "sidecar"}]}`

	diffs := suppressedDiffsForTest(t,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Cpu": 512}, {"Name": "sidecar", "Cpu": 0}]}`,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Cpu": 512}, {"Name": "sidecar", "Cpu": 256}]}`,
		desired, schema)

	require.Len(t, diffs, 1)
	assert.Equal(t, "ContainerDefinitions.Cpu", diffs[0].Path)
}

func TestSuppressedFieldDiffs_PureObjectDottedPath_DeclaredInDesired_NotReported(t *testing.T) {
	// A dotted path that never traverses an array keeps the conditional
	// strip semantics: declared in desired means plan territory, no note.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Config"},
		Hints:  map[string]pkgmodel.FieldHint{"Config.Encryption": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "Config": {"Encryption": "aes"}}`,
		`{"Name": "r", "Config": {"Encryption": "kms"}}`,
		`{"Name": "r", "Config": {"Encryption": "kms"}}`,
		schema)

	assert.Empty(t, diffs)
}

func TestSuppressedFieldDiffs_SetEmptyBaseline_AppearanceNotReported(t *testing.T) {
	// Same witnessed rule for unkeyed collections: members appearing on an
	// empty baseline (runtime registration) are not movement.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Targets"},
		Hints:  map[string]pkgmodel.FieldHint{"Targets": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsForTest(t,
		`{"Name": "tg", "Targets": []}`,
		`{"Name": "tg", "Targets": [{"Id": "10.0.0.5", "Port": 80}]}`,
		`{"Name": "tg"}`,
		schema)

	assert.Empty(t, diffs)
}

func TestSuppressedFieldDiffs_EntitySetPartial_SystemEntryAppears_NotReported(t *testing.T) {
	// Partial declaration with no suppressed entries at the baseline: a
	// system entry appearing beside the declared keys is initialization.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {HasProviderDefault: true, UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}
	desired := `{"Name": "r", "Tags": [{"Key": "mine", "Value": "v"}]}`

	diffs := suppressedDiffsForTest(t,
		`{"Name": "r", "Tags": [{"Key": "mine", "Value": "v"}]}`,
		`{"Name": "r", "Tags": [{"Key": "mine", "Value": "v"}, {"Key": "aws:sys", "Value": "s"}]}`,
		desired, schema)

	assert.Empty(t, diffs)
}

func TestSuppressedFieldDiffs_DottedLeaves_EmptyBaseline_NotReported(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Family", "ContainerDefinitions"},
		Hints:  map[string]pkgmodel.FieldHint{"ContainerDefinitions.Cpu": {HasProviderDefault: true}},
	}
	desired := `{"Family": "f", "ContainerDefinitions": [{"Name": "app"}]}`

	diffs := suppressedDiffsForTest(t,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app"}]}`,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Cpu": 256}]}`,
		desired, schema)

	assert.Empty(t, diffs, "leaves appearing where none were witnessed are initialization")
}

func TestSuppressedFieldDiffs_DottedLeaves_NullBaseline_NotReported(t *testing.T) {
	// A leaf that existed only as null (or an empty collection) at the
	// baseline witnessed nothing; a value arriving there is initialization.
	schema := pkgmodel.Schema{
		Fields: []string{"Family", "ContainerDefinitions"},
		Hints:  map[string]pkgmodel.FieldHint{"ContainerDefinitions.Cpu": {HasProviderDefault: true}},
	}
	desired := `{"Family": "f", "ContainerDefinitions": [{"Name": "app"}]}`

	diffs := suppressedDiffsForTest(t,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Cpu": null}]}`,
		`{"Family": "f", "ContainerDefinitions": [{"Name": "app", "Cpu": 256}]}`,
		desired, schema)

	assert.Empty(t, diffs)
}

func TestSuppressedFieldDiffs_RuntimeChurn_NeverInWriteEcho_NotReported(t *testing.T) {
	// The write echo never contained the field: everything that happened to
	// it since arrived through sync (a co-actor registering and re-registering
	// members). Steady-state churn between two sync-absorbed states is the
	// infrastructure's business, not movement of anything formae witnessed.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Targets"},
		Hints:  map[string]pkgmodel.FieldHint{"Targets": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsWithWitness(t,
		`{"Name": "tg", "Targets": [{"Id": "10.0.0.5", "Port": 80}]}`,
		`{"Name": "tg", "Targets": [{"Id": "10.0.0.9", "Port": 80}]}`,
		`{"Name": "tg"}`,
		`{"Name": "tg", "Targets": []}`,
		schema)

	assert.Empty(t, diffs, "a field absent or empty in the write echo is never note-worthy, even between two populated states")
}

func TestSuppressedFieldDiffs_WitnessedInWriteEcho_LaterMovement_Reported(t *testing.T) {
	// The write echo held a value; the window's movement is between two
	// later states. The witness gates, the window provides from/to.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Rotation"},
		Hints:  map[string]pkgmodel.FieldHint{"Rotation": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsWithWitness(t,
		`{"Name": "k", "Rotation": "on"}`,
		`{"Name": "k", "Rotation": "weekly"}`,
		`{"Name": "k"}`,
		`{"Name": "k", "Rotation": "off"}`,
		schema)

	require.Len(t, diffs, 1)
	assert.JSONEq(t, `"on"`, string(diffs[0].From))
	assert.JSONEq(t, `"weekly"`, string(diffs[0].To))
}

func TestSuppressedFieldDiffs_NilWitness_NothingReported(t *testing.T) {
	// No write-origin version exists (formae never wrote the resource, e.g.
	// freshly imported): nothing is witnessed, nothing is reported.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "EnableKeyRotation"},
		Hints:  map[string]pkgmodel.FieldHint{"EnableKeyRotation": {HasProviderDefault: true}},
	}

	diffs := suppressedDiffsWithWitness(t,
		`{"Name": "k", "EnableKeyRotation": false}`,
		`{"Name": "k", "EnableKeyRotation": true}`,
		`{"Name": "k"}`,
		"",
		schema)

	assert.Empty(t, diffs)
}

func assertWitnessedForTest(t *testing.T, desired, witness string, schema pkgmodel.Schema) string {
	t.Helper()
	out, err := AssertWitnessedSuppressed(json.RawMessage(desired), json.RawMessage(witness), schema)
	require.NoError(t, err)
	return string(out)
}

func TestAssertWitnessedSuppressed_OmittedScalar_AssertsWitnessValue(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Rotation"},
		Hints:  map[string]pkgmodel.FieldHint{"Rotation": {HasProviderDefault: true}},
	}
	out := assertWitnessedForTest(t,
		`{"Name": "k"}`,
		`{"Name": "k", "Rotation": "off"}`,
		schema)
	assert.JSONEq(t, `{"Name": "k", "Rotation": "off"}`, out)
}

func TestAssertWitnessedSuppressed_DeclaredField_Untouched(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Rotation"},
		Hints:  map[string]pkgmodel.FieldHint{"Rotation": {HasProviderDefault: true}},
	}
	out := assertWitnessedForTest(t,
		`{"Name": "k", "Rotation": "weekly"}`,
		`{"Name": "k", "Rotation": "off"}`,
		schema)
	assert.JSONEq(t, `{"Name": "k", "Rotation": "weekly"}`, out, "a declared value is intent; the witness never overrides it")
}

func TestAssertWitnessedSuppressed_UnwitnessedField_NotInjected(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Targets"},
		Hints:  map[string]pkgmodel.FieldHint{"Targets": {HasProviderDefault: true}},
	}
	out := assertWitnessedForTest(t,
		`{"Name": "tg"}`,
		`{"Name": "tg", "Targets": []}`,
		schema)
	assert.JSONEq(t, `{"Name": "tg"}`, out, "an empty witness asserts nothing")
}

func TestAssertWitnessedSuppressed_OpaqueAndCreateOnlyAndDotted_Skipped(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Secret", "BucketName", "Containers"},
		Hints: map[string]pkgmodel.FieldHint{
			"Secret":         {HasProviderDefault: true, Opaque: true},
			"BucketName":     {HasProviderDefault: true, CreateOnly: true},
			"Containers.Cpu": {HasProviderDefault: true},
		},
	}
	out := assertWitnessedForTest(t,
		`{"Name": "r", "Containers": [{"Name": "app"}]}`,
		`{"Name": "r", "Secret": "hash", "BucketName": "gen-123", "Containers": [{"Name": "app", "Cpu": 256}]}`,
		schema)
	assert.JSONEq(t, `{"Name": "r", "Containers": [{"Name": "app"}]}`, out,
		"opaque values cannot be asserted from hashes, createOnly assertion would plan replacements, dotted paths have no stable injection")
}

func TestAssertWitnessedSuppressed_EntitySetPartial_MergesWitnessSystemEntries(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {HasProviderDefault: true, UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}
	out := assertWitnessedForTest(t,
		`{"Name": "r", "Tags": [{"Key": "mine", "Value": "v"}]}`,
		`{"Name": "r", "Tags": [{"Key": "mine", "Value": "old"}, {"Key": "sys", "Value": "s0"}]}`,
		schema)
	assert.JSONEq(t, `{"Name": "r", "Tags": [{"Key": "mine", "Value": "v"}, {"Key": "sys", "Value": "s0"}]}`, out,
		"declared entries keep their declared values; witnessed undeclared entries are asserted alongside")
}

func TestAssertWitnessedSuppressed_EntitySetExplicitEmpty_NotInjected(t *testing.T) {
	// An explicit empty declaration is a drain; asserting witness entries
	// would undo the user's clear.
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {HasProviderDefault: true, UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}
	out := assertWitnessedForTest(t,
		`{"Name": "r", "Tags": []}`,
		`{"Name": "r", "Tags": [{"Key": "sys", "Value": "s0"}]}`,
		schema)
	assert.JSONEq(t, `{"Name": "r", "Tags": []}`, out)
}

// coOwnedMappingSchema is a co-owned Mapping field (CoOwned + no
// UpdateMethod, per pkgmodel.IdentityRule) with no HasProviderDefault hint —
// the co-owned regime must not depend on it.
func coOwnedMappingSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"labels"},
		Hints: map[string]pkgmodel.FieldHint{
			"labels": {CoOwned: &pkgmodel.CoOwnership{}},
		},
	}
}

func TestSuppressedFieldDiffs_CoOwnedMapping_NeverOwnedMemberChanges_Reported(t *testing.T) {
	// "theirs" is live but neither declared by this forma now nor on the
	// prior apply (the record only ever claimed "mine"): it is NeverOwned,
	// so its value change is suppressed movement.
	priorOwned := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"mine"}}}

	diffs := suppressedDiffsForCoOwned(t,
		`{"labels": {"mine": "1", "theirs": "a"}}`,
		`{"labels": {"mine": "1", "theirs": "b"}}`,
		`{"labels": {"mine": "1"}}`,
		priorOwned, coOwnedMappingSchema())

	require.Len(t, diffs, 1)
	assert.Equal(t, "labels", diffs[0].Path)
	assert.False(t, diffs[0].Opaque)
	assert.JSONEq(t, `{"theirs": "a"}`, string(diffs[0].From))
	assert.JSONEq(t, `{"theirs": "b"}`, string(diffs[0].To))
}

func TestSuppressedFieldDiffs_CoOwnedMapping_DeclaredMemberDisappears_NoNoteForThatMember(t *testing.T) {
	// "mine" is still declared by this forma, so it is never NeverOwned no
	// matter how it moves: its disappearance is real drift for the plan to
	// surface, not a suppressed note. "theirs" is unchanged, so nothing at
	// all is reported.
	priorOwned := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"mine"}}}

	diffs := suppressedDiffsForCoOwned(t,
		`{"labels": {"mine": "1", "theirs": "a"}}`,
		`{"labels": {"theirs": "a"}}`,
		`{"labels": {"mine": "1"}}`,
		priorOwned, coOwnedMappingSchema())

	assert.Empty(t, diffs, "a declared member's own movement is plan territory, not a suppressed note")
}

func TestSuppressedFieldDiffs_CoOwnedMapping_RuleMismatchedRecord_TreatsUndeclaredAsNeverOwned(t *testing.T) {
	// The stored record's Rule ("Set") does not match this field's current
	// IdentityRule ("Mapping" — CoOwned with no UpdateMethod): it is stale
	// and discarded, so "mine" — no longer declared, and no longer protected
	// as formerly-owned — is NeverOwned like any other undeclared member, and
	// its movement is reported instead of silently tolerated.
	priorOwned := pkgmodel.OwnedMembers{"labels": {Rule: "Set", Members: []string{"mine"}}}

	diffs := suppressedDiffsForCoOwned(t,
		`{"labels": {"mine": "1"}}`,
		`{"labels": {"mine": "2"}}`,
		`{"labels": {}}`,
		priorOwned, coOwnedMappingSchema())

	require.Len(t, diffs, 1)
	assert.Equal(t, "labels", diffs[0].Path)
	assert.JSONEq(t, `{"mine": "1"}`, string(diffs[0].From))
	assert.JSONEq(t, `{"mine": "2"}`, string(diffs[0].To))
}

func TestSuppressedFieldDiffs_CoOwnedPathWithoutHasProviderDefault_EntersCandidateSet(t *testing.T) {
	// coOwnedMappingSchema's "labels" hint carries no HasProviderDefault, so
	// schema.HasProviderDefault() alone would never enumerate it. Path
	// enumeration is the union with every CoOwned-hinted path, so this must
	// still be classified.
	diffs := suppressedDiffsForCoOwned(t,
		`{"labels": {"theirs": "a"}}`,
		`{"labels": {"theirs": "b"}}`,
		`{}`,
		nil, coOwnedMappingSchema())

	require.Len(t, diffs, 1, "a CoOwned path without hasProviderDefault must enter the candidate set")
	assert.Equal(t, "labels", diffs[0].Path)
}
