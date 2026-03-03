// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeepEqualIgnoreArrayOrder_EmptyEquivalence(t *testing.T) {
	t.Run("null field vs absent — equal", func(t *testing.T) {
		a := map[string]any{"Tags": nil}
		b := map[string]any{}
		assert.True(t, deepEqualIgnoreArrayOrder(a, b))
	})

	t.Run("empty array vs absent — equal", func(t *testing.T) {
		a := map[string]any{"Tags": []any{}}
		b := map[string]any{}
		assert.True(t, deepEqualIgnoreArrayOrder(a, b))
	})

	t.Run("null vs empty array — equal", func(t *testing.T) {
		a := map[string]any{"Tags": nil}
		b := map[string]any{"Tags": []any{}}
		assert.True(t, deepEqualIgnoreArrayOrder(a, b))
	})

	t.Run("non-empty array vs null — not equal", func(t *testing.T) {
		a := map[string]any{"Tags": []any{map[string]any{"Key": "k"}}}
		b := map[string]any{"Tags": nil}
		assert.False(t, deepEqualIgnoreArrayOrder(a, b))
	})

	t.Run("non-empty array vs absent — not equal", func(t *testing.T) {
		a := map[string]any{"Tags": []any{map[string]any{"Key": "k"}}}
		b := map[string]any{}
		assert.False(t, deepEqualIgnoreArrayOrder(a, b))
	})

	t.Run("empty map vs absent — equal", func(t *testing.T) {
		a := map[string]any{"Nested": map[string]any{}}
		b := map[string]any{}
		assert.True(t, deepEqualIgnoreArrayOrder(a, b))
	})

	t.Run("nil vs nil — equal", func(t *testing.T) {
		assert.True(t, deepEqualIgnoreArrayOrder(nil, nil))
	})

	t.Run("nil vs empty array — equal", func(t *testing.T) {
		assert.True(t, deepEqualIgnoreArrayOrder(nil, []any{}))
	})

	t.Run("nil vs empty map — equal", func(t *testing.T) {
		assert.True(t, deepEqualIgnoreArrayOrder(nil, map[string]any{}))
	})

	t.Run("both sides have value — still works", func(t *testing.T) {
		a := map[string]any{"prop": "value", "Tags": []any{map[string]any{"Key": "k", "Value": "v"}}}
		b := map[string]any{"prop": "value", "Tags": []any{map[string]any{"Key": "k", "Value": "v"}}}
		assert.True(t, deepEqualIgnoreArrayOrder(a, b))
	})

	t.Run("different values — not equal", func(t *testing.T) {
		a := map[string]any{"prop": "value1"}
		b := map[string]any{"prop": "value2"}
		assert.False(t, deepEqualIgnoreArrayOrder(a, b))
	})

	t.Run("extra non-empty key — not equal", func(t *testing.T) {
		a := map[string]any{"prop": "value", "extra": "data"}
		b := map[string]any{"prop": "value"}
		assert.False(t, deepEqualIgnoreArrayOrder(a, b))
	})
}
