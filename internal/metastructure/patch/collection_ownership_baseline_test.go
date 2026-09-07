// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"testing"

	"github.com/platform-engineering-labs/jsonpatch"
	"github.com/stretchr/testify/require"
)

// What the planner does with a collection whose live content the forma does not
// declare — a co-actor's map key, an appended list member, a map the user
// cleared. These tests state the current behavior rather than a desired one, so
// that a change to any of it is a deliberate decision with a failing test
// attached instead of a silent shift.
//
// The load-bearing fact is a property of the diff rather than of the schema:
// jsonpatch removes surplus ARRAY members but never surplus OBJECT keys. So a
// controller's label survives a reconcile whether or not the field is
// annotated, while a member a co-actor appends to a plain list is removed
// whether or not it is annotated.
//
// That asymmetry is the point of this file. Element-level tolerance is usually
// described as missing for both maps and lists and present only for EntitySet.
// For the planner, maps are already tolerant and plain lists are not, and the
// annotation is what makes an OMITTED field tolerated, not a partially declared
// one.
//
// Reconcile mode is PatchStrategyExactMatch and patch mode is
// PatchStrategyEnsureExists; where a case behaves the same under both, the test
// says so rather than covering only the reconcile path.

// planFor runs the patch pipeline and returns its operations as "op path"
// strings. providerDefaults names the paths annotated hasProviderDefault.
func planFor(t *testing.T, actual, desired string, schemaFields, providerDefaults []string, strategy jsonpatch.PatchStrategy) []string {
	t.Helper()
	ops, err := createPatchDocument(
		[]byte(actual), []byte(desired),
		schemaFields,
		nil, // requiredOnUpdate
		providerDefaults,
		nil, // entitySet provider defaults
		jsonpatch.Collections{},
		nil, // ignored fields
		strategy,
		nil, // converge fields
		nil, // preserve roots
		nil, // preserve paths
	)
	require.NoError(t, err)
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.Operation+" "+op.Path)
	}
	return out
}

var bothStrategies = []struct {
	name     string
	strategy jsonpatch.PatchStrategy
}{
	{"reconcile", jsonpatch.PatchStrategyExactMatch},
	{"patch", jsonpatch.PatchStrategyEnsureExists},
}

// A map key the forma does not declare is left alone, and the annotation makes
// no difference: the tolerance comes from the diff, not from the schema. This
// is why a Kubernetes controller's label survives a reconcile today.
func TestSurplusMapKeyIsNeverPlannedForRemoval(t *testing.T) {
	cases := []struct {
		name             string
		actual, desired  string
		schemaFields     []string
		providerDefaults []string
	}{
		{
			name:         "top-level, annotated",
			actual:       `{"tags":{"Name":"mine","aws:cloudformation:stack-id":"arn:x"}}`,
			desired:      `{"tags":{"Name":"mine"}}`,
			schemaFields: []string{"tags"}, providerDefaults: []string{"tags"},
		},
		{
			name:         "top-level, not annotated",
			actual:       `{"tags":{"Name":"mine","surplus":"x"}}`,
			desired:      `{"tags":{"Name":"mine"}}`,
			schemaFields: []string{"tags"},
		},
		{
			name:         "nested, annotated",
			actual:       `{"metadata":{"labels":{"app":"probe","injected-by":"coactor"}}}`,
			desired:      `{"metadata":{"labels":{"app":"probe"}}}`,
			schemaFields: []string{"metadata"}, providerDefaults: []string{"metadata.labels"},
		},
		{
			name:         "nested, not annotated",
			actual:       `{"metadata":{"labels":{"app":"probe","injected-by":"coactor"}}}`,
			desired:      `{"metadata":{"labels":{"app":"probe"}}}`,
			schemaFields: []string{"metadata"},
		},
	}

	for _, tc := range cases {
		for _, s := range bothStrategies {
			t.Run(tc.name+"/"+s.name, func(t *testing.T) {
				require.Empty(t, planFor(t, tc.actual, tc.desired, tc.schemaFields, tc.providerDefaults, s.strategy))
			})
		}
	}
}

// Tolerance covers keys the forma never declared. A declared key whose value
// moved out of band is the user's own content changing, and still diffs.
func TestDeclaredMapKeyStillDiffsWhenItsValueMoves(t *testing.T) {
	for _, s := range bothStrategies {
		t.Run(s.name, func(t *testing.T) {
			require.Equal(t,
				[]string{"replace /tags/Name"},
				planFor(t,
					`{"tags":{"Name":"hijacked","injected":"coactor"}}`,
					`{"tags":{"Name":"mine"}}`,
					[]string{"tags"}, []string{"tags"}, s.strategy))
		})
	}
}

