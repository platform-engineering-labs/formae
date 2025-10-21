// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ccx

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStripIgnoredFields(t *testing.T) {
	jsonPayload := []byte(`{
	"foo": "value to ignore",
	"bar": "another value",
	"baz": {
		"qux": "value to ignore",
		"quux": "value to keep"
	}
}`)
	unmarshaled := make(map[string]any)
	err := json.Unmarshal(jsonPayload, &unmarshaled)
	require.NoError(t, err)

	ignoredFields := []string{"$.foo", "$.baz.qux"}

	err = stripIgnoredFields(unmarshaled, ignoredFields)
	require.NoError(t, err)

	require.NotContains(t, unmarshaled, "foo")
	require.Contains(t, unmarshaled, "bar")
	require.Contains(t, unmarshaled, "baz")
	require.NotContains(t, unmarshaled["baz"].(map[string]any), "qux")
	require.Contains(t, unmarshaled["baz"].(map[string]any), "quux")
}
