// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package inventory

import (
	"bytes"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ansiRe strips well-formed ANSI CSI sequences.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// fixedNow is a deterministic timestamp for golden tests (stacks' TTL expiry).
var fixedNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// ── Fixtures ─────────────────────────────────────────────────────────────────

func makeResourcesFixture() *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{
				Label:    "prod-bucket",
				Type:     "AWS::S3::Bucket",
				Stack:    "prod",
				Target:   "aws-us-east-1",
				NativeID: "arn:aws:s3:::prod-bucket",
				Managed:  true,
			},
			{
				Label:    "orphan-queue",
				Type:     "AWS::SQS::Queue",
				Stack:    "unmanaged",
				Target:   "aws-us-east-1",
				NativeID: "https://sqs.us-east-1.amazonaws.com/123/orphan-queue",
				Managed:  false,
			},
			{
				Label:    "staging-fn",
				Type:     "AWS::Lambda::Function",
				Stack:    "staging",
				Target:   "aws-eu-west-1",
				NativeID: "arn:aws:lambda:eu-west-1:123:function:staging-fn",
				Managed:  true,
			},
		},
	}
}

func makeTargetsFixture() []*pkgmodel.Target {
	return []*pkgmodel.Target{
		{
			Label:        "aws-us-east-1",
			Namespace:    "AWS",
			Discoverable: true,
			Version:      2,
		},
		{
			Label:        "aws-eu-west-1",
			Namespace:    "AWS",
			Discoverable: false,
			Version:      1,
		},
		{
			Label:        "tailscale-main",
			Namespace:    "tailscale",
			Discoverable: false,
			Version:      1,
		},
	}
}

func makeStacksFixture() []*pkgmodel.Stack {
	ttlPolicy, _ := json.Marshal(map[string]any{
		"Type":       "ttl",
		"TTLSeconds": float64(7200), // 2 h — expires 2026-01-15 14:00 UTC, fixedNow is 12:00 → in 2h
	})
	return []*pkgmodel.Stack{
		{
			Label:       "prod",
			Description: "Production stack",
			CreatedAt:   time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC), // created 2h before fixedNow → TTL expires exactly at 12:00
			Policies:    []json.RawMessage{ttlPolicy},
		},
		{
			Label:       "staging",
			Description: "Staging stack",
			CreatedAt:   time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			Label:       "expired-stack",
			Description: "Stack with expired TTL",
			CreatedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			Policies:    []json.RawMessage{ttlPolicy},
		},
	}
}

func makePoliciesFixture() []apimodel.PolicyInventoryItem {
	ttlPolicy, _ := json.Marshal(map[string]any{
		"Type":       "ttl",
		"TTLSeconds": float64(86400),
	})
	autoPolicy, _ := json.Marshal(map[string]any{
		"Type":            "auto-reconcile",
		"IntervalSeconds": float64(3600),
	})
	return []apimodel.PolicyInventoryItem{
		{
			Label:          "global-ttl",
			Type:           "ttl",
			Config:         ttlPolicy,
			AttachedStacks: []string{"prod", "staging"},
		},
		{
			Label:          "global-recon",
			Type:           "auto-reconcile",
			Config:         autoPolicy,
			AttachedStacks: []string{"staging"},
		},
		{
			Label:          "standalone-policy",
			Type:           "ttl",
			Config:         ttlPolicy,
			AttachedStacks: nil,
		},
	}
}

// ── Machine golden pins ────────────────────────────────────────────────────

// TestMachineGolden_Resources pins the exact bytes produced by
// MachineReadablePrinter over the resources fixture (the inventory struct).
func TestMachineGolden_Resources(t *testing.T) {
	forma := makeResourcesFixture()
	inv := &inventory{
		Resources: forma.Resources,
		Targets:   forma.Targets,
	}
	var buf bytes.Buffer
	p := printer.NewMachineReadablePrinter[inventory](&buf, "json")
	err := p.Print(inv)
	require.NoError(t, err)
	tuitest.RequireGolden(t, buf.Bytes())
}

// TestMachineGolden_Targets pins the exact bytes for the targets machine path.
func TestMachineGolden_Targets(t *testing.T) {
	targets := makeTargetsFixture()
	var buf bytes.Buffer
	p := printer.NewMachineReadablePrinter[[]*pkgmodel.Target](&buf, "json")
	err := p.Print(&targets)
	require.NoError(t, err)
	tuitest.RequireGolden(t, buf.Bytes())
}

// TestMachineGolden_Stacks pins the exact bytes for the stacks machine path.
func TestMachineGolden_Stacks(t *testing.T) {
	stacks := makeStacksFixture()
	var buf bytes.Buffer
	p := printer.NewMachineReadablePrinter[[]*pkgmodel.Stack](&buf, "json")
	err := p.Print(&stacks)
	require.NoError(t, err)
	tuitest.RequireGolden(t, buf.Bytes())
}

// TestMachineGolden_Policies pins the exact bytes for the policies machine path.
func TestMachineGolden_Policies(t *testing.T) {
	policies := makePoliciesFixture()
	var buf bytes.Buffer
	p := printer.NewMachineReadablePrinter[[]apimodel.PolicyInventoryItem](&buf, "json")
	err := p.Print(&policies)
	require.NoError(t, err)
	tuitest.RequireGolden(t, buf.Bytes())
}

// ── renderInventoryResources golden tests ─────────────────────────────────

