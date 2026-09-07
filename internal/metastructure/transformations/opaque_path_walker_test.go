// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package transformations

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// redactAll is a total match callback standing in for the real hash/sentinel
// callbacks: it replaces whatever it is handed, so a test can assert purely on
// WHICH nodes the walk selected.
func redactAll(any) (any, bool) { return "REDACTED", true }

// walkJSON decodes props, walks it with the given opaque set, and returns the
// re-encoded tree plus the walk's diagnostics.
func walkJSON(t *testing.T, props string, opaque ...string) (string, []Diagnostic) {
	t.Helper()

	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(props), &m))

	set := make(map[string]bool, len(opaque))
	for _, f := range opaque {
		set[f] = true
	}

	w := &OpaqueWalk{Opaque: set, Match: redactAll}
	w.WalkProperties(m)

	out, err := json.Marshal(m)
	require.NoError(t, err)
	return string(out), w.Diagnostics()
}

func TestOpaqueWalk_MatchesNestedProperty(t *testing.T) {
	out, diags := walkJSON(t,
		`{"settings":{"password":"hunter2","host":"example.com"},"name":"cp"}`,
		"settings.password")

	assert.JSONEq(t, `{"settings":{"password":"REDACTED","host":"example.com"},"name":"cp"}`, out)
	assert.Empty(t, diags)
}

// A hint with k dots has 2^(k-1) possible readings of its key boundaries. Plain
// prefix concatenation matches every reading that is actually present in the
// payload, without enumerating any of them — that is what makes the rule
// fail-safe for confidentiality.
func TestOpaqueWalk_MatchesEverySegmentationPresentInThePayload(t *testing.T) {
	tests := map[string]struct {
		props string
		want  string
	}{
		"fully nested":      {`{"a":{"b":{"c":"s"}}}`, `{"a":{"b":{"c":"REDACTED"}}}`},
		"dotted leaf":       {`{"a":{"b.c":"s"}}`, `{"a":{"b.c":"REDACTED"}}`},
		"dotted parent":     {`{"a.b":{"c":"s"}}`, `{"a.b":{"c":"REDACTED"}}`},
		"fully flat":        {`{"a.b.c":"s"}`, `{"a.b.c":"REDACTED"}`},
		"unrelated sibling": {`{"a":{"b":{"d":"s"}}}`, `{"a":{"b":{"d":"s"}}}`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			out, _ := walkJSON(t, tc.props, "a.b.c")
			assert.JSONEq(t, tc.want, out)
		})
	}
}

// A flat key that genuinely contains a dot must match as itself. gjson would
// read it as a nested path and miss it; this is the shape a Grafana
// provisioning response returns.
func TestOpaqueWalk_MatchesLiteralDotContainingKey(t *testing.T) {
	out, diags := walkJSON(t,
		`{"hmacConfig.secret":"s","url":"https://example.com"}`,
		"hmacConfig.secret")

	assert.JSONEq(t, `{"hmacConfig.secret":"REDACTED","url":"https://example.com"}`, out)
	assert.Empty(t, diags)
}

// Matching is never gated on the parent also being a declared hint: a plugin
// whose schema declares only the nested name is exactly the incomplete-schema
// case, and reading the hint as a literal key there would leave a real password
// in cleartext.
func TestOpaqueWalk_MatchesNestedHintWithNoDeclaredParent(t *testing.T) {
	out, _ := walkJSON(t, `{"settings":{"password":"hunter2"}}`, "settings.password")
	assert.JSONEq(t, `{"settings":{"password":"REDACTED"}}`, out)
}

// pkl emits index-free hint names for a Listing<SubResource>, so array
// elements are descended with the SAME prefix as the field itself.
func TestOpaqueWalk_DescendsArrayElementsWithoutIndexSegment(t *testing.T) {
	out, diags := walkJSON(t,
		`{"webhooks":[{"password":"a","url":"u1"},{"password":"b","url":"u2"}]}`,
		"webhooks.password")

	assert.JSONEq(t, `{"webhooks":[{"password":"REDACTED","url":"u1"},{"password":"REDACTED","url":"u2"}]}`, out)
	assert.Empty(t, diags, "one segmentation matched at several concrete paths is not ambiguous")
}

