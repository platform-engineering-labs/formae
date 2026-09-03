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

func projectionMappingSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"labels"},
		Hints:  map[string]pkgmodel.FieldHint{"labels": {CoOwned: &pkgmodel.CoOwnership{}}},
	}
}

func projectionSetSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"SecurityGroups"},
		Hints: map[string]pkgmodel.FieldHint{
			"SecurityGroups": {UpdateMethod: pkgmodel.FieldUpdateMethodSet, CoOwned: &pkgmodel.CoOwnership{}},
		},
	}
}

// A co-actor's live members (never declared, never claimed) join the
// declared ones; the desired document a plugin acts on then says the same
// thing the drainable-restricted patch does.
func TestProjectDesiredForWrite_MappingGainsNeverOwnedKeys(t *testing.T) {
	record := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app"}}}

	out, err := ProjectDesiredForWrite(
		json.RawMessage(`{"labels":{"app":"web"}}`),
		json.RawMessage(`{"labels":{"app":"web","team":"platform","opinion":"oob"}}`),
		record, projectionMappingSchema())
	require.NoError(t, err)
	assert.JSONEq(t, `{"labels":{"app":"web","team":"platform","opinion":"oob"}}`, string(out))
}

// A formerly-owned member (claimed on record, no longer declared) stays
// absent: its absence IS the drain, for a desired-state-driven plugin
// exactly as for the patch's restricted remove.
func TestProjectDesiredForWrite_FormerlyOwnedStaysAbsent(t *testing.T) {
	record := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app", "team"}}}

	out, err := ProjectDesiredForWrite(
		json.RawMessage(`{"labels":{"app":"web"}}`),
		json.RawMessage(`{"labels":{"app":"web","team":"absorbed","opinion":"oob"}}`),
		record, projectionMappingSchema())
	require.NoError(t, err)
	assert.JSONEq(t, `{"labels":{"app":"web","opinion":"oob"}}`, string(out))
}

// Explicit empty drains the formerly-owned portion and preserves the rest:
// the projected document carries exactly the co-actor's members.
func TestProjectDesiredForWrite_ExplicitEmptyPreservesNeverOwned(t *testing.T) {
	record := pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"app"}}}

	out, err := ProjectDesiredForWrite(
		json.RawMessage(`{"labels":{}}`),
		json.RawMessage(`{"labels":{"app":"web","kubernetes.io/metadata.name":"ns"}}`),
		record, projectionMappingSchema())
	require.NoError(t, err)
	assert.JSONEq(t, `{"labels":{"kubernetes.io/metadata.name":"ns"}}`, string(out))
}

// An unset field is skipped entirely: whole-field omit tolerance already
// keeps every plugin's hands off it, and inventing the field would turn an
// omitted declaration into an asserted one.
func TestProjectDesiredForWrite_UnsetFieldUntouched(t *testing.T) {
	out, err := ProjectDesiredForWrite(
		json.RawMessage(`{"Name":"n"}`),
		json.RawMessage(`{"Name":"n","labels":{"team":"platform"}}`),
		nil, projectionMappingSchema())
	require.NoError(t, err)
	assert.JSONEq(t, `{"Name":"n"}`, string(out))
}

// Set listings: never-owned live elements are appended after the declared
// ones, in the stored document's own representation.
func TestProjectDesiredForWrite_SetGainsNeverOwnedElements(t *testing.T) {
	record := pkgmodel.OwnedMembers{"SecurityGroups": {Rule: "Set", Members: []string{`"sg-mine"`}}}

	out, err := ProjectDesiredForWrite(
		json.RawMessage(`{"SecurityGroups":["sg-mine"]}`),
		json.RawMessage(`{"SecurityGroups":["sg-mine","sg-oob"]}`),
		record, projectionSetSchema())
	require.NoError(t, err)
	assert.JSONEq(t, `{"SecurityGroups":["sg-mine","sg-oob"]}`, string(out))
}

// A rule-mismatched record is uninterpretable and degrades to no prior:
// live members it named merge in as never-owned - toward tolerance, never
// toward a deletion.
func TestProjectDesiredForWrite_RuleMismatchedRecordDegradesToTolerance(t *testing.T) {
	record := pkgmodel.OwnedMembers{"SecurityGroups": {Rule: "EntitySet/Key", Members: []string{`"sg-old"`}}}

	out, err := ProjectDesiredForWrite(
		json.RawMessage(`{"SecurityGroups":["sg-mine"]}`),
		json.RawMessage(`{"SecurityGroups":["sg-mine","sg-old"]}`),
		record, projectionSetSchema())
	require.NoError(t, err)
	assert.JSONEq(t, `{"SecurityGroups":["sg-mine","sg-old"]}`, string(out))
}

// A declared member echoed in the stored document as a resolved reference
// envelope must not be duplicated: the envelope's identity matches the
// declared literal, so it is not never-owned.
func TestProjectDesiredForWrite_ResolvedEnvelopeMemberNotDuplicated(t *testing.T) {
	out, err := ProjectDesiredForWrite(
		json.RawMessage(`{"SecurityGroups":["sg-mine"]}`),
		json.RawMessage(`{"SecurityGroups":[{"$ref":"formae://resource/x#/GroupId","$value":"sg-mine"}]}`),
		nil, projectionSetSchema())
	require.NoError(t, err)
	assert.JSONEq(t, `{"SecurityGroups":["sg-mine"]}`, string(out))
}

// No co-owned paths, or no never-owned content: the document passes through
// byte-identical.
func TestProjectDesiredForWrite_NoNeverOwnedContentPassesThrough(t *testing.T) {
	in := json.RawMessage(`{"labels":{"app":"web"}}`)
	out, err := ProjectDesiredForWrite(in,
		json.RawMessage(`{"labels":{"app":"web"}}`), nil, projectionMappingSchema())
	require.NoError(t, err)
	assert.Equal(t, string(in), string(out))
}

// A CoOwned hint on an ineligible collection shape (no identity rule, e.g.
// Array) is ignored by the patch layer; the projection must ignore exactly
// the same hints, or the two representations diverge again.
func TestProjectDesiredForWrite_IneligibleCoOwnedHintIgnored(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Ordered"},
		Hints: map[string]pkgmodel.FieldHint{
			"Ordered": {UpdateMethod: pkgmodel.FieldUpdateMethodArray, CoOwned: &pkgmodel.CoOwnership{}},
		},
	}

	in := json.RawMessage(`{"Ordered":["a"]}`)
	out, err := ProjectDesiredForWrite(in,
		json.RawMessage(`{"Ordered":["a","b"]}`), nil, schema)
	require.NoError(t, err)
	assert.Equal(t, string(in), string(out))
}
