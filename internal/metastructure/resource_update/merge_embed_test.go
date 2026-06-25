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
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestMerge_PreservesEmbedEnvelope(t *testing.T) {
	user := json.RawMessage(`{"functionCode":{"$embed":true,"$template":"cf.kvs('X')"}}`)
	plugin := json.RawMessage(`{"functionCode":"cf.kvs('KV-7H9X')"}`) // assembled scalar from cloud
	out, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{})
	require.NoError(t, err)
	// The merged result must be an object (the user's envelope), not the plugin scalar
	assert.True(t, gjson.GetBytes(out, "functionCode").IsObject(),
		"merge must keep the $embed object envelope, not collapse to the plugin scalar")
	assert.True(t, gjson.GetBytes(out, "functionCode.$embed").Bool(),
		"merge must keep $embed:true from user envelope")
	assert.Equal(t, "cf.kvs('X')", gjson.GetBytes(out, "functionCode.$template").String(),
		"merge must keep user's $template expression")
}

// TestMerge_PreservesEmbedEnvelope_PluginReturnsObject is the regression case that
// FAILS without the explicit $embed guard in mergeObject. When the plugin returns an
// OBJECT at functionCode (rather than a scalar), the recursive merge would normally
// descend into it and overwrite the user's $embed:true with the plugin's $embed:false.
func TestMerge_PreservesEmbedEnvelope_PluginReturnsObject(t *testing.T) {
	user := json.RawMessage(`{"functionCode":{"$embed":true,"$template":"cf.kvs('X')"}}`)
	// Plugin returns an object at functionCode — without an explicit guard, the recursive
	// merge would descend into this object and clobber the user's $embed:true.
	plugin := json.RawMessage(`{"functionCode":{"$embed":false,"$template":"stale"}}`)
	out, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{})
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(out, "functionCode.$embed").Bool(),
		"merge must keep user's $embed:true even when plugin returns an object at the same path")
	assert.Equal(t, "cf.kvs('X')", gjson.GetBytes(out, "functionCode.$template").String(),
		"merge must keep user's $template, not the plugin's stale value")
}