func TestRenderInventoryResources_Styled(t *testing.T) {
	th := theme.New("formae")
	forma := makeResourcesFixture()
	out := renderInventoryResources(th, forma, 0 /*unlimited*/, 120)
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderInventoryResources_Piped(t *testing.T) {
	th := theme.New("formae")
	forma := makeResourcesFixture()
	out := renderInventoryResources(th, forma, 0, 120)
	plain := stripANSI(out)

	// Must contain no raw ANSI escapes (lipgloss strips when not a TTY;
	// in tests we pin TrueColor, so we strip manually and assert plain content).
	assert.NotContains(t, plain, "\x1b[", "piped output must not contain ANSI escapes after stripping")
	assert.Contains(t, plain, "prod-bucket", "must contain managed resource label")
	assert.Contains(t, plain, "unmanaged", "must contain unmanaged stack marker")
	assert.Contains(t, plain, "AWS::S3::Bucket", "must contain resource type")
	assert.Contains(t, plain, "Showing 3 of 3 total resources", "must contain status line")
}

func TestRenderInventoryResources_MaxResultsCapping(t *testing.T) {
	th := theme.New("formae")
	forma := makeResourcesFixture()
	// Cap to 1 of 3 rows.
	out := renderInventoryResources(th, forma, 1 /*maxResults*/, 120)
	plain := stripANSI(out)

	assert.Contains(t, plain, "Showing 1 of 3 total resources", "must show capped count")
	assert.Contains(t, plain, "more results not shown", "must show more-results notice")
	// prod-bucket is the first row, others must be absent.
	assert.Contains(t, plain, "prod-bucket", "first row must appear")
	assert.NotContains(t, plain, "staging-fn", "third row must not appear when capped to 1")
}

// ── renderInventoryTargets golden tests ───────────────────────────────────

func TestRenderInventoryTargets_Styled(t *testing.T) {
	th := theme.New("formae")
	targets := makeTargetsFixture()
	out := renderInventoryTargets(th, targets, 0, 120)
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderInventoryTargets_Piped(t *testing.T) {
	th := theme.New("formae")
	targets := makeTargetsFixture()
	out := renderInventoryTargets(th, targets, 0, 120)
	plain := stripANSI(out)

	assert.Contains(t, plain, "aws-us-east-1", "must contain first target label")
	assert.Contains(t, plain, "aws-eu-west-1", "must contain second target label")
	assert.Contains(t, plain, "tailscale-main", "must contain tailscale target")
	assert.Contains(t, plain, "Showing 3 of 3 total targets", "must contain status line")
}

func TestRenderInventoryTargets_MaxResultsCapping(t *testing.T) {
	th := theme.New("formae")
	targets := makeTargetsFixture()
	out := renderInventoryTargets(th, targets, 2 /*maxResults*/, 120)
	plain := stripANSI(out)

	assert.Contains(t, plain, "Showing 2 of 3 total targets")
	assert.Contains(t, plain, "more results not shown")
}

// ── renderInventoryStacks golden tests ────────────────────────────────────

func TestRenderInventoryStacks_Styled(t *testing.T) {
	th := theme.New("formae")
	stacks := makeStacksFixture()
	out := renderInventoryStacks(th, stacks, fixedNow, 0, 120)
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderInventoryStacks_Piped(t *testing.T) {
	th := theme.New("formae")
	stacks := makeStacksFixture()
	out := renderInventoryStacks(th, stacks, fixedNow, 0, 120)
	plain := stripANSI(out)

	assert.Contains(t, plain, "prod", "must contain prod stack")
	assert.Contains(t, plain, "staging", "must contain staging stack")
	assert.Contains(t, plain, "TTL:", "must contain TTL policy summary")
	assert.Contains(t, plain, "expired", "must show expired TTL")
	assert.Contains(t, plain, "Showing 3 of 3 total stacks", "must contain status line")
}

func TestRenderInventoryStacks_MaxResultsCapping(t *testing.T) {
	th := theme.New("formae")
	stacks := makeStacksFixture()
	out := renderInventoryStacks(th, stacks, fixedNow, 1 /*maxResults*/, 120)
	plain := stripANSI(out)

	assert.Contains(t, plain, "Showing 1 of 3 total stacks")
	assert.Contains(t, plain, "more results not shown")
}

// ── renderInventoryPolicies golden tests ──────────────────────────────────

func TestRenderInventoryPolicies_Styled(t *testing.T) {
	th := theme.New("formae")
	policies := makePoliciesFixture()
	out := renderInventoryPolicies(th, policies, 0, 120)
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderInventoryPolicies_Piped(t *testing.T) {
	th := theme.New("formae")
	policies := makePoliciesFixture()
	out := renderInventoryPolicies(th, policies, 0, 120)
	plain := stripANSI(out)

	assert.Contains(t, plain, "global-ttl", "must contain first policy label")
	assert.Contains(t, plain, "global-recon", "must contain second policy label")
	assert.Contains(t, plain, "standalone-policy", "must contain third policy label")
	assert.Contains(t, plain, "Showing 3 of 3 total policies", "must contain status line")
}

func TestRenderInventoryPolicies_MaxResultsCapping(t *testing.T) {
	th := theme.New("formae")
	policies := makePoliciesFixture()
	out := renderInventoryPolicies(th, policies, 2 /*maxResults*/, 120)
	plain := stripANSI(out)

	assert.Contains(t, plain, "Showing 2 of 3 total policies")
	assert.Contains(t, plain, "more results not shown")
}
