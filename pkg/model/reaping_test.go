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

func TestResolveReaping_ExplicitWins(t *testing.T) {
	explicit := &ReapAfter{Kind: "after", MaxUnreachableSeconds: 3600}
	manifestDefault := &NeverReap{Kind: "never"}

	resolved := ResolveReaping(explicit, manifestDefault)

	assert.Equal(t, explicit, resolved)
}

func TestResolveReaping_ManifestDefaultWhenNoExplicit(t *testing.T) {
	manifestDefault := &NeverReap{Kind: "never"}

	resolved := ResolveReaping(nil, manifestDefault)

	assert.Equal(t, manifestDefault, resolved)
}

func TestResolveReaping_GlobalDefaultWhenNothingSet(t *testing.T) {
	resolved := ResolveReaping(nil, nil)

	after, ok := resolved.(*ReapAfter)
	require.True(t, ok, "global default must be a ReapAfter")
	assert.Equal(t, "after", after.Kind)
	assert.Equal(t, DefaultReapMaxUnreachableSeconds, after.MaxUnreachableSeconds)
}

func TestReapingToColumns_After(t *testing.T) {
	kind, max, err := ReapingToColumns(json.RawMessage(`{"Kind":"after","MaxUnreachableSeconds":7200}`))
	require.NoError(t, err)
	assert.Equal(t, "after", kind)
	assert.Equal(t, int64(7200), max)
}

func TestReapingToColumns_Never(t *testing.T) {
	kind, max, err := ReapingToColumns(json.RawMessage(`{"Kind":"never"}`))
	require.NoError(t, err)
	assert.Equal(t, "never", kind)
	assert.Equal(t, DefaultReapMaxUnreachableSeconds, max)
}

func TestReapingToColumns_NilFallsBackToDefault(t *testing.T) {
	kind, max, err := ReapingToColumns(nil)
	require.NoError(t, err)
	assert.Equal(t, "after", kind)
	assert.Equal(t, DefaultReapMaxUnreachableSeconds, max)
}

func TestReapingRawFromColumns_After(t *testing.T) {
	raw := ReapingRawFromColumns("after", 1800)

	behaviour, err := ParseReaping(raw)
	require.NoError(t, err)
	after, ok := behaviour.(*ReapAfter)
	require.True(t, ok)
	assert.Equal(t, int64(1800), after.MaxUnreachableSeconds)
}

func TestReapingRawFromColumns_Never(t *testing.T) {
	raw := ReapingRawFromColumns("never", 1800)

	behaviour, err := ParseReaping(raw)
	require.NoError(t, err)
	_, ok := behaviour.(*NeverReap)
	assert.True(t, ok, "never kind must reconstruct a NeverReap regardless of stored seconds")
}

func TestMarshalReaping_RoundTrip(t *testing.T) {
	raw, err := MarshalReaping(&ReapAfter{Kind: "after", MaxUnreachableSeconds: 900})
	require.NoError(t, err)

	behaviour, err := ParseReaping(raw)
	require.NoError(t, err)
	after, ok := behaviour.(*ReapAfter)
	require.True(t, ok)
	assert.Equal(t, int64(900), after.MaxUnreachableSeconds)
}