func TestOpaqueWalk_DescendsNestedArrays(t *testing.T) {
	out, _ := walkJSON(t, `{"webhooks":[[{"password":"a"}]]}`, "webhooks.password")
	assert.JSONEq(t, `{"webhooks":[[{"password":"REDACTED"}]]}`, out)
}

func TestOpaqueWalk_SkipsNonContainerArrayElements(t *testing.T) {
	out, _ := walkJSON(t, `{"webhooks":["a",null,7,{"password":"p"}]}`, "webhooks.password")
	assert.JSONEq(t, `{"webhooks":["a",null,7,{"password":"REDACTED"}]}`, out)
}

// Provider payloads are legitimately heterogeneous; a ragged array must not
// fail the walk at the persist boundary.
func TestOpaqueWalk_ToleratesRaggedArrays(t *testing.T) {
	out, _ := walkJSON(t,
		`{"webhooks":[{"password":"a"},{"other":"b"},[{"password":"c"}]]}`,
		"webhooks.password")
	assert.JSONEq(t, `{"webhooks":[{"password":"REDACTED"},{"other":"b"},[{"password":"REDACTED"}]]}`, out)
}

// A numeric OBJECT key is a key, not an index: the walk distinguishes them
// structurally because it inspects the actual value.
func TestOpaqueWalk_NumericObjectKeyIsNotAnIndex(t *testing.T) {
	out, _ := walkJSON(t, `{"accounts":{"0":{"password":"p"}}}`, "accounts.0.password")
	assert.JSONEq(t, `{"accounts":{"0":{"password":"REDACTED"}}}`, out)

	out, _ = walkJSON(t, `{"accounts":{"0":{"password":"p"}}}`, "accounts.password")
	assert.JSONEq(t, `{"accounts":{"0":{"password":"p"}}}`, out,
		"an object key must not be elided the way an array index is")
}

// An exact name match hands the whole value to the callback and stops: hashing
// only part of a map-shaped secret would leave its sibling keys in cleartext.
func TestOpaqueWalk_ExactMatchStopsDescent(t *testing.T) {
	out, _ := walkJSON(t,
		`{"decodedData":{"user":"admin","password":"p"}}`,
		"decodedData", "decodedData.password")

	assert.JSONEq(t, `{"decodedData":"REDACTED"}`, out,
		"the parent hint wins and the whole map is replaced as one value")
}

func TestOpaqueWalk_DescendantMatchLeavesNonSecretSiblings(t *testing.T) {
	out, _ := walkJSON(t,
		`{"settings":{"user":"admin","password":"p"}}`,
		"settings.password")
	assert.JSONEq(t, `{"settings":{"user":"admin","password":"REDACTED"}}`, out)
}

// Today's top-level behaviour is unchanged: a bare hint name selects only the
// top-level key, never the same key at depth.
func TestOpaqueWalk_TopLevelHintDoesNotMatchAtDepth(t *testing.T) {
	out, _ := walkJSON(t, `{"settings":{"password":"p"},"password":"top"}`, "password")
	assert.JSONEq(t, `{"settings":{"password":"p"},"password":"REDACTED"}`, out)
}

// A match callback may decline to change the value (an already-hashed
// envelope). Descent still stops — the value is the declared secret either way.
func TestOpaqueWalk_UnchangedMatchStillStopsDescent(t *testing.T) {
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"settings":{"password":{"nested":"p"}}}`), &m))

	w := &OpaqueWalk{
		Opaque: map[string]bool{"settings.password": true, "settings.password.nested": true},
		Match: func(v any) (any, bool) {
			if _, isMap := v.(map[string]any); isMap {
				return v, false // decline, as an already-hashed envelope would
			}
			return "REDACTED", true
		},
	}
	w.WalkProperties(m)

	out, err := json.Marshal(m)
	require.NoError(t, err)
	assert.JSONEq(t, `{"settings":{"password":{"nested":"p"}}}`, string(out))
}

// OnMiss is the transformer's inline-envelope branch. It runs only AFTER the
// name match misses, so a raw map-shaped secret that happens to carry a $value
// key is never mistaken for an envelope.
func TestOpaqueWalk_NameMatchIsTestedBeforeOnMiss(t *testing.T) {
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"settings":{"password":{"$value":"p","$visibility":"Opaque"}}}`), &m))

	// A realistic OnMiss claims only genuine envelopes — which the nested
	// password IS. The name match must still win it.
	claimEnvelopes := func(v any) (any, bool) {
		m, isMap := v.(map[string]any)
		if !isMap {
			return nil, false
		}
		if vis, _ := m["$visibility"].(string); vis == "Opaque" {
			return "ENVELOPE", true
		}
		return nil, false
	}

	w := &OpaqueWalk{
		Opaque: map[string]bool{"settings.password": true},
		Match:  redactAll,
		OnMiss: claimEnvelopes,
	}
	w.WalkProperties(m)

	out, err := json.Marshal(m)
	require.NoError(t, err)
	assert.JSONEq(t, `{"settings":{"password":"REDACTED"}}`, string(out),
		"the name match wins, so the envelope branch never sees the value")
}

