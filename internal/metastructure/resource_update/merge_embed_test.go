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
	assert.True(t, gjson.GetBytes(out, "functionCode.$embed").Bool(),
		"merge must keep the $embed envelope, not the plugin scalar")
}
