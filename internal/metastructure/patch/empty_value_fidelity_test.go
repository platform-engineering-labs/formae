// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package patch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
)

func TestFirstPointerSegment(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"/Spec", "Spec", true},
		{"/Spec/x", "Spec", true},
		{"/Spec/0/y", "Spec", true},
		{"/Specification", "Specification", true},
		{"/a~1b/c", "a/b", true},
		{"/a~0b", "a~b", true},
		{"/a~01", "a~1", true},
		{"", "", false},
		{"Spec", "", false},
		{"/", "", false},
	}
	for _, c := range cases {
		got, ok := firstPointerSegment(c.path)
		assert.Equal(t, c.ok, ok, "path %q ok", c.path)
		if c.ok {
			assert.Equal(t, c.want, got, "path %q", c.path)
		}
	}
}

func TestIsKeepableReferenceEnvelope(t *testing.T) {
	keep := []string{
		`{"$ref":"formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/SecretString"}`,
		`{"$ref":"formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/S","$value":"x","$visibility":"Opaque"}`,
		`{"$res":true,"$label":"a","$type":"T::B","$stack":"s","$property":"P"}`,
		`{"$res":true,"$label":"a","$type":"T::B","$stack":"s","$property":"P","$json":"host"}`,
	}
	drop := []string{
		`{"$res":false,"$label":"a","$type":"T::B","$stack":"s","$property":"P"}`,
		`{"$res":true,"$label":"a","$type":"T::B","$stack":"s"}`,
		`{"$res":true,"$label":"","$type":"T::B","$stack":"s","$property":"P"}`,
		`{"$ref":42}`,
		`{"$ref":"formae://#/S"}`,
		`{"$ref":"not-a-uri"}`,
		`{"$ref":"formae://#/S","$res":true,"$label":"a","$type":"T","$stack":"s","$property":"P"}`,
		`"plain"`,
		`{}`,
		`[]`,
	}
	for _, s := range keep {
		assert.True(t, isKeepableReferenceEnvelope(gjson.Parse(s)), "must keep %s", s)
	}
	for _, s := range drop {
		assert.False(t, isKeepableReferenceEnvelope(gjson.Parse(s)), "must drop %s", s)
	}
}

func TestReferenceEnvelopeFields(t *testing.T) {
	desired := []byte(`{
		"Token": {"$ref": "formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/S"},
		"Bad": {"$res": true},
		"Plain": "",
		"Extra": {"$ref": "formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/T"}
	}`)
	fields := referenceEnvelopeFields(desired, pkgmodel.Schema{Fields: []string{"Token", "Bad", "Plain"}})
	assert.Equal(t, map[string]bool{"Token": true}, fields,
		"only well-formed envelopes on schema fields enter the keep-set")
	assert.Empty(t, referenceEnvelopeFields(nil, pkgmodel.Schema{Fields: []string{"Token"}}))
	assert.Empty(t, referenceEnvelopeFields([]byte(`not json`), pkgmodel.Schema{Fields: []string{"Token"}}))
	assert.Empty(t, referenceEnvelopeFields(desired, pkgmodel.Schema{
		Fields: []string{"Token"},
		Hints:  map[string]pkgmodel.FieldHint{"Token": {CreateOnly: true}},
	}), "createOnly destinations never enter the keep-set")
}

func TestPreserveEmptyRootFields(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Spec", "Other", "Nested"},
		Hints: map[string]pkgmodel.FieldHint{
			"Spec":       {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic, PreserveEmptyValues: true},
			"Other":      {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic},
			"Nested.sub": {PreserveEmptyValues: true},
			"Bare":       {PreserveEmptyValues: true},
		},
	}
	assert.Equal(t, map[string]bool{"Spec": true, "Bare": true}, PreserveEmptyRootFields(schema),
		"only preserveEmptyValues hints on non-dotted keys enter the root set")
	assert.Empty(t, PreserveEmptyRootFields(pkgmodel.Schema{}))
}

func fidelitySchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Spec", "Other"},
		Hints: map[string]pkgmodel.FieldHint{
			"Spec": {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic, PreserveEmptyValues: true},
		},
	}
}