func TestOpaqueWalk_OnMissStopsDescent(t *testing.T) {
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"settings":{"password":"p"}}`), &m))

	w := &OpaqueWalk{
		Opaque: map[string]bool{"settings.password": true},
		Match:  redactAll,
		OnMiss: func(v any) (any, bool) {
			if _, isMap := v.(map[string]any); isMap {
				return "ENVELOPE", true
			}
			return nil, false
		},
	}
	w.WalkProperties(m)

	out, err := json.Marshal(m)
	require.NoError(t, err)
	assert.JSONEq(t, `{"settings":"ENVELOPE"}`, string(out),
		"a value OnMiss claims is an envelope is not descended into")
}

// Ambiguity is two distinct SEGMENTATIONS of one hint, not two concrete paths.
func TestOpaqueWalk_ReportsTwoDistinctSegmentationsOnce(t *testing.T) {
	out, diags := walkJSON(t, `{"a":{"b":{"c":"s1"}},"a.b":{"c":"s2"}}`, "a.b.c")

	assert.JSONEq(t, `{"a":{"b":{"c":"REDACTED"}},"a.b":{"c":"REDACTED"}}`, out)
	require.Len(t, diags, 1)
	assert.Equal(t, DiagnosticWarn, diags[0].Severity)
	assert.Equal(t, "a.b.c", diags[0].Hint)
	assert.Contains(t, diags[0].Detail, "2 distinct")
}

func TestOpaqueWalk_ListOfSubResourcesIsNotAmbiguous(t *testing.T) {
	_, diags := walkJSON(t,
		`{"webhooks":[{"password":"a"},{"password":"b"},{"password":"c"}]}`,
		"webhooks.password")
	assert.Empty(t, diags)
}

// Several candidate prefixes (the patch-document case) resolving to one
// concrete node must mutate it once and manufacture no ambiguity.
func TestOpaqueWalk_MultipleCandidatePrefixesMutateANodeOnce(t *testing.T) {
	var v any
	require.NoError(t, json.Unmarshal([]byte(`{"password":"p"}`), &v))

	calls := 0
	w := &OpaqueWalk{
		Opaque: map[string]bool{"accounts.0.webhooks.password": true, "accounts.webhooks.password": true},
		Match:  func(any) (any, bool) { calls++; return "REDACTED", true },
	}
	prefixes := []prefix{
		{path: "accounts.0.webhooks.", steps: []string{"accounts", "0", "webhooks"}},
		{path: "accounts.webhooks.", steps: []string{"accounts", "webhooks"}},
	}
	v = w.walkValueAt(v, prefixes)

	out, err := json.Marshal(v)
	require.NoError(t, err)
	assert.JSONEq(t, `{"password":"REDACTED"}`, string(out))
	assert.Equal(t, 1, calls, "a single concrete node is handed to the callback once")
	assert.Empty(t, w.Diagnostics(), "two candidate readings of one path are not two segmentations of one hint")
}

// The conservative mode used for an undecodable patch pointer: every hint name
// becomes testable at any depth within the value, so nothing leaks.
func TestOpaqueWalk_MatchAtAnyDepthTestsEveryHintEverywhere(t *testing.T) {
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"deep":{"settings":{"password":"p"},"token":"t"}}`), &m))

	w := &OpaqueWalk{
		Opaque:          map[string]bool{"settings.password": true, "token": true},
		Match:           redactAll,
		MatchAtAnyDepth: true,
	}
	w.WalkProperties(m)

	out, err := json.Marshal(m)
	require.NoError(t, err)
	assert.JSONEq(t, `{"deep":{"settings":{"password":"REDACTED"},"token":"REDACTED"}}`, string(out))
}
