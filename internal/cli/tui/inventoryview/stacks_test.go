// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// fixed clock used by all stacks tests
var stacksNow = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// stackRow: cell builders — summary strings
// ---------------------------------------------------------------------------

func TestStackRow_Cells_NoPolicies(t *testing.T) {
	s := &pkgmodel.Stack{
		Label:       "empty-stack",
		Description: "A stack with no policies",
		Policies:    nil,
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, []string{"empty-stack", "A stack with no policies", "none"}, got.cells)
}

func TestStackRow_Cells_EmptyPolicies(t *testing.T) {
	s := &pkgmodel.Stack{
		Label:    "empty-stack",
		Policies: []json.RawMessage{},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "none", got.cells[2])
}

func TestStackRow_Cells_TTLPolicy_ExpiresIn23h(t *testing.T) {
	// CreatedAt is 1h before now; TTL is 24h → expires in 23h0m (renderer format: "in %dh%dm")
	// formatTTLDur(24h) == "1d" because 24h >= 24*time.Hour triggers the days branch
	createdAt := stacksNow.Add(-1 * time.Hour)
	ttlJSON := json.RawMessage(`{"Type":"ttl","TTLSeconds":86400}`)
	s := &pkgmodel.Stack{
		Label:     "ttl-stack",
		CreatedAt: createdAt,
		Policies:  []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	// renderer: formatTTLDuration(24h) = "1d", formatExpiryTime remaining 23h = "in 23h0m"
	assert.Equal(t, "TTL: 1d, expires in 23h0m", got.cells[2])
}

func TestStackRow_Cells_TTLPolicy_ExpiresIn23h45m(t *testing.T) {
	// CreatedAt is 15 minutes before now; TTL is 24h → expires in 23h45m
	// formatTTLDur(24h) == "1d" (days branch)
	createdAt := stacksNow.Add(-15 * time.Minute)
	ttlJSON := json.RawMessage(`{"Type":"ttl","TTLSeconds":86400}`)
	s := &pkgmodel.Stack{
		Label:     "ttl-stack",
		CreatedAt: createdAt,
		Policies:  []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	// renderer: "in %dh%dm"
	assert.Equal(t, "TTL: 1d, expires in 23h45m", got.cells[2])
}

func TestStackRow_Cells_TTLPolicy_Expired(t *testing.T) {
	// Created 25h ago with 24h TTL → expired; formatTTLDur(24h) == "1d"
	createdAt := stacksNow.Add(-25 * time.Hour)
	ttlJSON := json.RawMessage(`{"Type":"ttl","TTLSeconds":86400}`)
	s := &pkgmodel.Stack{
		Label:     "old-stack",
		CreatedAt: createdAt,
		Policies:  []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	// expired TTL renders as "TTL: <dur> (expired)"
	assert.Equal(t, "TTL: 1d (expired)", got.cells[2])
}

func TestStackRow_Cells_TTLPolicy_NoCreatedAt(t *testing.T) {
	// Zero CreatedAt — no expiry string
	ttlJSON := json.RawMessage(`{"Type":"ttl","TTLSeconds":3600}`)
	s := &pkgmodel.Stack{
		Label:    "no-ts-stack",
		Policies: []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "TTL: 1h", got.cells[2])
}

func TestStackRow_Cells_AutoReconcile_NoLastRun(t *testing.T) {
	// 5 minute interval, no LastReconcileAt
	arJSON := json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":300}`)
	s := &pkgmodel.Stack{
		Label:    "ar-stack",
		Policies: []json.RawMessage{arJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "Auto-reconcile: every 5m", got.cells[2])
}

func TestStackRow_Cells_AutoReconcile_WithLastRun(t *testing.T) {
	// last run 2 minutes ago
	lastRun := stacksNow.Add(-2 * time.Minute)
	arJSON, _ := json.Marshal(map[string]any{
		"Type":            "auto-reconcile",
		"IntervalSeconds": float64(300),
		"LastReconcileAt": lastRun.Format(time.RFC3339),
	})
	s := &pkgmodel.Stack{
		Label:    "ar-stack",
		Policies: []json.RawMessage{arJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "Auto-reconcile: every 5m, last 2m ago", got.cells[2])
}

func TestStackRow_Cells_PolicyReference_RendersLabel(t *testing.T) {
	refJSON := json.RawMessage(`{"$ref":"policy://shared-retention"}`)
	s := &pkgmodel.Stack{
		Label:    "ref-stack",
		Policies: []json.RawMessage{refJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "shared-retention", got.cells[2])
}

func TestStackRow_Cells_MultiplePolicies(t *testing.T) {
	arJSON := json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":300}`)
	refJSON := json.RawMessage(`{"$ref":"policy://my-ttl"}`)
	s := &pkgmodel.Stack{
		Label:    "multi-stack",
		Policies: []json.RawMessage{arJSON, refJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "Auto-reconcile: every 5m, my-ttl", got.cells[2])
}

func TestStackRow_Cells_UnknownPolicyType(t *testing.T) {
	unknownJSON := json.RawMessage(`{"Type":"future-type"}`)
	s := &pkgmodel.Stack{
		Label:    "unknown-stack",
		Policies: []json.RawMessage{unknownJSON},
	}
	got := stackRow(s, stacksNow)
	// unknown non-empty Type → render the type string
	assert.Equal(t, "future-type", got.cells[2])
}

func TestStackRow_Cells_UnparsablePolicy_NoPanic(t *testing.T) {
	badJSON := json.RawMessage(`not-json`)
	s := &pkgmodel.Stack{
		Label:    "bad-stack",
		Policies: []json.RawMessage{badJSON},
	}
	// Should not panic and should produce "none" (no valid parts)
	got := stackRow(s, stacksNow)
	assert.Equal(t, "none", got.cells[2])
}

// ---------------------------------------------------------------------------
// stackRow: detail renderer
// ---------------------------------------------------------------------------

func TestStackRow_Detail_IdentityLines(t *testing.T) {
	createdAt := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	s := &pkgmodel.Stack{
		Label:       "my-stack",
		Description: "My test stack",
		CreatedAt:   createdAt,
	}
	got := stackRow(s, stacksNow)
	require.NotNil(t, got.detail)
	lines := got.detail(80)

	assert.Equal(t, "Label:       my-stack", lines[0])
	assert.Equal(t, "Description: My test stack", lines[1])
	assert.Equal(t, "CreatedAt:   2026-01-15T10:30:00Z", lines[2])
}

func TestStackRow_Detail_NoPolicies(t *testing.T) {
	s := &pkgmodel.Stack{
		Label:    "empty",
		Policies: nil,
	}
	got := stackRow(s, stacksNow)
	lines := got.detail(80)

	// should have "Policies:" heading
	assert.Contains(t, lines, "Policies:")
	// but no policy detail lines
	hasNone := false
	for _, l := range lines {
		if l == "  none" {
			hasNone = true
		}
	}
	assert.True(t, hasNone, "should have '  none' for empty policies in detail")
}

func TestStackRow_Detail_InlinePolicy_JsonTree(t *testing.T) {
	ttlJSON := json.RawMessage(`{"Type":"ttl","TTLSeconds":3600}`)
	s := &pkgmodel.Stack{
		Label:    "ttl-stack",
		Policies: []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	lines := got.detail(80)

	assert.Contains(t, lines, "Policies:")
	// jsonTree renders sorted keys with 1-space indent
	assert.Contains(t, lines, " TTLSeconds: 3600")
	assert.Contains(t, lines, " Type: ttl")
}

func TestStackRow_Detail_PolicyReference(t *testing.T) {
	refJSON := json.RawMessage(`{"$ref":"policy://shared-retention"}`)
	s := &pkgmodel.Stack{
		Label:    "ref-stack",
		Policies: []json.RawMessage{refJSON},
	}
	got := stackRow(s, stacksNow)
	lines := got.detail(80)

	assert.Contains(t, lines, "Policies:")
	assert.Contains(t, lines, "  → policy: shared-retention")
}

func TestStackRow_Detail_HasBlankBeforePolicies(t *testing.T) {
	s := &pkgmodel.Stack{
		Label:    "s",
		Policies: nil,
	}
	got := stackRow(s, stacksNow)
	lines := got.detail(80)

	// Find blank line between identity and Policies:
	policiesIdx := -1
	for i, l := range lines {
		if l == "Policies:" {
			policiesIdx = i
			break
		}
	}
	require.Greater(t, policiesIdx, 0, "must have Policies: heading")
	assert.Equal(t, "", lines[policiesIdx-1], "blank line must precede Policies:")
}

// ---------------------------------------------------------------------------
// fetch integration via newSpecs
// ---------------------------------------------------------------------------

func TestStacksSpec_FetchDelegates(t *testing.T) {
	fixedNow := stacksNow
	s := &pkgmodel.Stack{
		Label:       "my-stack",
		Description: "test",
		Policies:    nil,
	}
	c := &fakeClient{stacks: []*pkgmodel.Stack{s}}
	specs := newSpecs(func() time.Time { return fixedNow })
	rows, nags, err := specs[TabStacks].fetch(c, "", true)
	require.NoError(t, err)
	assert.Empty(t, nags)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"my-stack", "test", "none"}, rows[0].cells)
	assert.NotNil(t, rows[0].detail)
}
