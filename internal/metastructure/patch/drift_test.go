// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDriftPatch(t *testing.T) {
	old := json.RawMessage(`{"Value":"/api/v1","Description":"API endpoint"}`)
	cur := json.RawMessage(`{"Value":"/api/v2","Description":"API endpoint"}`)
	p, err := DriftPatch(old, cur)
	require.NoError(t, err)
	var ops []map[string]any
	require.NoError(t, json.Unmarshal(p, &ops))
	require.Len(t, ops, 1)
	assert.Equal(t, "replace", ops[0]["op"])
	assert.Equal(t, "/Value", ops[0]["path"])
	assert.Equal(t, "/api/v2", ops[0]["value"])
}
