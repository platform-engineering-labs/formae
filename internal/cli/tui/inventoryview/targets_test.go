// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ---------------------------------------------------------------------------
// targetRow: cell builders
// ---------------------------------------------------------------------------

func TestTargetRow_Cells_Discoverable(t *testing.T) {
	t1 := &pkgmodel.Target{
		Label:        "aws-prod",
		Namespace:    "AWS",
		Discoverable: true,
		Config:       json.RawMessage(`{"Account":"123456","Region":"us-east-1"}`),
	}
	got := targetRow(t1)
	assert.Equal(t, "aws-prod", got.cells[0])
	assert.Equal(t, "AWS", got.cells[1])
	assert.Equal(t, "yes", got.cells[2])
	// Config is compactKV: sorted keys
	assert.Equal(t, "Account: 123456, Region: us-east-1", got.cells[3])
}

func TestTargetRow_Cells_NotDiscoverable(t *testing.T) {
	t1 := &pkgmodel.Target{
		Label:        "dev",
		Namespace:    "Azure",
		Discoverable: false,
		Config:       json.RawMessage(`{}`),
	}
	got := targetRow(t1)
	assert.Equal(t, "no", got.cells[2])
}

func TestTargetRow_Cells_Config_RefValueUnwrapping(t *testing.T) {
	// Config with a $ref/$value pair — compactKV should unwrap to the $value
	t1 := &pkgmodel.Target{
		Config: json.RawMessage(`{"Region":{"$ref":"formae://x","$value":"eu-west-1"},"Account":"999"}`),
	}
	got := targetRow(t1)
	assert.Equal(t, "Account: 999, Region: eu-west-1", got.cells[3])
}

// ---------------------------------------------------------------------------
// targetRow: detail renderer
// ---------------------------------------------------------------------------

func TestTargetRow_Detail_IdentityLines(t *testing.T) {
	t1 := &pkgmodel.Target{
		Label:        "aws-prod",
		Namespace:    "AWS",
		Discoverable: true,
		Version:      3,
		Config:       json.RawMessage(`{"Account":"123"}`),
	}
	got := targetRow(t1)
	require.NotNil(t, got.detail)
	lines := got.detail(80)

	assert.Contains(t, lines, "Label:        aws-prod")
	assert.Contains(t, lines, "Namespace:    AWS")
	assert.Contains(t, lines, "Discoverable: yes")
	assert.Contains(t, lines, "Version:      3")
}

func TestTargetRow_Detail_ConfigTree(t *testing.T) {
	t1 := &pkgmodel.Target{
		Config: json.RawMessage(`{"Account":"123","Region":"us-east-1"}`),
	}
	got := targetRow(t1)
	lines := got.detail(80)

	assert.Contains(t, lines, "Config:")
	assert.Contains(t, lines, " Account: 123")
	assert.Contains(t, lines, " Region: us-east-1")
}

func TestTargetRow_Detail_DiscoverableNo(t *testing.T) {
	t1 := &pkgmodel.Target{
		Discoverable: false,
		Config:       json.RawMessage(`{}`),
	}
	got := targetRow(t1)
	lines := got.detail(80)
	assert.Contains(t, lines, "Discoverable: no")
}

// ---------------------------------------------------------------------------
// fetch integration via newSpecs
// ---------------------------------------------------------------------------

func TestTargetsSpec_FetchDelegates(t *testing.T) {
	c := &fakeClient{
		targets: []*pkgmodel.Target{
			{Label: "aws-prod", Namespace: "AWS", Discoverable: true, Config: json.RawMessage(`{"Account":"1"}`)},
			{Label: "dev", Namespace: "Azure", Discoverable: false, Config: json.RawMessage(`{}`)},
		},
	}
	specs := newSpecs(nil)
	rows, nags, err := specs[TabTargets].fetch(c, "", true)
	require.NoError(t, err)
	assert.Empty(t, nags)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"aws-prod", "AWS", "yes", "Account: 1"}, rows[0].cells)
	assert.Equal(t, []string{"dev", "Azure", "no", ""}, rows[1].cells)
	assert.NotNil(t, rows[0].detail)
	assert.NotNil(t, rows[1].detail)
}
