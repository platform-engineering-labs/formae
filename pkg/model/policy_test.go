// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

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

func TestParsePolicy_TTLAbsolute(t *testing.T) {
	raw := json.RawMessage(`{
		"Type": "ttl",
		"Label": "trial",
		"ExpiresAt": "2026-08-07T12:00:00Z",
		"OnDependents": "cascade"
	}`)

	policy, err := ParsePolicy(raw)
	require.NoError(t, err)

	ttl, ok := policy.(*TTLPolicy)
	require.True(t, ok, "expected *TTLPolicy")

	assert.True(t, ttl.IsAbsolute())
	assert.Equal(t, time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC), ttl.ExpiresAt)
	assert.Equal(t, int64(0), ttl.TTLSeconds)
	assert.Equal(t, "cascade", ttl.OnDependents)
}

func TestParsePolicy_TTLOneOf(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{
			name:    "both ttl and expiresAt",
			raw:     `{"Type":"ttl","TTLSeconds":3600,"ExpiresAt":"2026-08-07T12:00:00Z"}`,
			wantErr: "exactly one of TTLSeconds or ExpiresAt",
		},
		{
			name:    "neither ttl nor expiresAt",
			raw:     `{"Type":"ttl","OnDependents":"abort"}`,
			wantErr: "exactly one of TTLSeconds or ExpiresAt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParsePolicy(json.RawMessage(tt.raw))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// A TTL of zero or a negative TTL are legal relative values: presence of the
// key, not its magnitude, selects the variant.
func TestParsePolicy_TTLZeroAndNegativeAreRelative(t *testing.T) {
	for _, seconds := range []int64{0, -10} {
		raw := json.RawMessage(fmt.Sprintf(`{"Type":"ttl","TTLSeconds":%d}`, seconds))

		policy, err := ParsePolicy(raw)
		require.NoError(t, err)

		ttl, ok := policy.(*TTLPolicy)
		require.True(t, ok, "expected *TTLPolicy")
		assert.False(t, ttl.IsAbsolute())
		assert.Equal(t, seconds, ttl.TTLSeconds)
	}
}

func TestParsePolicy_TTLExpiresAtInvalid(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "not a timestamp", raw: `{"Type":"ttl","ExpiresAt":"tomorrow"}`},
		// Well-shaped but not a real calendar date: only a real parse catches it.
		{name: "impossible date", raw: `{"Type":"ttl","ExpiresAt":"2026-02-30T12:00:00Z"}`},
		{name: "empty", raw: `{"Type":"ttl","ExpiresAt":""}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParsePolicy(json.RawMessage(tt.raw))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "ExpiresAt")
		})
	}
}

// Any accepted spelling of an instant canonicalises to the same UTC,
// whole-second value, so stored state is byte-stable across re-applies.
func TestParsePolicy_TTLExpiresAtCanonicalises(t *testing.T) {
	spellings := []string{
		"2026-08-07T12:00:00Z",
		"2026-08-07T14:00:00+02:00",
		"2026-08-07T12:00:00.750Z",
	}

	want := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	for _, spelling := range spellings {
		raw := json.RawMessage(fmt.Sprintf(`{"Type":"ttl","ExpiresAt":%q}`, spelling))

		policy, err := ParsePolicy(raw)
		require.NoError(t, err, spelling)

		ttl := policy.(*TTLPolicy)
		assert.Equal(t, want, ttl.ExpiresAt, spelling)
		assert.Equal(t, "2026-08-07T12:00:00Z", ttl.CanonicalExpiresAt(), spelling)
	}
}

func TestTTLPolicy_CanonicalExpiresAtEmptyWhenRelative(t *testing.T) {
	p := &TTLPolicy{Type: "ttl", TTLSeconds: 3600}
	assert.False(t, p.IsAbsolute())
	assert.Empty(t, p.CanonicalExpiresAt())
}

func TestSetLabel_TTL(t *testing.T) {
	p := &TTLPolicy{}
	p.SetLabel("ephemeral-1h")
	assert.Equal(t, "ephemeral-1h", p.GetLabel())
}

func TestSetLabel_AutoReconcile(t *testing.T) {
	p := &AutoReconcilePolicy{}
	p.SetLabel("reconcile-5m")
	assert.Equal(t, "reconcile-5m", p.GetLabel())
}

func TestParsePolicy_UnknownType(t *testing.T) {
	raw := json.RawMessage(`{"Type": "unknown"}`)

	_, err := ParsePolicy(raw)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown policy type")
}
