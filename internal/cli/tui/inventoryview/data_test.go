// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ---------------------------------------------------------------------------
// fakeClient — reused by all later golden tests in this package.
// ---------------------------------------------------------------------------

// fakeClient is a test double that implements Client.
// Per-entity result and error fields plus recorders for the fromTUI flags
// and query strings are all package-visible so later test files can set them.
type fakeClient struct {
	// per-entity results
	forma    *pkgmodel.Forma
	targets  []*pkgmodel.Target
	stacks   []*pkgmodel.Stack
	policies []apimodel.PolicyInventoryItem

	// summaries and detail-by-ksuid for the lazy-detail path. When summaries is
	// nil, ListResourceSummaries derives them from forma (for backward compat).
	summaries      []pkgmodel.ResourceSummary
	detailsByKsuid map[string]*pkgmodel.Resource

	// per-entity errors
	formaErr    error
	targetsErr  error
	stacksErr   error
	policiesErr error

	// per-entity nags
	formaNags    []string
	targetsNags  []string
	stacksNags   []string
	policiesNags []string

	// recorders
	resourcesQuery             string
	resourcesFromTUI           bool
	targetsQuery               string
	targetsFromTUI             bool
	stacksFromTUI              bool
	policiesFromTUI            bool
	resourceDetailByKsuidCalls int
}

func (f *fakeClient) ExtractResources(query string, fromTUI bool) (*pkgmodel.Forma, []string, error) {
	f.resourcesQuery = query
	f.resourcesFromTUI = fromTUI
	if f.formaErr != nil || f.forma == nil || query == "" {
		return f.forma, f.formaNags, f.formaErr
	}
	// Simulate server-side filtering: keep resources whose fields contain the
	// query as a case-insensitive substring (the real server honours a richer
	// syntax; this is enough to exercise the serverQuery re-fetch path).
	needle := strings.ToLower(query)
	filtered := make([]pkgmodel.Resource, 0, len(f.forma.Resources))
	for _, r := range f.forma.Resources {
		hay := strings.ToLower(r.NativeID + " " + r.Stack + " " + r.Type + " " + r.Label)
		if strings.Contains(hay, needle) {
			filtered = append(filtered, r)
		}
	}
	return &pkgmodel.Forma{Resources: filtered}, f.formaNags, f.formaErr
}

func (f *fakeClient) ExtractTargets(query string, fromTUI bool) ([]*pkgmodel.Target, []string, error) {
	f.targetsQuery = query
	f.targetsFromTUI = fromTUI
	return f.targets, f.targetsNags, f.targetsErr
}

func (f *fakeClient) ExtractStacks(fromTUI bool) ([]*pkgmodel.Stack, []string, error) {
	f.stacksFromTUI = fromTUI
	return f.stacks, f.stacksNags, f.stacksErr
}

func (f *fakeClient) ExtractPolicies(fromTUI bool) ([]apimodel.PolicyInventoryItem, []string, error) {
	f.policiesFromTUI = fromTUI
	return f.policies, f.policiesNags, f.policiesErr
}

// ListResourceSummaries returns the pre-seeded summaries. When summaries is nil
// it derives them from forma.Resources so existing tests that only set forma
// continue to work without modification.
func (f *fakeClient) ListResourceSummaries(query string, fromTUI bool) ([]pkgmodel.ResourceSummary, []string, error) {
	f.resourcesQuery = query
	f.resourcesFromTUI = fromTUI
	if f.formaErr != nil {
		return nil, f.formaNags, f.formaErr
	}
	if f.summaries != nil {
		// Use explicit summaries; apply a simple substring filter if query is set.
		var filtered []pkgmodel.ResourceSummary
		if query == "" {
			filtered = f.summaries
		} else {
			needle := strings.ToLower(query)
			for _, s := range f.summaries {
				hay := strings.ToLower(s.Label + " " + s.Stack + " " + s.Type + " " + s.NativeID)
				if strings.Contains(hay, needle) {
					filtered = append(filtered, s)
				}
			}
		}
		return filtered, f.formaNags, nil
	}
	// Derive summaries from forma for backward compatibility with existing tests.
	if f.forma == nil {
		return nil, f.formaNags, nil
	}
	needle := strings.ToLower(query)
	summaries := make([]pkgmodel.ResourceSummary, 0, len(f.forma.Resources))
	for _, r := range f.forma.Resources {
		if query != "" {
			hay := strings.ToLower(r.NativeID + " " + r.Stack + " " + r.Type + " " + r.Label)
			if !strings.Contains(hay, needle) {
				continue
			}
		}
		summaries = append(summaries, pkgmodel.ResourceSummary{
			Label:    r.Label,
			Stack:    r.Stack,
			Type:     r.Type,
			NativeID: r.NativeID,
			Ksuid:    r.Ksuid,
		})
	}
	return summaries, f.formaNags, nil
}

// ResourceDetailByKsuid returns the resource for the given ksuid from
// detailsByKsuid, or nil if not found or map is nil. Increments the call counter.
func (f *fakeClient) ResourceDetailByKsuid(ksuid string, _ bool) (*pkgmodel.Resource, []string, error) {
	f.resourceDetailByKsuidCalls++
	if f.detailsByKsuid == nil {
		return nil, nil, nil
	}
	r, ok := f.detailsByKsuid[ksuid]
	if !ok {
		return nil, nil, nil
	}
	return r, nil, nil
}

// ---------------------------------------------------------------------------
// Helper: simplifyValue
// ---------------------------------------------------------------------------

