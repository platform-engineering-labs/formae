// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TestResolveOpaqueTargetConfig_NoOpaqueRefsPassthrough asserts that a config
// with no opaque references needs no source read (a nil process is safe) and is
// returned as plain plugin JSON with any cached resolvable metadata stripped.
// The opaque-resolving paths are covered end-to-end via the discovery wiring
// tests, which drive this same routine through resolveTargetConfigForList.
func TestResolveOpaqueTargetConfig_NoOpaqueRefsPassthrough(t *testing.T) {
	// A plain config plus a NON-opaque ($visibility Clear) resolvable carrying a
	// cached $value: no opaque ref, so no plugin read; the $value is used and the
	// $ref wrapper stripped.
	target := pkgmodel.Target{
		Label: "t1",
		Config: json.RawMessage(`{
			"Region": "us-east-1",
			"Url": {"$ref": "formae://abc#/status.url", "$value": "http://svc", "$visibility": "Clear"}
		}`),
	}

	// No opaque refs → the routine must not touch the process.
	out, err := ResolveOpaqueTargetConfig(nil, target)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, "us-east-1", got["Region"])
	assert.Equal(t, "http://svc", got["Url"], "non-opaque ref collapses to its cached value")
	assert.NotContains(t, string(out), "$ref", "resolvable wrappers stripped for the plugin")
}
