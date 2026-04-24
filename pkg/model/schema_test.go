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

func TestFieldHintHostsOnJSONRoundTrip(t *testing.T) {
	original := FieldHint{HostsOn: true}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded FieldHint
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.True(t, decoded.HostsOn, "HostsOn should round-trip through JSON (raw=%s)", string(data))
}

func TestFieldHintHostsOnDefaultsFalse(t *testing.T) {
	var decoded FieldHint
	require.NoError(t, json.Unmarshal([]byte(`{"CreateOnly":true}`), &decoded))

	assert.False(t, decoded.HostsOn, "HostsOn should default false for pre-existing stored schemas")
}
