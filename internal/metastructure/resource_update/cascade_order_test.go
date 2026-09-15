//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package resource_update

import (
	"encoding/json"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCascadeUpdatePatchStableAcrossPlans(t *testing.T) {
	dependent := pkgmodel.Resource{Properties: json.RawMessage(`{"second":{"$ref":"formae://b#/name"},"first":{"$ref":"formae://a#/name"}}`)}
	parents := map[string]*pkgmodel.Resource{"a": {Properties: json.RawMessage(`{"name":"new-a"}`)}, "b": {Properties: json.RawMessage(`{"name":"new-b"}`)}}
	for range 64 {
		patch, err := synthesizeCascadeUpdatePatch(dependent, map[string]bool{"a": true, "b": true}, map[string]bool{"a": true, "b": true}, map[string]string{"a": "a", "b": "b"}, parents)
		require.NoError(t, err)
		require.Equal(t, `[{"op":"replace","path":"/first","value":"new-a"},{"op":"replace","path":"/second","value":"new-b"}]`, string(patch), "review hashes must not change with reference map iteration")
	}
}
