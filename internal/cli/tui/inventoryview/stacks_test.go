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

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/datastore"
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

func TestStackRow_Cells_TTLPolicy_AbsoluteExpiresIn23h(t *testing.T) {
	// An absolute deadline is rendered from the instant itself, not from
	// CreatedAt — so a stack of any age shows the same remaining time.
	expiresAt := stacksNow.Add(23 * time.Hour).Format(time.RFC3339)
	ttlJSON := json.RawMessage(`{"Type":"ttl","ExpiresAt":"` + expiresAt + `"}`)
	s := &pkgmodel.Stack{
		Label:     "trial-stack",
		CreatedAt: stacksNow.Add(-100 * time.Hour),
		Policies:  []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "TTL: expires in 23h0m", got.cells[2])
}

func TestStackRow_Cells_TTLPolicy_AbsoluteExpired(t *testing.T) {
	expiresAt := stacksNow.Add(-1 * time.Hour).Format(time.RFC3339)
	ttlJSON := json.RawMessage(`{"Type":"ttl","ExpiresAt":"` + expiresAt + `"}`)
	s := &pkgmodel.Stack{
		Label:     "trial-stack",
		CreatedAt: stacksNow.Add(-2 * time.Hour),
		Policies:  []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "TTL: expired", got.cells[2])
}

func TestStackRow_Cells_TTLPolicy_AbsoluteWithLabel(t *testing.T) {
	expiresAt := stacksNow.Add(23 * time.Hour).Format(time.RFC3339)
	ttlJSON := json.RawMessage(`{"Type":"ttl","Label":"trial","ExpiresAt":"` + expiresAt + `"}`)
	s := &pkgmodel.Stack{
		Label:    "trial-stack",
		Policies: []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "TTL: expires in 23h0m (trial)", got.cells[2])
}

// An unreadable stored deadline must not be rendered as an expiry.
func TestStackRow_Cells_TTLPolicy_AbsoluteMalformed(t *testing.T) {
	ttlJSON := json.RawMessage(`{"Type":"ttl","ExpiresAt":"not-a-timestamp"}`)
	s := &pkgmodel.Stack{
		Label:    "trial-stack",
		Policies: []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "TTL", got.cells[2])
}

