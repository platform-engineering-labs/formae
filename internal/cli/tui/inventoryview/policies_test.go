// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// ---------------------------------------------------------------------------
// policyRow: cell builders
// ---------------------------------------------------------------------------

func TestPolicyRow_Cells_Basic(t *testing.T) {
	p := apimodel.PolicyInventoryItem{
		Label:          "my-ttl",
		Type:           "ttl",
		Config:         json.RawMessage(`{"TTLSeconds":3600}`),
		AttachedStacks: []string{"prod", "staging"},
	}
	got := policyRow(p)
	assert.Equal(t, "my-ttl", got.cells[0])
	assert.Equal(t, "ttl", got.cells[1])
	assert.Equal(t, "TTLSeconds: 3600", got.cells[2])
	assert.Equal(t, "prod, staging", got.cells[3])
}

func TestPolicyRow_Cells_NoAttachedStacks(t *testing.T) {
	p := apimodel.PolicyInventoryItem{
		Label:          "orphan-policy",
		Type:           "auto-reconcile",
		Config:         json.RawMessage(`{"IntervalSeconds":300}`),
		AttachedStacks: nil,
	}
	got := policyRow(p)
	assert.Equal(t, "none", got.cells[3])
}

func TestPolicyRow_Cells_EmptyAttachedStacks(t *testing.T) {
	p := apimodel.PolicyInventoryItem{
		Label:          "p",
		Type:           "ttl",
		Config:         json.RawMessage(`{}`),
		AttachedStacks: []string{},
	}
	got := policyRow(p)
	assert.Equal(t, "none", got.cells[3])
}

func TestPolicyRow_Cells_SingleAttachedStack(t *testing.T) {
	p := apimodel.PolicyInventoryItem{
		Label:          "p",
		Type:           "ttl",
		Config:         json.RawMessage(`{}`),
		AttachedStacks: []string{"only-stack"},
	}
	got := policyRow(p)
	assert.Equal(t, "only-stack", got.cells[3])
}

func TestPolicyRow_Cells_CompactKVConfig(t *testing.T) {
	// Config with multiple keys — compactKV returns sorted k: v
	p := apimodel.PolicyInventoryItem{
		Label:  "p",
		Type:   "ttl",
		Config: json.RawMessage(`{"onDependents":"cascade","TTLSeconds":7200}`),
	}
	got := policyRow(p)
	assert.Equal(t, "TTLSeconds: 7200, onDependents: cascade", got.cells[2])
}

// ---------------------------------------------------------------------------
// policyRow: detail renderer
// ---------------------------------------------------------------------------

func TestPolicyRow_Detail_IdentityLines(t *testing.T) {
	p := apimodel.PolicyInventoryItem{
		Label:  "my-ttl",
		Type:   "ttl",
		Config: json.RawMessage(`{"TTLSeconds":3600}`),
	}
	got := policyRow(p)
	require.NotNil(t, got.detail)
	lines := got.detail(80)

	assert.Equal(t, "Label: my-ttl", lines[0])
	assert.Equal(t, "Type:  ttl", lines[1])
}

func TestPolicyRow_Detail_ConfigTree(t *testing.T) {
	p := apimodel.PolicyInventoryItem{
		Label:  "p",
		Type:   "ttl",
		Config: json.RawMessage(`{"TTLSeconds":3600,"onDependents":"abort"}`),
	}
	got := policyRow(p)
	lines := got.detail(80)

	assert.Contains(t, lines, "Config:")
	assert.Contains(t, lines, " TTLSeconds: 3600")
	assert.Contains(t, lines, " onDependents: abort")
}

func TestPolicyRow_Detail_AttachedStacks(t *testing.T) {
	p := apimodel.PolicyInventoryItem{
		Label:          "p",
		Type:           "ttl",
		Config:         json.RawMessage(`{}`),
		AttachedStacks: []string{"prod", "staging"},
	}
	got := policyRow(p)
	lines := got.detail(80)

	assert.Contains(t, lines, "Attached stacks:")
	assert.Contains(t, lines, "  - prod")
	assert.Contains(t, lines, "  - staging")
}

func TestPolicyRow_Detail_NoAttachedStacks(t *testing.T) {
	p := apimodel.PolicyInventoryItem{
		Label:          "orphan",
		Type:           "auto-reconcile",
		Config:         json.RawMessage(`{"IntervalSeconds":300}`),
		AttachedStacks: nil,
	}
	got := policyRow(p)
	lines := got.detail(80)

	assert.Contains(t, lines, "Attached stacks:")
	assert.Contains(t, lines, "  none")
}

func TestPolicyRow_Detail_BlanksBetweenSections(t *testing.T) {
	p := apimodel.PolicyInventoryItem{
		Label:  "p",
		Type:   "ttl",
		Config: json.RawMessage(`{"TTLSeconds":3600}`),
	}
	got := policyRow(p)
	lines := got.detail(80)

	// Find "Config:" and verify blank precedes it
	configIdx := -1
	attachedIdx := -1
	for i, l := range lines {
		if l == "Config:" {
			configIdx = i
		}
		if l == "Attached stacks:" {
			attachedIdx = i
		}
	}
	require.Greater(t, configIdx, 0)
	assert.Equal(t, "", lines[configIdx-1], "blank before Config:")
	require.Greater(t, attachedIdx, 0)
	assert.Equal(t, "", lines[attachedIdx-1], "blank before Attached stacks:")
}

// ---------------------------------------------------------------------------
// fetch integration via newSpecs
// ---------------------------------------------------------------------------

func TestPoliciesSpec_FetchDelegates(t *testing.T) {
	p1 := apimodel.PolicyInventoryItem{
		Label:          "p1",
		Type:           "ttl",
		Config:         json.RawMessage(`{"TTLSeconds":3600}`),
		AttachedStacks: []string{"prod"},
	}
	p2 := apimodel.PolicyInventoryItem{
		Label:          "p2",
		Type:           "auto-reconcile",
		Config:         json.RawMessage(`{"IntervalSeconds":300}`),
		AttachedStacks: nil,
	}
	c := &fakeClient{policies: []apimodel.PolicyInventoryItem{p1, p2}}
	specs := newSpecs(nil)
	rows, nags, err := specs[TabPolicies].fetch(c, "", true)
	require.NoError(t, err)
	assert.Empty(t, nags)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"p1", "ttl", "TTLSeconds: 3600", "prod"}, rows[0].cells)
	assert.Equal(t, []string{"p2", "auto-reconcile", "IntervalSeconds: 300", "none"}, rows[1].cells)
	assert.NotNil(t, rows[0].detail)
	assert.NotNil(t, rows[1].detail)
}