func TestSimplifyValue(t *testing.T) {
	// Plain scalar passes through.
	assert.Equal(t, "plain", simplifyValue("plain"))

	// Ordinary object (no "$" markers) is returned unchanged.
	obj := map[string]any{"a": "b"}
	assert.Equal(t, obj, simplifyValue(obj))

	// SetOnce/Clear wrapper unwraps to its value.
	assert.Equal(t, "lifeline-igw", simplifyValue(map[string]any{
		"$strategy": "SetOnce", "$visibility": "Clear", "$value": "lifeline-igw",
	}))

	// Opaque wrapper masks the value.
	assert.Equal(t, opaqueVal{}, simplifyValue(map[string]any{
		"$visibility": "Opaque", "$value": "s3cr3t",
	}))

	// Resolvable ref carries value + "label.property" target.
	got := simplifyValue(map[string]any{
		"$res": true, "$label": "lifeline-vpc", "$property": "VpcId",
		"$stack": "lifeline-fresh", "$type": "AWS::EC2::VPC", "$value": "vpc-0b5",
	})
	assert.Equal(t, refVal{value: "vpc-0b5", target: "lifeline-vpc.VpcId"}, got)

	// $ref-only resolvable strips the formae:// scheme for its target.
	got2 := simplifyValue(map[string]any{"$ref": "formae://abc", "$value": "us-east-1"})
	assert.Equal(t, refVal{value: "us-east-1", target: "abc"}, got2)

	// An opaque $res reference is masked but still names its target: the
	// resolved value is withheld, but which resource property it points at
	// is safe metadata and must not be dropped along with the value.
	gotOpaqueRes := simplifyValue(map[string]any{
		"$res": true, "$label": "lifeline-vpc", "$property": "VpcId", "$value": "vpc-0b5",
		"$visibility": "Opaque",
	})
	assert.Equal(t, refVal{value: opaqueVal{}, target: "lifeline-vpc.VpcId"}, gotOpaqueRes)

	// An opaque $ref reference is masked but still names its target.
	gotOpaqueRef := simplifyValue(map[string]any{
		"$ref": "formae://abc", "$value": "us-east-1", "$visibility": "Opaque",
	})
	assert.Equal(t, refVal{value: opaqueVal{}, target: "abc"}, gotOpaqueRef)

	// A $gen envelope is a masked reference, not a raw JSON blob: the value is
	// withheld like any opaque wrapper, but the generator it names still
	// shows.
	got3 := simplifyValue(map[string]any{
		"$gen": true, "$generator": "2ABcDeFgHiJkLmNoPqRsTuVwXyZ", "$output": "value",
		"$visibility": "Opaque", "$value": "hunter2",
	})
	assert.Equal(t, refVal{value: opaqueVal{}, target: "2ABcDeFgHiJkLmNoPqRsTuVwXyZ"}, got3)
}

func TestFormatScalar(t *testing.T) {
	assert.Equal(t, opaqueMask, formatScalar(opaqueVal{}))
	assert.Equal(t, "vpc-0b5  → lifeline-vpc.VpcId",
		formatScalar(refVal{value: "vpc-0b5", target: "lifeline-vpc.VpcId"}))
	assert.Equal(t, "plain", formatScalar("plain"))
	// Compact form drops the ref target.
	assert.Equal(t, "vpc-0b5",
		formatCompact(refVal{value: "vpc-0b5", target: "lifeline-vpc.VpcId"}))
}

// ---------------------------------------------------------------------------
// Helper: compactKV
// ---------------------------------------------------------------------------

func TestCompactKV_SortedAndUnwrapped(t *testing.T) {
	raw := json.RawMessage(`{"Region":{"$ref":"formae://x","$value":"eu-west-1"},"Account":"123"}`)
	assert.Equal(t, "Account: 123, Region: eu-west-1", compactKV(raw))

	raw2 := json.RawMessage(`{"Zone":"z","Account":"a"}`)
	assert.Equal(t, "Account: a, Zone: z", compactKV(raw2))
}

// ---------------------------------------------------------------------------
// Helper: jsonTree
// ---------------------------------------------------------------------------

func TestJSONTree_NestedSorted(t *testing.T) {
	raw := json.RawMessage(`{"Tags":{"Env":"prod"},"BucketName":"b"}`)
	assert.Equal(t, []string{"BucketName: b", "Tags:", "  Env: prod"}, jsonTree(raw, 0))

	rawArr := json.RawMessage(`{"Subnets":["a","b"]}`)
	assert.Equal(t, []string{"Subnets:", "  - a", "  - b"}, jsonTree(rawArr, 0))
}

// ---------------------------------------------------------------------------
// row.detail
// ---------------------------------------------------------------------------

// TestRowDetail verifies that a row with no detail expander returns nil and
// a row with a detail expander returns lines at the given width.
func TestRowDetail(t *testing.T) {
	r := row{cells: []string{"a"}}
	assert.Nil(t, r.detail, "no detail set")

	r2 := row{
		cells:  []string{"b"},
		detail: func(width int) []string { return []string{"line"} },
	}
	require.NotNil(t, r2.detail)
	assert.Equal(t, []string{"line"}, r2.detail(80))
}

// ---------------------------------------------------------------------------
// fetchCmd
// ---------------------------------------------------------------------------

func TestFetchCmd_DeliversLoadedMsg(t *testing.T) {
	c := &fakeClient{stacks: []*pkgmodel.Stack{{Label: "s"}}}
	specs := newSpecs(func() time.Time { return time.Unix(0, 0) })
	msg := fetchCmd(c, specs, TabStacks, "", true)()
	loaded, ok := msg.(tabLoadedMsg)
	require.True(t, ok)
	assert.Equal(t, TabStacks, loaded.tab)
	assert.True(t, c.stacksFromTUI, "fromTUI must be threaded")
	assert.Len(t, loaded.rows, 1)
}