// A declared key the cloud is missing is still added, so tolerance does not
// swallow a genuine change.
func TestDeclaredMapKeyIsStillAddedWhenMissing(t *testing.T) {
	for _, s := range bothStrategies {
		t.Run(s.name, func(t *testing.T) {
			require.Equal(t,
				[]string{"add /tags/Name"},
				planFor(t,
					`{"tags":{"injected":"coactor"}}`,
					`{"tags":{"Name":"mine"}}`,
					[]string{"tags"}, []string{"tags"}, s.strategy))
		})
	}
}

// Clearing a map by declaring it empty plans nothing, so the live keys stay.
// This is a gap rather than a decision: EntitySet grew an explicit-empty drain
// and Mapping never did, so a user who empties a map to clear it gets silence.
// Draining it correctly needs a record of which keys formae itself wrote, which
// the engine does not keep.
func TestExplicitlyEmptiedMapPlansNothing(t *testing.T) {
	for _, s := range bothStrategies {
		t.Run(s.name, func(t *testing.T) {
			require.Empty(t, planFor(t,
				`{"tags":{"Name":"mine","injected":"coactor"}}`,
				`{"tags":{}}`,
				[]string{"tags"}, []string{"tags"}, s.strategy))
		})
	}
}

// A surplus member of a plain list IS planned for removal under reconcile, with
// or without the annotation. This is the shape that produces a fight with a
// co-actor that keeps re-appending: formae removes the member, the co-actor
// writes it back. Removing it by index is also why the member cannot simply be
// filtered out before the diff — the remaining operations address positions in
// the unfiltered document.
func TestSurplusListMemberIsPlannedForRemovalUnderReconcile(t *testing.T) {
	for _, providerDefaults := range [][]string{{"members"}, nil} {
		name := "annotated"
		if providerDefaults == nil {
			name = "not annotated"
		}
		t.Run(name, func(t *testing.T) {
			require.Equal(t,
				[]string{"remove /members/1"},
				planFor(t,
					`{"members":["declared","appended-by-coactor"]}`,
					`{"members":["declared"]}`,
					[]string{"members"}, providerDefaults,
					jsonpatch.PatchStrategyExactMatch))
		})
	}
}

// Patch mode never removes, so the same list is left alone there. Reconcile is
// the only mode where the removal above happens.
func TestSurplusListMemberSurvivesPatchMode(t *testing.T) {
	require.Empty(t, planFor(t,
		`{"members":["declared","appended-by-coactor"]}`,
		`{"members":["declared"]}`,
		[]string{"members"}, []string{"members"},
		jsonpatch.PatchStrategyEnsureExists))
}

// An annotated field the forma omits entirely is tolerated whole by the
// field-level strip, for both shapes. Omitting a field means the cloud owns it.
func TestOmittedAnnotatedFieldIsToleratedWhole(t *testing.T) {
	cases := []struct {
		name            string
		actual, desired string
	}{
		{"map", `{"tags":{"injected":"coactor"}}`, `{}`},
		{"list", `{"members":["appended-by-coactor"]}`, `{}`},
	}
	for _, tc := range cases {
		for _, s := range bothStrategies {
			t.Run(tc.name+"/"+s.name, func(t *testing.T) {
				require.Empty(t, planFor(t, tc.actual, tc.desired,
					[]string{"tags", "members"},
					[]string{"tags", "members"}, s.strategy))
			})
		}
	}
}

// The tolerance above is not the annotation doing work: an object key the
// desired document lacks is never removed at any level, annotated or not, whole
// field or single key. Only array members are removed. That single fact
// explains every map row in this file, and it is the reason the annotation
// currently changes nothing for a Mapping — the strip removes the field from
// both sides, and the diff would have ignored it either way.
func TestObjectKeysAreNeverRemovedWithOrWithoutTheAnnotation(t *testing.T) {
	cases := []struct {
		name             string
		actual, desired  string
		providerDefaults []string
	}{
		{"whole field, annotated", `{"tags":{"injected":"coactor"}}`, `{}`, []string{"tags"}},
		{"whole field, not annotated", `{"tags":{"injected":"coactor"}}`, `{}`, nil},
		{"single key, annotated", `{"tags":{"a":"1","b":"2"}}`, `{"tags":{"a":"1"}}`, []string{"tags"}},
		{"single key, not annotated", `{"tags":{"a":"1","b":"2"}}`, `{"tags":{"a":"1"}}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Empty(t, planFor(t, tc.actual, tc.desired,
				[]string{"tags"}, tc.providerDefaults,
				jsonpatch.PatchStrategyExactMatch))
		})
	}
}
