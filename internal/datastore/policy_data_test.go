// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"testing"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
