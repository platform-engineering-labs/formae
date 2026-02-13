// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePolicy_AutoReconcile(t *testing.T) {
	raw := json.RawMessage(`{
		"Type": "auto-reconcile",
		"Label": "reconcile-5m",
		"IntervalSeconds": 300
	}`)

	policy, err := ParsePolicy(raw)
	require.NoError(t, err)

	autoReconcile, ok := policy.(*AutoReconcilePolicy)
	require.True(t, ok, "expected *AutoReconcilePolicy")

	assert.Equal(t, "auto-reconcile", autoReconcile.GetType())
	assert.Equal(t, "reconcile-5m", autoReconcile.GetLabel())
	assert.Equal(t, int64(300), autoReconcile.IntervalSeconds)
	assert.Empty(t, autoReconcile.GetStackID())

	// Test SetStackID
	autoReconcile.SetStackID("stack-123")
	assert.Equal(t, "stack-123", autoReconcile.GetStackID())
}

func TestParsePolicy_TTL(t *testing.T) {
	raw := json.RawMessage(`{
		"Type": "ttl",
		"Label": "ephemeral",
		"TTLSeconds": 3600,
		"OnDependents": "cascade"
	}`)

	policy, err := ParsePolicy(raw)
	require.NoError(t, err)

	ttl, ok := policy.(*TTLPolicy)
	require.True(t, ok, "expected *TTLPolicy")

	assert.Equal(t, "ttl", ttl.GetType())
	assert.Equal(t, "ephemeral", ttl.GetLabel())
	assert.Equal(t, int64(3600), ttl.TTLSeconds)
	assert.Equal(t, "cascade", ttl.OnDependents)
}

func TestParsePolicy_UnknownType(t *testing.T) {
	raw := json.RawMessage(`{"Type": "unknown"}`)

	_, err := ParsePolicy(raw)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown policy type")
}
