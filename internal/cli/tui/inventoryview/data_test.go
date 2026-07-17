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
	resourcesQuery   string
	resourcesFromTUI bool
	targetsQuery     string
	targetsFromTUI   bool
	stacksFromTUI    bool
	policiesFromTUI  bool
}

func (f *fakeClient) ExtractResources(query string, fromTUI bool) (*pkgmodel.Forma, []string, error) {
	f.resourcesQuery = query
	f.resourcesFromTUI = fromTUI
	return f.forma, f.formaNags, f.formaErr
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

// ---------------------------------------------------------------------------
// Helper: unwrapRefValue
// ---------------------------------------------------------------------------

func TestUnwrapRefValue(t *testing.T) {
	v := unwrapRefValue(map[string]any{"$ref": "formae://abc", "$value": "us-east-1"})
	assert.Equal(t, "us-east-1", v)
	assert.Equal(t, "plain", unwrapRefValue("plain"))
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
