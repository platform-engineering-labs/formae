// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/jsonpatch"
	"github.com/stretchr/testify/require"
)

// planForCoOwned is collection_ownership_baseline_test.go's planFor, extended
// with a priorOwned parameter and driven off a full pkgmodel.Schema (so a
// hint's CoOwned/UpdateMethod/IndexField/HasProviderDefault all take effect).
// Like planFor, it drives createPatchDocument directly, not GeneratePatch —
// but it additionally computes the CoOwned partition the same way generatePatch
// does, immediately before the createPatchDocument call, over the same
// (actual, desired) byte pair.
func planForCoOwned(t *testing.T, actual, desired string, schema pkgmodel.Schema, priorOwned pkgmodel.OwnedMembers, strategy jsonpatch.PatchStrategy) []string {
	t.Helper()

	collections := collectionSemanticsFromFieldHints(schema.Hints)
	collections.CoOwned = coOwnedCollections([]byte(actual), []byte(desired), schema.Hints, priorOwned)

	ops, err := createPatchDocument(
		[]byte(actual), []byte(desired),
		schema.Fields,
		nil, // requiredOnUpdate
		schema.HasProviderDefault(),
		entitySetProviderDefaultsFromHints(schema.Hints),
		collections,
		nil, // ignored fields
		strategy,
		nil, // converge fields
		nil, // preserve roots
	)
	require.NoError(t, err)
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.Operation+" "+op.Path)
	}
	return out
}

// A co-owned Set field shrinks to exactly what this forma is relinquishing: a
// member present on a prior apply but no longer declared is drained, while a
// member neither declared now nor previously (another writer's) is tolerated
// even though it too is absent from desired.
func TestCoOwnedSetShrinkDrainsExactlyTheRemovedMember(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"members"},
		Hints: map[string]pkgmodel.FieldHint{
			"members": {UpdateMethod: pkgmodel.FieldUpdateMethodSet, CoOwned: &pkgmodel.CoOwnership{}},
		},
	}
	priorOwned := pkgmodel.OwnedMembers{
		"members": {Rule: "Set", Members: []string{"\"a\"", "\"b\""}},
	}

	ops := planForCoOwned(t,
		`{"members":["a","b","c"]}`,
		`{"members":["a"]}`,
		schema, priorOwned, jsonpatch.PatchStrategyExactMatch)

	require.Equal(t, []string{"remove /members/1"}, ops)
}

// A live member this forma never declared, on this apply or any prior one, is
// tolerated whole — the co-owned annotation restricts drains to formerly-owned
// members, it never authorizes removing a co-actor's content.
func TestCoOwnedSetNeverOwnedMemberIsTolerated(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"members"},
		Hints: map[string]pkgmodel.FieldHint{
			"members": {UpdateMethod: pkgmodel.FieldUpdateMethodSet, CoOwned: &pkgmodel.CoOwnership{}},
		},
	}

	ops := planForCoOwned(t,
		`{"members":["a","x"]}`,
		`{"members":["a"]}`,
		schema, nil, jsonpatch.PatchStrategyExactMatch)

	require.Empty(t, ops)
}

// Declaring a co-owned Mapping explicitly empty drains exactly the members
// this forma previously declared, leaving a co-actor's key untouched — the
// gap TestExplicitlyEmptiedMapPlansNothing (collection_ownership_baseline_test.go)
// documents for an unannotated Mapping.
func TestCoOwnedMappingExplicitEmptyDrainsPriorOwnedOnly(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"labels"},
		Hints: map[string]pkgmodel.FieldHint{
			"labels": {CoOwned: &pkgmodel.CoOwnership{}},
		},
	}
	priorOwned := pkgmodel.OwnedMembers{
		"labels": {Rule: "Mapping", Members: []string{"mine"}},
	}

	ops := planForCoOwned(t,
		`{"labels":{"mine":"1","theirs":"2"}}`,
		`{"labels":{}}`,
		schema, priorOwned, jsonpatch.PatchStrategyExactMatch)

	require.Equal(t, []string{"remove /labels/mine"}, ops)
}