// The headline shape: a hinted field's empty-object member survives to the
// single whole-value replace op. Requires both the diff-input exemption and
// the op-value exemption; red until both land.
func TestGeneratePatch_PreserveEmpty_ReplaceCarriesVerbatimValue(t *testing.T) {
	document := json.RawMessage(`{"Name":"x","Spec":{"acme":{"server":"https://old"}}}`)
	desired := json.RawMessage(`{"Name":"x","Spec":{"selfSigned":{}}}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, fidelitySchema(), nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)

	var ops []struct {
		Op    string          `json:"op"`
		Path  string          `json:"path"`
		Value json.RawMessage `json:"value"`
	}
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	require.Len(t, ops, 1)
	assert.Equal(t, "replace", ops[0].Op)
	assert.Equal(t, "/Spec", ops[0].Path)
	assert.JSONEq(t, `{"selfSigned":{}}`, string(ops[0].Value),
		"the empty-object member is the declaration and must survive")
}

// Symmetry: identical values incl. empties on both sides plan nothing.
func TestGeneratePatch_PreserveEmpty_IdenticalSidesPlanNothing(t *testing.T) {
	doc := json.RawMessage(`{"Name":"x","Spec":{"selfSigned":{}}}`)

	patchDoc, _, _, err := GeneratePatch(doc, doc, doc, doc, resolver.ResolvableProperties{}, fidelitySchema(), nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, patchDoc)
}

// The default is pinned, not just the exemption: a field without the hint
// keeps today's stripping even when another field carries it.
func TestGeneratePatch_NonHintedFieldStillStripped(t *testing.T) {
	document := json.RawMessage(`{"Name":"x","Other":{"acme":{"server":"https://old"}}}`)
	desired := json.RawMessage(`{"Name":"x","Other":{"selfSigned":{}}}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, fidelitySchema(), nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.NotContains(t, string(patchDoc), "selfSigned",
		"unhinted fields keep the rendering-noise strip")
}

func refSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Token", "Other"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
}

// A first-declared unresolvable reference survives as a placeholder add op;
// a plain empty string is still dropped as rendering noise.
func TestGeneratePatch_ReferenceEnvelopeAddSurvives(t *testing.T) {
	document := json.RawMessage(`{"Name":"x"}`)
	desired := json.RawMessage(`{"Name":"x","Token":{"$ref":"formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/S"}}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, refSchema(), nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"op":"add","path":"/Token","value":""}]`, string(patchDoc))

	plain := json.RawMessage(`{"Name":"x","Token":""}`)
	patchDoc, _, _, err = GeneratePatch(document, plain, document, plain, resolver.ResolvableProperties{}, refSchema(), nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, patchDoc, "a plain empty string is still rendering noise")
}

// A malformed envelope contributes nothing to the keep-set: current behavior
// (silent drop) is preserved rather than minting a placeholder.
func TestGeneratePatch_MalformedEnvelopeNotKept(t *testing.T) {
	document := json.RawMessage(`{"Name":"x"}`)
	desired := json.RawMessage(`{"Name":"x","Token":{"$res":true}}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, refSchema(), nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.NotContains(t, string(patchDoc), `"value":""`,
		"the keep-set must never mint an empty-string placeholder for a malformed envelope")
	assert.JSONEq(t, `[{"op":"add","path":"/Token","value":{"$res":true}}]`, string(patchDoc),
		"a malformed envelope keeps its pre-existing plain-map diff behavior, unchanged by the keep-set")
}

// Keeping a field only permits diffing: equal values still produce no op.
func TestGeneratePatch_KeptFieldEqualValuesNoOp(t *testing.T) {
	document := json.RawMessage(`{"Name":"x","Token":"v"}`)
	desired := json.RawMessage(`{"Name":"x","Token":{"$ref":"formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/S","$value":"v"}}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, refSchema(), nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, patchDoc)
}

