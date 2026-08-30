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

func TestNetworkConfig_PluginConfigJSON_LegacyRawJSONTakesPrecedence(t *testing.T) {
	legacy := json.RawMessage(`{"legacy":true}`)
	cfg := &NetworkConfig{
		Type: "tailscale",
		Tailscale: &TailscaleConfig{
			Hostname: "should-be-ignored",
		},
		LegacyRawJSON: legacy,
	}

	got, err := cfg.PluginConfigJSON()
	require.NoError(t, err)
	assert.JSONEq(t, string(legacy), string(got))
}

func TestNetworkConfig_PluginConfigJSON_MarshalsTypedTailscale(t *testing.T) {
	cfg := &NetworkConfig{
		Type: "tailscale",
		Tailscale: &TailscaleConfig{
			TLS:           true,
			AuthKey:       "key-123",
			Hostname:      "formae-agent",
			AdvertiseTags: []string{"tag:formae"},
		},
	}

	got, err := cfg.PluginConfigJSON()
	require.NoError(t, err)

	want, err := json.Marshal(cfg.Tailscale)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
}

func TestNetworkConfig_PluginConfigJSON_NilTailscaleMarshalsNull(t *testing.T) {
	cfg := &NetworkConfig{
		Type:      "tailscale",
		Tailscale: nil,
	}

	got, err := cfg.PluginConfigJSON()
	require.NoError(t, err)
	assert.Equal(t, "null", string(got))
}

func TestMatchFilterAppliesToEveryTypeWhenNoTypesAreListed(t *testing.T) {
	filter := MatchFilter{
		Conditions: []FilterCondition{{PropertyPath: "$.tags.app", PropertyValue: "formae-agent"}},
	}

	assert.True(t, filter.AppliesTo("AZURE::Compute::VirtualMachine"))
	assert.True(t, filter.AppliesTo("AWS::EC2::Instance"))
}

func TestMatchFilterAppliesOnlyToListedTypes(t *testing.T) {
	filter := MatchFilter{
		ResourceTypes: []string{"AWS::EC2::Instance"},
		Conditions:    []FilterCondition{{PropertyPath: "$.SkipMe", PropertyValue: "yes"}},
	}

	assert.True(t, filter.AppliesTo("AWS::EC2::Instance"))
	assert.False(t, filter.AppliesTo("AWS::S3::Bucket"))
}

func TestFiltersForTypeKeepsUntypedAndMatchingFilters(t *testing.T) {
	untyped := MatchFilter{Conditions: []FilterCondition{{PropertyPath: "$.tags.app"}}}
	matching := MatchFilter{ResourceTypes: []string{"AWS::EC2::Instance"}}
	other := MatchFilter{ResourceTypes: []string{"AWS::S3::Bucket"}}

	got := FiltersForType([]MatchFilter{untyped, matching, other}, "AWS::EC2::Instance")

	assert.Equal(t, []MatchFilter{untyped, matching}, got)
}
