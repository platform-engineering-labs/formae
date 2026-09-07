// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package discovery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// A target whose configuration is bound to a generator has no credential until
// that value is drawn, so discovery must not scan through it: the envelope is
// not the credential, and a scan that cannot authenticate returns nothing,
// which is indistinguishable from an empty account. The resource type is
// skipped for the cycle, the way any other per-type scan failure is.
func TestScanTargetForResourceType_UndrawnGeneratorConfigIsNeverSentToAPlugin(t *testing.T) {
	data := newScanData("FakeAWS::S3::Bucket")
	target := data.targets["us-east-1"]
	target.Config = json.RawMessage(`{
		"Region": "us-east-1",
		"Token": {
			"$gen":        true,
			"$generator":  "2abcDEFghiJKLmnoPQRstuVWxyz",
			"$output":     "value",
			"$visibility": "Opaque"
		}
	}`)
	op := data.queuedListOperations["FakeAWS"][0]
	proc := &stubProcess{}

	err := scanTargetForResourceType(target, op, data, proc)

	// The dispatch assertion leads and does not short-circuit on the error
	// assertion: an unguarded scan reports no error at all, so a test that
	// stopped there would never look at what reached the plugin.
	for _, message := range proc.sent {
		_, isList := message.(plugin.ListResources)
		assert.False(t, isList, "no list request may carry a credential formae does not have")
	}
	assert.Empty(t, proc.spawnRequests,
		"a scan that cannot authenticate must not spawn a PluginOperator that is then never sent ListResources and never reaped")
	if assert.Error(t, err, "a target formae cannot authenticate must not be scanned") {
		assert.Contains(t, err.Error(), "has not been drawn",
			"the skip must say why the resource type was not scanned")
	}
	assert.Empty(t, data.outstandingListOperations,
		"a skipped resource type must not be left outstanding, or the cycle never completes")
}

// The guard is scoped to generator references: an ordinary target config still
// scans, so the skip above is not simply refusing every target.
func TestScanTargetForResourceType_OrdinaryConfigIsStillScanned(t *testing.T) {
	data := newScanData("FakeAWS::S3::Bucket")
	target := data.targets["us-east-1"]
	target.Config = json.RawMessage(`{"Region":"us-east-1"}`)
	op := data.queuedListOperations["FakeAWS"][0]
	proc := &stubProcess{}

	require.NoError(t, scanTargetForResourceType(target, op, data, proc))

	var listed bool
	for _, message := range proc.sent {
		if _, isList := message.(plugin.ListResources); isList {
			listed = true
		}
	}
	assert.True(t, listed, "an ordinary target config must still reach the plugin")
}
