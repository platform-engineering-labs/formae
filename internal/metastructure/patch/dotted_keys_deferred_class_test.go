// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// normalizeToFlattenedKeys resolves a document carrying BOTH a dotted key and a
// same-shaped nested map by dropping the nested side. That is a decision about
// which of two representations wins, made on the assumption the two cannot both
// be meant — the same assumption that makes a surgical repair of the historical
// corruption unsound, since equal shapes are evidence of ambiguity and not of
// provenance.
//
// It stays as it is: its dotted form is the inbound convention shared with the
// Kubernetes plugin's normalize shim, so changing it is a cross-layer contract
// change and belongs with the structured-segments redesign. This pins today's
// behavior verbatim so that change is a decision rather than an accident.

func TestNormalizeToFlattenedKeys_NestedSideLoses(t *testing.T) {
	m := map[string]any{
		"a.b": "flattened",
		"a":   map[string]any{"b": "nested"},
	}

	normalizeToFlattenedKeys(m)

	require.Len(t, m, 1, "the nested map is dropped, got %v", m)
	assert.Equal(t, "flattened", m["a.b"])
	_, nestedSurvives := m["a"]
	assert.False(t, nestedSurvives, "the nested side loses unconditionally")
}

// The rule is keyed on the key's own shape, not on any relationship between the
// two: EVERY map value under a dot-free key is dropped, whether or not a dotted
// key names it.
func TestNormalizeToFlattenedKeys_DropsMapsWithNoDottedCounterpart(t *testing.T) {
	m := map[string]any{
		"unrelated": map[string]any{"x": 1},
		"scalar":    "kept",
		"a.b":       "kept",
	}

	normalizeToFlattenedKeys(m)

	_, mapSurvives := m["unrelated"]
	assert.False(t, mapSurvives, "a map under a dot-free key is dropped regardless, got %v", m)
	assert.Equal(t, "kept", m["scalar"])
	assert.Equal(t, "kept", m["a.b"])
}

// A map under a DOTTED key is kept: the rule only fires on dot-free keys.
func TestNormalizeToFlattenedKeys_KeepsMapsUnderDottedKeys(t *testing.T) {
	m := map[string]any{
		"a.b": map[string]any{"c": "kept"},
	}

	normalizeToFlattenedKeys(m)

	require.Len(t, m, 1, "got %v", m)
	assert.Equal(t, map[string]any{"c": "kept"}, m["a.b"])
}
