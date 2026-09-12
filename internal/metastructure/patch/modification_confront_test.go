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

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// confrontMappingSchema: a co-owned Mapping label field plus a plain field.
func confrontMappingSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"Name", "labels"},
		Hints:  map[string]pkgmodel.FieldHint{"labels": {CoOwned: &pkgmodel.CoOwnership{}}},
	}
}

// The helper retains the witness argument for compatibility with the
// surrounding fixtures. Classification uses the baseline and declaration;
// established default values stay protected.
func confront(t *testing.T, old, new, desired, witness string, prior pkgmodel.OwnedMembers, schema pkgmodel.Schema) bool {
	t.Helper()
	got, err := ModificationConfrontable(json.RawMessage(old), json.RawMessage(new), json.RawMessage(desired), json.RawMessage(witness), prior, schema)
	require.NoError(t, err)
	return got
}

// The bug: a co-actor's never-owned member appears out of band. Nothing the
// user manages moved, so a reconcile need not confront.
func TestModificationConfrontable_NeverOwnedMemberAdded_Tolerated(t *testing.T) {
	prior := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app"}}}
	got := confront(t,
		`{"Name":"n","labels":{"app":"web"}}`,
		`{"Name":"n","labels":{"app":"web","team":"platform"}}`,
		`{"Name":"n","labels":{"app":"web"}}`,
		"", prior, confrontMappingSchema())
	assert.False(t, got, "a never-owned member's appearance is tolerated")
}

func TestModificationConfrontable_NeverOwnedMemberValueChanged_Tolerated(t *testing.T) {
	prior := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app"}}}
	got := confront(t,
		`{"Name":"n","labels":{"app":"web","team":"a"}}`,
		`{"Name":"n","labels":{"app":"web","team":"b"}}`,
		`{"Name":"n","labels":{"app":"web"}}`,
		"", prior, confrontMappingSchema())
	assert.False(t, got, "a never-owned member's value change is tolerated")
}

// A member the user declares is theirs; its out-of-band change is real drift.
func TestModificationConfrontable_DeclaredMemberChanged_Confront(t *testing.T) {
	prior := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app"}}}
	got := confront(t,
		`{"Name":"n","labels":{"app":"web"}}`,
		`{"Name":"n","labels":{"app":"hacked"}}`,
		`{"Name":"n","labels":{"app":"web"}}`,
		"", prior, confrontMappingSchema())
	assert.True(t, got, "a declared member's out-of-band change must confront")
}

// A member on the record but no longer declared (being drained): its
// out-of-band movement still confronts.
func TestModificationConfrontable_FormerlyOwnedMemberChanged_Confront(t *testing.T) {
	prior := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app", "team"}}}
	got := confront(t,
		`{"Name":"n","labels":{"app":"web","team":"a"}}`,
		`{"Name":"n","labels":{"app":"web","team":"b"}}`,
		`{"Name":"n","labels":{"app":"web"}}`,
		"", prior, confrontMappingSchema())
	assert.True(t, got, "a formerly-owned member's out-of-band change must confront")
}

// A plain (non-co-owned) field moving out of band is always confrontable.
func TestModificationConfrontable_PlainFieldChanged_Confront(t *testing.T) {
	prior := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app"}}}
	got := confront(t,
		`{"Name":"n","labels":{"app":"web"}}`,
		`{"Name":"changed","labels":{"app":"web","team":"x"}}`,
		`{"Name":"n","labels":{"app":"web"}}`,
		"", prior, confrontMappingSchema())
	assert.True(t, got, "a plain field's out-of-band change must confront even beside tolerated movement")
}

// A witnessed provider-default move confronts; the witness makes it real.
func TestModificationConfrontable_WitnessedProviderDefault_Confront(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "EnableKeyRotation"},
		Hints:  map[string]pkgmodel.FieldHint{"EnableKeyRotation": {HasProviderDefault: true}},
	}
	got := confront(t,
		`{"Name":"n","EnableKeyRotation":false}`,
		`{"Name":"n","EnableKeyRotation":true}`,
		`{"Name":"n"}`,
		`{"Name":"n","EnableKeyRotation":false}`, // witness holds a real value
		nil, schema)
	assert.True(t, got, "a witnessed provider-default move must confront")
}