// GREEN-FIRST GUARD: a preserveEmptyValues root omitted from desired while
// the document holds a bare empty stays invisible (the absence-scoped
// provider-echo tolerance). Fails exactly if an implementation wrongly
// exempts preserved roots from that tolerance.
func TestGeneratePatch_PreservedRootAbsentDesired_NoRemove(t *testing.T) {
	document := json.RawMessage(`{"Name":"x","Spec":{}}`)
	desired := json.RawMessage(`{"Name":"x"}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, fidelitySchema(), nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.NotContains(t, string(patchDoc), "remove")
}

// The reference placeholder add survives under Patch mode too.
func TestGeneratePatch_ReferenceAddSurvivesPatchMode(t *testing.T) {
	document := json.RawMessage(`{"Name":"x"}`)
	desired := json.RawMessage(`{"Name":"x","Token":{"$ref":"formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/S"}}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, refSchema(), nil, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"op":"add","path":"/Token","value":""}]`, string(patchDoc))
}

// A reference on a createOnly destination never mints a placeholder op: a
// kept placeholder there could only surface as a replacement, and a
// replacement is never planned from a value that has not resolved.
func TestGeneratePatch_ReferenceOnCreateOnlyDestination_NoPlaceholder(t *testing.T) {
	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Token"},
		Hints:      map[string]pkgmodel.FieldHint{"Token": {CreateOnly: true}},
	}
	document := json.RawMessage(`{"Name":"x"}`)
	desired := json.RawMessage(`{"Name":"x","Token":{"$ref":"formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/S"}}`)

	patchDoc, createOnly, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, schema, nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, createOnly, "no replacement may be planned from an unresolved placeholder")
	assert.NotContains(t, string(patchDoc), "/Token")
}

// The accepted churn contract: an actual-side empty-shaped extra inside a
// preserved field makes the whole value differ, producing exactly one
// replace carrying the desired value verbatim.
func TestGeneratePatch_PreserveEmpty_ActualExtraEmptyChurnsAsWholeReplace(t *testing.T) {
	document := json.RawMessage(`{"Name":"x","Spec":{"selfSigned":{},"defaulted":{}}}`)
	desired := json.RawMessage(`{"Name":"x","Spec":{"selfSigned":{}}}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, fidelitySchema(), nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"op":"replace","path":"/Spec","value":{"selfSigned":{}}}]`, string(patchDoc))
}

// The no-change guarantee for existing plugins: Atomic WITHOUT
// preserveEmptyValues keeps exactly today's behavior - nested empties are
// stripped on both sides and equalize the diff.
func TestGeneratePatch_AtomicWithoutPreserve_KeepsStripBehavior(t *testing.T) {
	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Doc"},
		Hints:      map[string]pkgmodel.FieldHint{"Doc": {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic}},
	}
	document := json.RawMessage(`{"Name":"x","Doc":{"stmt":{"cond":{}}}}`)
	desired := json.RawMessage(`{"Name":"x","Doc":{"stmt":{}}}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, schema, nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, patchDoc,
		"symmetric stripping still equalizes empty-shaped differences for atomic-only fields")
}

// A dotted (nested subresource) hint key grants no fidelity anywhere.
func TestGeneratePatch_DottedPreserveHint_NoFidelity(t *testing.T) {
	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Config"},
		Hints:      map[string]pkgmodel.FieldHint{"Config.records": {PreserveEmptyValues: true}},
	}
	document := json.RawMessage(`{"Name":"x","Config":{"records":{"a":{}}}}`)
	desired := json.RawMessage(`{"Name":"x","Config":{"records":{}}}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, schema, nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, patchDoc, "nested hints are out of fidelity scope; today's stripping applies")
}

// The whole-resource change gate must see an empty-only difference inside a
// preserveEmptyValues root: stored {} vs desired {selfSigned:{}} IS a change
// there, while the same shape on an unhinted field keeps today's tolerance.
// (The document-side tolerance exists for provider echoes; inside a preserved
// root, empties are values.)
func TestGeneratePatch_PreservedRoot_EmptyOnlyDifferenceMintsReplace(t *testing.T) {
	document := json.RawMessage(`{"Name":"x","Spec":{}}`)
	desired := json.RawMessage(`{"Name":"x","Spec":{"selfSigned":{}}}`)

	patchDoc, _, _, err := GeneratePatch(document, desired, document, desired, resolver.ResolvableProperties{}, fidelitySchema(), nil, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"op":"replace","path":"/Spec","value":{"selfSigned":{}}}]`, string(patchDoc))
}