// The absolute arm must be selected from the policy form the store actually
// round-trips, not from hand-written JSON. The fixture is built by walking that
// chain — the stored policy_data payload, read back the way a backend reads it,
// then marshalled as the inventory response carries it — so a change to the
// key the model writes breaks this test instead of silently falling through to
// the relative arm. It covers store shape → read → marshal → render and stops
// short of the HTTP hop, which marshals and decodes the same type on both sides.
func TestStackRow_Cells_TTLPolicy_AbsoluteThroughStoredForm(t *testing.T) {
	policy := &pkgmodel.TTLPolicy{
		Type:         "ttl",
		Label:        "trial-expiry",
		ExpiresAt:    stacksNow.Add(23 * time.Hour),
		OnDependents: "abort",
	}

	storedJSON, err := json.Marshal(datastore.TTLPolicyData(policy))
	require.NoError(t, err)
	stored, err := datastore.TTLPolicyFromData(policy.Label, string(storedJSON), "stack-1")
	require.NoError(t, err)
	policyJSON, err := json.Marshal(stored)
	require.NoError(t, err)

	s := &pkgmodel.Stack{
		Label: "trial-stack",
		// Long past, so a mis-selected arm renders "0s (expired)" rather than
		// merely a different remaining time.
		CreatedAt: stacksNow.Add(-100 * time.Hour),
		Policies:  []json.RawMessage{policyJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "TTL: expires in 23h0m (trial-expiry)", got.cells[2])
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
// stackRow: absolute timestamps beyond the 24h horizon
//
// Past 24h the summary drops the relative form and prints an instant. That
// instant is rendered in UTC with a trailing Z so it reads the same for every
// operator and cannot be mistaken for local wall-clock.
// ---------------------------------------------------------------------------

// pinLocalZone points time.Local at a fixed non-UTC zone for the duration of a
// test, so an assertion on a rendered instant distinguishes UTC from local
// time. On a UTC host the two are identical and such an assertion would hold
// either way.
//
// time.Local is process-global, so this is only safe while no test in this
// package calls t.Parallel(). Adding t.Parallel() here must be a conscious
// decision that accounts for these tests.
func pinLocalZone(t *testing.T) {
	t.Helper()
	saved := time.Local
	time.Local = time.FixedZone("PDT", -7*60*60)
	t.Cleanup(func() { time.Local = saved })
}

// The deadline must render as the stored UTC instant no matter where the
// operator sits. Reading a local wall-clock time off this cell and passing it
// back as an absolute deadline would move the deadline by the operator's
// offset.
func TestStackRow_Cells_TTLPolicy_AbsoluteBeyond24h_RendersUTCUnderLocalZone(t *testing.T) {
	pinLocalZone(t)

	expiresAt := stacksNow.Add(9 * 24 * time.Hour)
	ttlJSON, err := json.Marshal(map[string]any{
		"Type":      "ttl",
		"Label":     "trial-expiry",
		"ExpiresAt": expiresAt.Format(time.RFC3339),
	})
	require.NoError(t, err)

	s := &pkgmodel.Stack{
		Label:     "trial-stack",
		CreatedAt: stacksNow.Add(-100 * time.Hour),
		Policies:  []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "TTL: expires Jul 26 12:00Z (trial-expiry)", got.cells[2])
}

// Documents the rendered format independently of the zone guard. This one holds
// on a UTC host regardless of which zone the formatter uses, so it pins the
// shape rather than guarding the conversion.
func TestStackRow_Cells_TTLPolicy_AbsoluteBeyond24h_CarriesUTCQualifier(t *testing.T) {
	expiresAt := stacksNow.Add(9 * 24 * time.Hour)
	ttlJSON, err := json.Marshal(map[string]any{
		"Type":      "ttl",
		"ExpiresAt": expiresAt.Format(time.RFC3339),
	})
	require.NoError(t, err)

	s := &pkgmodel.Stack{
		Label:     "trial-stack",
		CreatedAt: stacksNow.Add(-100 * time.Hour),
		Policies:  []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "TTL: expires Jul 26 12:00Z", got.cells[2])
}

// A duration-based TTL reaches the same formatter through createdAt+duration,
// so it carries the qualifier too.
func TestStackRow_Cells_TTLPolicy_DurationBeyond24h_RendersUTC(t *testing.T) {
	pinLocalZone(t)

	ttlJSON := json.RawMessage(`{"Type":"ttl","TTLSeconds":604800}`)
	s := &pkgmodel.Stack{
		Label:     "week-stack",
		CreatedAt: stacksNow,
		Policies:  []json.RawMessage{ttlJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "TTL: 7d, expires Jul 24 12:00Z", got.cells[2])
}

// The auto-reconcile last-run timestamp shares the cell with the TTL deadline
// and the same absolute-time branch, so it is qualified the same way.
func TestStackRow_Cells_AutoReconcile_LastRunBeyond24h_RendersUTC(t *testing.T) {
	pinLocalZone(t)

	lastRun := stacksNow.Add(-30 * time.Hour)
	arJSON, err := json.Marshal(map[string]any{
		"Type":            "auto-reconcile",
		"IntervalSeconds": float64(300),
		"LastReconcileAt": lastRun.Format(time.RFC3339),
	})
	require.NoError(t, err)

	s := &pkgmodel.Stack{
		Label:    "ar-stack",
		Policies: []json.RawMessage{arJSON},
	}
	got := stackRow(s, stacksNow)
	assert.Equal(t, "Auto-reconcile: every 5m, last Jul 16 06:00Z", got.cells[2])
}

// Pins which side of the 24h horizon each boundary value falls on, so a later
// refactor cannot move the switch between the relative and absolute forms
// without a test noticing.
func TestStackRow_Cells_TTLPolicy_Absolute24hBoundary(t *testing.T) {
	pinLocalZone(t)

	tests := []struct {
		name      string
		remaining time.Duration
		want      string
	}{
		{"exactly 24h renders the instant", 24 * time.Hour, "TTL: expires Jul 18 12:00Z"},
		{"one minute under renders relative", 23*time.Hour + 59*time.Minute, "TTL: expires in 23h59m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ttlJSON, err := json.Marshal(map[string]any{
				"Type":      "ttl",
				"ExpiresAt": stacksNow.Add(tt.remaining).Format(time.RFC3339),
			})
			require.NoError(t, err)

			s := &pkgmodel.Stack{
				Label:     "boundary-stack",
				CreatedAt: stacksNow.Add(-100 * time.Hour),
				Policies:  []json.RawMessage{ttlJSON},
			}
			got := stackRow(s, stacksNow)
			assert.Equal(t, tt.want, got.cells[2])
		})
	}
}

// The Policies column is a fixed width and hard-truncates from the right, so
// asserting on the cell alone does not prove an operator ever sees the
// qualifier. This goes through the entry point both the TUI tab and the
// non-TTY table share, at the real column width, and pins the budget the
// format depends on: a longer format that pushes the Z past the column fails
// here rather than in production.
func TestRenderStacks_AbsoluteDeadlineKeepsUTCQualifierAtColumnWidth(t *testing.T) {
	pinLocalZone(t)

	expiresAt := stacksNow.Add(9 * 24 * time.Hour)
	ttlJSON, err := json.Marshal(map[string]any{
		"Type":      "ttl",
		"Label":     "trial-expiry",
		"ExpiresAt": expiresAt.Format(time.RFC3339),
	})
	require.NoError(t, err)

	s := &pkgmodel.Stack{
		Label:       "trial-stack",
		Description: "A hosted trial installation",
		CreatedAt:   stacksNow.Add(-100 * time.Hour),
		Policies:    []json.RawMessage{ttlJSON},
	}

	out := RenderStacks(theme.New("quiet"), []*pkgmodel.Stack{s}, stacksNow, 10, 120)
	assert.Contains(t, out, "TTL: expires Jul 26 12:00Z",
		"the qualifier must survive truncation of the Policies column")
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