// First-time provider population is tolerated even when an unrelated edit
// makes this resource enter the confrontation predicate.
func TestModificationConfrontable_InitialProviderDefault_Tolerated(t *testing.T) {
	schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"EnableKeyRotation": {HasProviderDefault: true}}}
	got := confront(t, `{"Name":"n"}`, `{"Name":"n","EnableKeyRotation":true}`, `{"Name":"n"}`, "", nil, schema)
	assert.False(t, got, "initial provider defaults do not require a drift decision")
}

func TestModificationConfrontable_NoChange_Tolerated(t *testing.T) {
	prior := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app"}}}
	got := confront(t,
		`{"Name":"n","labels":{"app":"web","team":"x"}}`,
		`{"Name":"n","labels":{"app":"web","team":"x"}}`,
		`{"Name":"n","labels":{"app":"web"}}`,
		"", prior, confrontMappingSchema())
	assert.False(t, got, "no movement at all is not confrontable")
}

// The version=2 rig repro in miniature: a never-owned member is live and the
// modification is purely that co-actor's content, while the user's edit lives
// in the plan (not in this modification). Must be tolerated.
func TestModificationConfrontable_CoActorDriftBesidePendingEdit_Tolerated(t *testing.T) {
	prior := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app", "owner"}}}
	got := confront(t,
		`{"Name":"n","labels":{"app":"probe","owner":"formae"}}`,
		`{"Name":"n","labels":{"app":"probe","opinion":"oob","owner":"formae","team":"platform"}}`,
		`{"Name":"n","labels":{"app":"probe","owner":"formae","version":"2"}}`,
		"", prior, confrontMappingSchema())
	assert.False(t, got, "co-actor drift beside a declared edit is tolerated; the edit is not in the modification")
}

// A provider-default field the user DECLARES is owned by ordinary planning,
// not suppressed. Its out-of-band change is real drift and must confront —
// the remainder must not neutralize a declared provider-default field.
func TestModificationConfrontable_DeclaredProviderDefaultChanged_Confront(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "EnableKeyRotation"},
		Hints:  map[string]pkgmodel.FieldHint{"EnableKeyRotation": {HasProviderDefault: true}},
	}
	got := confront(t,
		`{"Name":"n","EnableKeyRotation":true}`,
		`{"Name":"n","EnableKeyRotation":false}`,
		`{"Name":"n","EnableKeyRotation":true}`, // declared
		"", nil, schema)
	assert.True(t, got, "a declared provider-default field's out-of-band change must confront")
}

// A nested co-owned path whose only content is never-owned must stay
// tolerated even when neutralizing it leaves empty ancestor objects that
// differ structurally between the two sides.
func TestModificationConfrontable_NestedCoOwnedEmptyAncestors_Tolerated(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"meta"},
		Hints:  map[string]pkgmodel.FieldHint{"meta.labels": {CoOwned: &pkgmodel.CoOwnership{}}},
	}
	got := confront(t,
		`{"meta":{"labels":{"team":"x"}}}`,
		`{}`,
		`{}`,
		"", nil, schema)
	assert.False(t, got, "a nested co-owned path of only never-owned members is tolerated despite empty-ancestor mismatch")
}

// A plain managed field toggling between a null/empty value and absent is
// real drift; neutralizing co-owned content must not prune managed nulls or
// empty containers elsewhere in the document.
func TestModificationConfrontable_ManagedNullTogglesConfront(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"meta", "config"},
		Hints:  map[string]pkgmodel.FieldHint{"meta.labels": {CoOwned: &pkgmodel.CoOwnership{}}},
	}
	// meta.labels carries only a never-owned member (tolerated), but the plain
	// `config` field flips from null to absent — that must still confront.
	got := confront(t,
		`{"meta":{"labels":{"team":"x"}},"config":null}`,
		`{"meta":{"labels":{"team":"x"}}}`,
		`{}`,
		"", nil, schema)
	assert.True(t, got, "a managed field flipping between null and absent is real drift")
}

