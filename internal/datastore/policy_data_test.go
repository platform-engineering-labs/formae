// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"testing"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The expiry queries compare absolute deadlines as strings, which cannot tell a
// real calendar date from an impossible one. Whatever they report is therefore
// re-checked against a real parse before anything is destroyed.
func TestExpiredStackInfo_HasUnreadableDeadline(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt string
		want      bool
	}{
		{name: "canonical", expiresAt: "2026-08-07T12:00:00Z", want: false},
		{name: "relative policy carries no deadline string", expiresAt: "", want: false},
		// Shape-valid but not a real date: the string comparison lets this
		// through, a real parse does not.
		{name: "impossible calendar date", expiresAt: "2025-02-30T12:00:00Z", want: true},
		{name: "impossible month", expiresAt: "2026-13-01T12:00:00Z", want: true},
		{name: "impossible hour", expiresAt: "2026-08-07T25:00:00Z", want: true},
		{name: "not a timestamp", expiresAt: "not-a-timestamp", want: true},
		{name: "below the supported floor", expiresAt: "1999-12-31T23:59:59Z", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ExpiredStackInfo{StackLabel: "s", ExpiresAt: tt.expiresAt}
			assert.Equal(t, tt.want, info.HasUnreadableDeadline())
		})
	}
}

func TestTTLPolicyData_RelativeWritesOnlyTTLSeconds(t *testing.T) {
	data := TTLPolicyData(&pkgmodel.TTLPolicy{
		Type:         "ttl",
		TTLSeconds:   3600,
		OnDependents: "cascade",
	})

	require.Contains(t, data, "TTLSeconds")
	assert.NotContains(t, data, "ExpiresAt")
	assert.Equal(t, int64(3600), data["TTLSeconds"])
	assert.Equal(t, "cascade", data["OnDependents"])
}

func TestTTLPolicyData_AbsoluteWritesOnlyExpiresAt(t *testing.T) {
	data := TTLPolicyData(&pkgmodel.TTLPolicy{
		Type:         "ttl",
		ExpiresAt:    time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
		OnDependents: "abort",
	})

	require.Contains(t, data, "ExpiresAt")
	assert.NotContains(t, data, "TTLSeconds")
	assert.Equal(t, "2026-08-07T12:00:00Z", data["ExpiresAt"])
	assert.Equal(t, "abort", data["OnDependents"])
}

// A zero or negative TTL is a legal relative deadline and must still be written
// as TTLSeconds — the key's presence is what selects the variant on read back.
func TestTTLPolicyData_ZeroAndNegativeTTLStayRelative(t *testing.T) {
	for _, seconds := range []int64{0, -10} {
		data := TTLPolicyData(&pkgmodel.TTLPolicy{Type: "ttl", TTLSeconds: seconds})

		require.Contains(t, data, "TTLSeconds")
		assert.NotContains(t, data, "ExpiresAt")
		assert.Equal(t, seconds, data["TTLSeconds"])
	}
}

func TestTTLPolicyFromData_Relative(t *testing.T) {
	p, err := TTLPolicyFromData("ephemeral", `{"TTLSeconds":3600,"OnDependents":"cascade"}`, "stack-1")
	require.NoError(t, err)

	assert.False(t, p.IsAbsolute())
	assert.Equal(t, int64(3600), p.TTLSeconds)
	assert.Equal(t, "cascade", p.OnDependents)
	assert.Equal(t, "stack-1", p.StackID)
}

func TestTTLPolicyFromData_Absolute(t *testing.T) {
	p, err := TTLPolicyFromData("trial", `{"ExpiresAt":"2026-08-07T12:00:00Z","OnDependents":"abort"}`, "stack-1")
	require.NoError(t, err)

	assert.True(t, p.IsAbsolute())
	assert.Equal(t, time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC), p.ExpiresAt)
	assert.Equal(t, int64(0), p.TTLSeconds)
}

// Deserialization is deliberately lenient: stored state that no longer parses
// must stay readable, so a bad deadline degrades to "no deadline" rather than
// making the whole policy — and the stack that carries it — unloadable.
func TestTTLPolicyFromData_MalformedExpiresAtDegradesToNoDeadline(t *testing.T) {
	p, err := TTLPolicyFromData("trial", `{"ExpiresAt":"not-a-time"}`, "stack-1")
	require.NoError(t, err)

	assert.False(t, p.IsAbsolute())
	assert.Equal(t, int64(0), p.TTLSeconds)
}

// Not reachable through any accepted input, but hand-edited or corrupt rows
// must resolve the same way the expiry query resolves them: ExpiresAt wins.
func TestTTLPolicyFromData_BothSetResolvesToExpiresAt(t *testing.T) {
	p, err := TTLPolicyFromData("trial", `{"TTLSeconds":3600,"ExpiresAt":"2026-08-07T12:00:00Z"}`, "stack-1")
	require.NoError(t, err)

	assert.True(t, p.IsAbsolute())
	assert.Equal(t, time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC), p.ExpiresAt)
	assert.Equal(t, int64(0), p.TTLSeconds)
}

func TestTTLPolicyFromData_NeitherSetHasNoDeadline(t *testing.T) {
	p, err := TTLPolicyFromData("trial", `{"OnDependents":"abort"}`, "stack-1")
	require.NoError(t, err)

	assert.False(t, p.IsAbsolute())
	assert.Equal(t, int64(0), p.TTLSeconds)
}

// Round-tripping any variant through storage must preserve which variant it is.
func TestTTLPolicyData_RoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		policy *pkgmodel.TTLPolicy
	}{
		{name: "relative", policy: &pkgmodel.TTLPolicy{Type: "ttl", TTLSeconds: 3600, OnDependents: "abort"}},
		{name: "relative zero", policy: &pkgmodel.TTLPolicy{Type: "ttl", TTLSeconds: 0, OnDependents: "abort"}},
		{
			name: "absolute",
			policy: &pkgmodel.TTLPolicy{
				Type:         "ttl",
				ExpiresAt:    time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
				OnDependents: "cascade",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(TTLPolicyData(tt.policy))
			require.NoError(t, err)

			decoded, err := TTLPolicyFromData("l", string(encoded), "stack-1")
			require.NoError(t, err)

			assert.Equal(t, tt.policy.IsAbsolute(), decoded.IsAbsolute())
			assert.Equal(t, tt.policy.TTLSeconds, decoded.TTLSeconds)
			assert.Equal(t, tt.policy.ExpiresAt, decoded.ExpiresAt)
			assert.Equal(t, tt.policy.OnDependents, decoded.OnDependents)
		})
	}
}

// Storage is always canonical, so an absolute deadline supplied in another
// timezone or with sub-second precision serializes byte-identically.
func TestTTLPolicyData_ExpiresAtIsCanonical(t *testing.T) {
	offset := time.FixedZone("CEST", 2*60*60)
	data := TTLPolicyData(&pkgmodel.TTLPolicy{
		Type:      "ttl",
		ExpiresAt: time.Date(2026, 8, 7, 14, 0, 0, 750_000_000, offset),
	})

	assert.Equal(t, "2026-08-07T12:00:00Z", data["ExpiresAt"])
}