// A stored ownership record computed under a rule the field no longer matches
// (its UpdateMethod changed since the record was written) is stale and must
// not be trusted: the record is discarded rather than degrading to a
// best-effort comparison, so a shrink after such a change drains nothing.
func TestCoOwnedRuleMismatchDegradesToNoDrain(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"members"},
		Hints: map[string]pkgmodel.FieldHint{
			"members": {UpdateMethod: pkgmodel.FieldUpdateMethodSet, CoOwned: &pkgmodel.CoOwnership{}},
		},
	}
	priorOwned := pkgmodel.OwnedMembers{
		// Stale: this field is a Set now, but the record was computed under an
		// EntitySet rule (e.g. before a schema change).
		"members": {Rule: "EntitySet/Name", Members: []string{"\"a\"", "\"b\""}},
	}

	ops := planForCoOwned(t,
		`{"members":["a","b"]}`,
		`{"members":["a"]}`,
		schema, priorOwned, jsonpatch.PatchStrategyExactMatch)

	require.Empty(t, ops)
}

// A co-owned EntitySet field the forma omits entirely is tolerated whole,
// exactly like the unannotated case in
// TestOmittedAnnotatedFieldIsToleratedWhole (collection_ownership_baseline_test.go).
func TestCoOwnedEntitySetOmittedFieldIsToleratedWhole(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"attrs"},
		Hints: map[string]pkgmodel.FieldHint{
			"attrs": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key", CoOwned: &pkgmodel.CoOwnership{}},
		},
	}

	ops := planForCoOwned(t,
		`{"attrs":[{"Key":"theirs","Value":"1"}]}`,
		`{}`,
		schema, nil, jsonpatch.PatchStrategyExactMatch)

	require.Empty(t, ops)
}

// A co-owned EntitySet still applies the user's own element updates and skips
// the provider-default pre-strip: op paths index the UNFILTERED document, so
// a co-actor's untouched element (which the pre-strip would otherwise have
// removed before the diff ran) does not shift the declared element's index.
func TestCoOwnedEntitySetKeepsDeclaredUpdatesUnfiltered(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"attrs"},
		Hints: map[string]pkgmodel.FieldHint{
			"attrs": {
				UpdateMethod:       pkgmodel.FieldUpdateMethodEntitySet,
				IndexField:         "Key",
				HasProviderDefault: true,
				CoOwned:            &pkgmodel.CoOwnership{},
			},
		},
	}

	ops := planForCoOwned(t,
		`{"attrs":[{"Key":"theirs","Value":"1"},{"Key":"mine","Value":"old"}]}`,
		`{"attrs":[{"Key":"mine","Value":"new"}]}`,
		schema, nil, jsonpatch.PatchStrategyExactMatch)

	require.Equal(t, []string{"replace /attrs/1/Value"}, ops)
}

// A co-owned Mapping nested under another field — the shape the Kubernetes
// metadata.labels case uses — should drain the same way a top-level one does:
// the explicit-empty drain should land at the nested path, removing only the
// member this forma previously declared and leaving a co-actor's key
// untouched.
//
// Currently blocked: jsonpatch itself resolves the nested co-owned path
// correctly (verified directly against jsonpatch.CreatePatch, independent of
// this package), but createPatchDocument's own empty-collection normalization
// (StripNestedEmptyCollectionsExcept) strips a NESTED empty collection from
// its parent before the diff ever runs — {"metadata":{"labels":{}}} becomes
// {"metadata":{}} — because that pass's preserve-exemption
// (PreserveEmptyRootFields) only covers top-level, non-dotted hint names. The
// explicit-empty signal a nested co-owned Mapping needs is erased before
// coOwnedCollections' whole-field-tolerance-vs-member-drain distinction ever
// gets to see it, so the field reads as omitted and is tolerated whole
// instead of being drained.
func TestCoOwnedNestedMappingExplicitEmptyDrainsPriorOwnedOnly(t *testing.T) {
	t.Skip("blocked: nested empty-collection normalization erases a nested co-owned Mapping's explicit-empty state before the diff runs; see comment above")

	schema := pkgmodel.Schema{
		Fields: []string{"metadata"},
		Hints: map[string]pkgmodel.FieldHint{
			"metadata.labels": {CoOwned: &pkgmodel.CoOwnership{}},
		},
	}
	priorOwned := pkgmodel.OwnedMembers{
		"metadata.labels": {Rule: "Mapping", Members: []string{"mine"}},
	}

	ops := planForCoOwned(t,
		`{"metadata":{"labels":{"mine":"1","theirs":"2"}}}`,
		`{"metadata":{"labels":{}}}`,
		schema, priorOwned, jsonpatch.PatchStrategyExactMatch)

	require.Equal(t, []string{"remove /metadata/labels/mine"}, ops)
}