func TestModificationConfrontable_ManagedEmptyObjectTogglesConfront(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"meta", "config"},
		Hints:  map[string]pkgmodel.FieldHint{"meta.labels": {CoOwned: &pkgmodel.CoOwnership{}}},
	}
	got := confront(t,
		`{"meta":{"labels":{"team":"x"}},"config":{}}`,
		`{"meta":{"labels":{"team":"x"}}}`,
		`{}`,
		"", nil, schema)
	assert.True(t, got, "a managed field flipping between an empty object and absent is real drift")
}

// The mirror of the empty-ancestor case: the OLD side carries the nested
// co-owned content and the NEW side already holds an explicitly empty
// ancestor. Both represent only never-owned movement and must be tolerated.
func TestModificationConfrontable_NestedCoOwnedEmptyAncestors_MirrorTolerated(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"meta"},
		Hints:  map[string]pkgmodel.FieldHint{"meta.labels": {CoOwned: &pkgmodel.CoOwnership{}}},
	}
	got := confront(t,
		`{"meta":{"labels":{"team":"x"}}}`,
		`{"meta":{}}`,
		`{}`,
		"", nil, schema)
	assert.False(t, got, "tolerated nested movement is symmetric whether the empty ancestor is old or new")
}

// A co-owned field carrying an unexpected scalar value cannot be classified
// into members; its out-of-band change must confront rather than be deleted
// from both remainders and read as equal.
func TestModificationConfrontable_CoOwnedScalarChange_Confront(t *testing.T) {
	got := confront(t,
		`{"Name":"n","labels":"a"}`,
		`{"Name":"n","labels":"b"}`,
		`{"Name":"n","labels":{}}`,
		"", nil, confrontMappingSchema())
	assert.True(t, got, "a malformed scalar co-owned value's change must confront")
}

// An EntitySet whose elements lack the configured identity field cannot be
// classified; its out-of-band change must confront.
func TestModificationConfrontable_CoOwnedEntitySetNoIdentity_Confront(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Targets"},
		Hints: map[string]pkgmodel.FieldHint{
			"Targets": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Id", CoOwned: &pkgmodel.CoOwnership{}},
		},
	}
	got := confront(t,
		`{"Targets":[{"Port":80}]}`,
		`{"Targets":[{"Port":81}]}`,
		`{"Targets":[]}`,
		"", nil, schema)
	assert.True(t, got, "an identity-less EntitySet element's change must confront")
}

// Large integers beyond float64's exact range must not collapse to equal
// during canonicalization, or a plain-field drift would be silently tolerated.
func TestModificationConfrontable_LargeIntegerPrecision_Confront(t *testing.T) {
	prior := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app"}}}
	got := confront(t,
		`{"Name":"n","serial":9007199254740992,"labels":{"app":"web"}}`,
		`{"Name":"n","serial":9007199254740993,"labels":{"app":"web","team":"x"}}`,
		`{"Name":"n","labels":{"app":"web"}}`,
		"", prior, confrontMappingSchema())
	assert.True(t, got, "distinct large integers must confront, not collapse to equal")
}

// A set-shaped co-owned field carrying an object value is malformed: object
// keys are identities only for a Mapping. Its change must confront, not be
// neutralized as though the keys were members.
func TestModificationConfrontable_SetShapedFieldWithObjectValue_Confront(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Groups"},
		Hints: map[string]pkgmodel.FieldHint{
			"Groups": {UpdateMethod: pkgmodel.FieldUpdateMethodSet, CoOwned: &pkgmodel.CoOwnership{}},
		},
	}
	got := confront(t,
		`{"Groups":{"a":"1"}}`,
		`{"Groups":{"b":"2"}}`,
		`{"Groups":[]}`,
		"", nil, schema)
	assert.True(t, got, "an object value on a set-shaped co-owned field is malformed and must confront")
}

