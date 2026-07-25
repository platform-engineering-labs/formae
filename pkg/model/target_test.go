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

func TestParseReaping_Never(t *testing.T) {
	raw := json.RawMessage(`{"Kind": "never"}`)

	reaping, err := ParseReaping(raw)
	require.NoError(t, err)

	never, ok := reaping.(*NeverReap)
	require.True(t, ok, "expected *NeverReap")
	assert.Equal(t, "never", never.GetKind())
}

func TestParseReaping_After(t *testing.T) {
	raw := json.RawMessage(`{"Kind": "after", "MaxUnreachableSeconds": 3600}`)

	reaping, err := ParseReaping(raw)
	require.NoError(t, err)

	after, ok := reaping.(*ReapAfter)
	require.True(t, ok, "expected *ReapAfter")
	assert.Equal(t, "after", after.GetKind())
	assert.Equal(t, int64(3600), after.MaxUnreachableSeconds)
}

func TestParseReaping_UnknownKind(t *testing.T) {
	raw := json.RawMessage(`{"Kind": "unknown"}`)

	_, err := ParseReaping(raw)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown reaping kind")
}

func TestParseReaping_Null(t *testing.T) {
	reaping, err := ParseReaping(nil)
	require.NoError(t, err)
	assert.Nil(t, reaping)

	reaping, err = ParseReaping(json.RawMessage("null"))
	require.NoError(t, err)
	assert.Nil(t, reaping)
}

// TestTarget_ReapingRoundTrip guards against a regression where Target.Reaping
// was a bare ReapingBehaviour interface: encoding/json cannot unmarshal a
// present JSON object into a nil interface field, which broke persistence of
// TargetUpdates (Target is embedded there and round-tripped through
// json.Marshal/Unmarshal). Reaping must stay a json.RawMessage on Target.
func TestTarget_ReapingRoundTrip(t *testing.T) {
	original := Target{
		Label:     "my-target",
		Namespace: "aws",
		Reaping:   json.RawMessage(`{"Kind": "after", "MaxUnreachableSeconds": 1800}`),
	}

	marshaled, err := json.Marshal(original)
	require.NoError(t, err)

	var roundTripped Target
	err = json.Unmarshal(marshaled, &roundTripped)
	require.NoError(t, err)

	reaping, err := ParseReaping(roundTripped.Reaping)
	require.NoError(t, err)

	after, ok := reaping.(*ReapAfter)
	require.True(t, ok, "expected *ReapAfter")
	assert.Equal(t, int64(1800), after.MaxUnreachableSeconds)
}