// A Mapping-shaped co-owned field carrying an array value is likewise
// malformed and must confront.
func TestModificationConfrontable_MappingShapedFieldWithArrayValue_Confront(t *testing.T) {
	got := confront(t,
		`{"Name":"n","labels":["a"]}`,
		`{"Name":"n","labels":["b"]}`,
		`{"Name":"n","labels":{}}`,
		"", nil, confrontMappingSchema())
	assert.True(t, got, "an array value on a Mapping-shaped co-owned field is malformed and must confront")
}

// A declared co-owned member whose value is a large integer beyond float64
// precision must confront when it drifts; restriction must not reconstruct it
// through a lossy float64.
func TestModificationConfrontable_LargeIntMemberValue_Confront(t *testing.T) {
	prior := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"serial"}}}
	got := confront(t,
		`{"labels":{"serial":9007199254740992}}`,
		`{"labels":{"serial":9007199254740993}}`,
		`{"labels":{"serial":9007199254740992}}`,
		"", prior, confrontMappingSchema())
	assert.True(t, got, "a declared member's large-integer drift must confront, not collapse via float64")
}

func TestModificationConfrontable_InitialDefaultsPreserveExplicitIntent(t *testing.T) {
	for _, tc := range []struct {
		name, old, observed, desired string
		drift                        bool
	}{
		{"null-default", `{"enabled":null}`, `{"enabled":false}`, `{}`, false},
		{"explicit-false", `{}`, `{"enabled":true}`, `{"enabled":false}`, true},
		{"explicit-empty", `{}`, `{"enabled":["provider"]}`, `{"enabled":[]}`, true},
		{"existing-value", `{"enabled":false}`, `{"enabled":true}`, `{}`, true},
		{"nested-first-population", `{}`, `{"settings":{"enabled":false}}`, `{}`, false},
		{"nested-empty-parent", `{"settings":{}}`, `{"settings":{"enabled":false}}`, `{}`, false},
		{"number-preservation", `{"number":9007199254740993}`, `{"number":9007199254740992,"enabled":false}`, `{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "enabled"
			if tc.name == "nested-first-population" || tc.name == "nested-empty-parent" {
				path = "settings.enabled"
			}
			schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{path: {HasProviderDefault: true}}}
			assert.Equal(t, tc.drift, confront(t, tc.old, tc.observed, tc.desired, "", nil, schema))
		})
	}
}

func TestModificationConfrontable_PatchWrittenDefaultAfterBaseline_Confront(t *testing.T) {
	schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"enabled": {HasProviderDefault: true}}}
	assert.True(t, confront(t, `{}`, `{"enabled":false}`, `{}`, `{"enabled":true}`, nil, schema),
		"a value written after the reconcile baseline is not initial provider population")
}

func TestModificationConfrontable_InitialDefaultShapes(t *testing.T) {
	for _, tc := range []struct{ name, path, old, observed, desired string }{
		{"empty-list", "targets", `{"targets":[]}`, `{"targets":["provider"]}`, `{}`},
		{"empty-map", "options", `{"options":{}}`, `{"options":{"provider":true}}`, `{}`},
		{"deep-parent", "a.b.enabled", `{}`, `{"a":{"b":{"enabled":false}}}`, `{}`},
		{"array-leaf", "items.enabled", `{"items":[{"name":"a"}]}`, `{"items":[{"name":"a","enabled":false}]}`, `{"items":[{"name":"a"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{tc.path: {HasProviderDefault: true}}}
			assert.False(t, confront(t, tc.old, tc.observed, tc.desired, "", nil, schema))
		})
	}
}

func TestModificationConfrontable_DefaultDoesNotHideAncestorTypeChange(t *testing.T) {
	schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"settings.enabled": {HasProviderDefault: true}}}
	assert.True(t, confront(t, `{"settings":"explicit"}`, `{"settings":{"enabled":false}}`, `{}`, "", nil, schema))
}
