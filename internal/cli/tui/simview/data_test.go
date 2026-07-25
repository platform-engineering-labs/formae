// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// ---------------------------------------------------------------------------
// Replace pairing
// ---------------------------------------------------------------------------

// TestReplacePairing verifies that two ResourceUpdates sharing a GroupID
// (one delete, one create with CreateOnlyPatch) merge into a single simRow
// with op==opReplace, res==create half, delRes==delete half, and label/type
// taken from the create half.
func TestReplacePairing(t *testing.T) {
	deleteHalf := apimodel.ResourceUpdate{
		ResourceID:      "id-delete",
		ResourceLabel:   "web-server",
		ResourceType:    "AWS::EC2::Instance",
		StackName:       "production",
		Operation:       apimodel.OperationDelete,
		GroupID:         "group-1",
		CreateOnlyPatch: []byte(`[{"op":"replace","path":"/ImageId","value":"ami-new"}]`),
	}
	createHalf := apimodel.ResourceUpdate{
		ResourceID:    "id-create",
		ResourceLabel: "web-server",
		ResourceType:  "AWS::EC2::Instance",
		StackName:     "production",
		Operation:     apimodel.OperationCreate,
		GroupID:       "group-1",
	}

	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{deleteHalf, createHalf},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1, "expected exactly one group")
	require.Equal(t, kindResource, groups[0].kind)
	require.Len(t, groups[0].rows, 1, "delete+create pair should merge into one row")

	row := groups[0].rows[0]
	assert.Equal(t, opReplace, row.op)
	assert.Equal(t, "web-server", row.label)
	assert.Equal(t, "AWS::EC2::Instance", row.typ)
	assert.Equal(t, "production", row.stack)
	require.NotNil(t, row.res, "res should be the create half")
	assert.Equal(t, "id-create", row.res.ResourceID)
	require.NotNil(t, row.delRes, "delRes should be the delete half")
	assert.Equal(t, "id-delete", row.delRes.ResourceID)
}

// TestReplacePairingReverseOrder verifies pairing works regardless of which
// half appears first in the slice.
func TestReplacePairingReverseOrder(t *testing.T) {
	createHalf := apimodel.ResourceUpdate{
		ResourceID:    "id-create",
		ResourceLabel: "db",
		ResourceType:  "AWS::RDS::DBInstance",
		StackName:     "staging",
		Operation:     apimodel.OperationCreate,
		GroupID:       "grp-2",
	}
	deleteHalf := apimodel.ResourceUpdate{
		ResourceID:      "id-delete",
		ResourceLabel:   "db",
		ResourceType:    "AWS::RDS::DBInstance",
		StackName:       "staging",
		Operation:       apimodel.OperationDelete,
		GroupID:         "grp-2",
		CreateOnlyPatch: []byte(`[{"op":"replace","path":"/EngineVersion","value":"15"}]`),
	}

	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{createHalf, deleteHalf},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].rows, 1)
	row := groups[0].rows[0]
	assert.Equal(t, opReplace, row.op)
	assert.Equal(t, "id-create", row.res.ResourceID)
	assert.Equal(t, "id-delete", row.delRes.ResourceID)
}

// ---------------------------------------------------------------------------
// Grouping + order
// ---------------------------------------------------------------------------

// TestGroupingOrder verifies the four group kinds appear in the correct order
// (targets, stacks, policies, resources) and empty groups are omitted.
func TestGroupingOrder(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "r1", ResourceLabel: "bucket", ResourceType: "AWS::S3::Bucket", StackName: "default", Operation: apimodel.OperationCreate},
		},
		TargetUpdates: []apimodel.TargetUpdate{
			{TargetLabel: "prod", Operation: "create"},
		},
		StackUpdates: []apimodel.StackUpdate{
			{StackLabel: "default", Operation: "create"},
		},
		PolicyUpdates: []apimodel.PolicyUpdate{
			{PolicyLabel: "ttl-policy", PolicyType: "ttl", StackLabel: "default", Operation: "create"},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 4)
	assert.Equal(t, kindTarget, groups[0].kind)
	assert.Equal(t, "Targets", groups[0].title)
	assert.Equal(t, kindStack, groups[1].kind)
	assert.Equal(t, "Stacks", groups[1].title)
	assert.Equal(t, kindPolicy, groups[2].kind)
	assert.Equal(t, "Policies", groups[2].title)
	assert.Equal(t, kindResource, groups[3].kind)
	assert.Equal(t, "Resources", groups[3].title)
}

// TestGroupingEmptyGroupsOmitted verifies that groups with no rows are not
// included in the output.
func TestGroupingEmptyGroupsOmitted(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "r1", ResourceLabel: "bucket", ResourceType: "AWS::S3::Bucket", StackName: "default", Operation: apimodel.OperationCreate},
		},
		// No TargetUpdates, StackUpdates, PolicyUpdates
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	assert.Equal(t, kindResource, groups[0].kind)
}

// TestGroupingAllEmpty verifies that a command with no updates returns no groups.
func TestGroupingAllEmpty(t *testing.T) {
	cmd := &apimodel.Command{}
	groups := buildSimGroups(cmd)
	assert.Empty(t, groups)
}

// ---------------------------------------------------------------------------
// Default sort: destructive-first within a group
// ---------------------------------------------------------------------------

// TestDefaultSort verifies that within a resource group, rows are ordered
// delete → replace → update → detach → create → keep after buildSimGroups.
func TestDefaultSort(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			// Deliberately shuffled: keep, create, update, delete
			{ResourceID: "rk", ResourceLabel: "keep-me", ResourceType: "AWS::S3::Bucket", StackName: "s", Operation: apimodel.OperationRead},
			{ResourceID: "rc", ResourceLabel: "new-bucket", ResourceType: "AWS::S3::Bucket", StackName: "s", Operation: apimodel.OperationCreate},
			{ResourceID: "ru", ResourceLabel: "update-me", ResourceType: "AWS::S3::Bucket", StackName: "s", Operation: apimodel.OperationUpdate},
			{ResourceID: "rd", ResourceLabel: "delete-me", ResourceType: "AWS::S3::Bucket", StackName: "s", Operation: apimodel.OperationDelete},
		},
	}

	groups := buildSimGroups(cmd)
	// "read" is excluded, so 3 rows: delete, update, create
	require.Len(t, groups, 1)
	rows := groups[0].rows
	require.Len(t, rows, 3, "read should be excluded; 3 rows remain")

	assert.Equal(t, opDelete, rows[0].op, "first row should be delete")
	assert.Equal(t, opUpdate, rows[1].op, "second row should be update")
	assert.Equal(t, opCreate, rows[2].op, "third row should be create")
}

// TestDefaultSortFullOrder exercises all six opKinds to verify full sort order.
func TestDefaultSortFullOrder(t *testing.T) {
	// We need to produce all six ops. Resources produce delete/replace/update/create.
	// "keep" maps from policy "skip". "detach" maps from policy "detach".
	// Use a mix of resource + policy updates to cover all.
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			// replace pair
			{
				ResourceID: "rr-del", ResourceLabel: "replaced", ResourceType: "T", StackName: "s",
				Operation: apimodel.OperationDelete, GroupID: "grp-r",
				CreateOnlyPatch: []byte(`[{"op":"replace","path":"/Foo","value":"bar"}]`),
			},
			{
				ResourceID: "rr-cre", ResourceLabel: "replaced", ResourceType: "T", StackName: "s",
				Operation: apimodel.OperationCreate, GroupID: "grp-r",
			},
			// create
			{ResourceID: "rc", ResourceLabel: "new", ResourceType: "T", StackName: "s", Operation: apimodel.OperationCreate},
			// update
			{ResourceID: "ru", ResourceLabel: "updated", ResourceType: "T", StackName: "s", Operation: apimodel.OperationUpdate},
			// delete
			{ResourceID: "rd", ResourceLabel: "deleted", ResourceType: "T", StackName: "s", Operation: apimodel.OperationDelete},
		},
		PolicyUpdates: []apimodel.PolicyUpdate{
			// skip → opKeep
			{PolicyLabel: "my-policy", PolicyType: "ttl", StackLabel: "s", Operation: "skip"},
			// detach → opDetach
			{PolicyLabel: "other-policy", PolicyType: "ttl", StackLabel: "s", Operation: "detach"},
		},
	}

	groups := buildSimGroups(cmd)
	// Two groups: policies (keep, detach) and resources (delete, replace, update, create)
	require.Len(t, groups, 2)

	// Policies group
	policyGroup := groups[0]
	assert.Equal(t, kindPolicy, policyGroup.kind)
	require.Len(t, policyGroup.rows, 2)
	// detach < keep in destructive-first order
	assert.Equal(t, opDetach, policyGroup.rows[0].op, "detach should sort before keep")
	assert.Equal(t, opKeep, policyGroup.rows[1].op, "keep should sort after detach")

	// Resources group
	resGroup := groups[1]
	assert.Equal(t, kindResource, resGroup.kind)
	require.Len(t, resGroup.rows, 4)
	assert.Equal(t, opDelete, resGroup.rows[0].op)
	assert.Equal(t, opReplace, resGroup.rows[1].op)
	assert.Equal(t, opUpdate, resGroup.rows[2].op)
	assert.Equal(t, opCreate, resGroup.rows[3].op)
}

// ---------------------------------------------------------------------------
// Key stability
// ---------------------------------------------------------------------------

// TestKeyStability verifies the row key is "<kind>/<stack>/<label>" and is
// stable regardless of the order updates arrive.
func TestKeyStability(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "r1", ResourceLabel: "web-server", ResourceType: "AWS::EC2::Instance", StackName: "production", Operation: apimodel.OperationCreate},
		},
		TargetUpdates: []apimodel.TargetUpdate{
			{TargetLabel: "us-east-1", Operation: "create"},
		},
		StackUpdates: []apimodel.StackUpdate{
			{StackLabel: "my-stack", Operation: "create"},
		},
		PolicyUpdates: []apimodel.PolicyUpdate{
			{PolicyLabel: "ttl-7d", PolicyType: "ttl", StackLabel: "my-stack", Operation: "create"},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 4)

	// targets: key = "target/<label>" (no stack for targets)
	assert.Equal(t, "target/us-east-1", groups[0].rows[0].key)
	// stacks: key = "stack/<label>"
	assert.Equal(t, "stack/my-stack", groups[1].rows[0].key)
	// policies: key = "policy/<stack>/<label>"
	assert.Equal(t, "policy/my-stack/ttl-7d", groups[2].rows[0].key)
	// resources: key = "resource/<stack>/<label>"
	assert.Equal(t, "resource/production/web-server", groups[3].rows[0].key)
}

// TestKeyStabilityReplace verifies replace rows use the create half's stack/label.
func TestKeyStabilityReplace(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceID: "del", ResourceLabel: "web-server", ResourceType: "T", StackName: "production",
				Operation: apimodel.OperationDelete, GroupID: "g1",
				CreateOnlyPatch: []byte(`[{"op":"replace","path":"/Foo","value":"bar"}]`),
			},
			{
				ResourceID: "cre", ResourceLabel: "web-server", ResourceType: "T", StackName: "production",
				Operation: apimodel.OperationCreate, GroupID: "g1",
			},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	assert.Equal(t, "resource/production/web-server", groups[0].rows[0].key)
}

// ---------------------------------------------------------------------------
// Cascade
// ---------------------------------------------------------------------------

// TestCascadeFields verifies IsCascade and CascadeSource populate the row.
func TestCascadeFields(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceID:    "r1",
				ResourceLabel: "child",
				ResourceType:  "AWS::EC2::Instance",
				StackName:     "prod",
				Operation:     apimodel.OperationDelete,
				IsCascade:     true,
				CascadeSource: "parent-db",
			},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].rows, 1)
	row := groups[0].rows[0]
	assert.True(t, row.cascade, "cascade should be true")
	assert.Equal(t, "parent-db", row.cascadeSrc)
}

// TestCascadeNotSet verifies cascade fields are false/empty when not set.
func TestCascadeNotSet(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "r1", ResourceLabel: "bucket", ResourceType: "AWS::S3::Bucket", StackName: "s", Operation: apimodel.OperationCreate},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	row := groups[0].rows[0]
	assert.False(t, row.cascade)
	assert.Empty(t, row.cascadeSrc)
}

// ---------------------------------------------------------------------------
// Policy keep/detach
// ---------------------------------------------------------------------------

// TestPolicySkipMapsToKeep verifies "skip" operation → opKeep.
func TestPolicySkipMapsToKeep(t *testing.T) {
	cmd := &apimodel.Command{
		PolicyUpdates: []apimodel.PolicyUpdate{
			{
				PolicyLabel:       "my-ttl",
				PolicyType:        "ttl",
				StackLabel:        "prod",
				Operation:         "skip",
				ReferencingStacks: []string{"stack-a", "stack-b"},
			},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].rows, 1)
	row := groups[0].rows[0]
	assert.Equal(t, opKeep, row.op)
	assert.NotNil(t, row.policy)
	// Detail should mention the referencing stacks
	assert.Contains(t, row.detail, "stack-a")
	assert.Contains(t, row.detail, "stack-b")
}

// TestPolicyDetachMapsToDetach verifies "detach" operation → opDetach.
func TestPolicyDetachMapsToDetach(t *testing.T) {
	cmd := &apimodel.Command{
		PolicyUpdates: []apimodel.PolicyUpdate{
			{PolicyLabel: "my-ttl", PolicyType: "ttl", StackLabel: "prod", Operation: "detach"},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].rows, 1)
	row := groups[0].rows[0]
	assert.Equal(t, opDetach, row.op)
}

// TestPolicySkipNoReferencingStacks verifies keep rows with no referencing stacks
// have an empty detail.
func TestPolicySkipNoReferencingStacks(t *testing.T) {
	cmd := &apimodel.Command{
		PolicyUpdates: []apimodel.PolicyUpdate{
			{PolicyLabel: "my-ttl", PolicyType: "ttl", StackLabel: "prod", Operation: "skip"},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	row := groups[0].rows[0]
	assert.Equal(t, opKeep, row.op)
	assert.Empty(t, row.detail)
}

// ---------------------------------------------------------------------------
// Target update detail
// ---------------------------------------------------------------------------

// TestTargetUpdateDetailDiscoverable verifies a target update with no config
// diffs and Discoverable=true generates "to discoverable" detail.
func TestTargetUpdateDetailDiscoverable(t *testing.T) {
	cmd := &apimodel.Command{
		TargetUpdates: []apimodel.TargetUpdate{
			{TargetLabel: "us-east-1", Operation: "update", Discoverable: true},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	row := groups[0].rows[0]
	assert.Equal(t, opUpdate, row.op)
	assert.Equal(t, "to discoverable", row.detail)
}

// TestTargetUpdateDetailNotDiscoverable verifies a target update with no config
// diffs and Discoverable=false generates "to not discoverable" detail.
func TestTargetUpdateDetailNotDiscoverable(t *testing.T) {
	cmd := &apimodel.Command{
		TargetUpdates: []apimodel.TargetUpdate{
			{TargetLabel: "us-west-2", Operation: "update", Discoverable: false},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	row := groups[0].rows[0]
	assert.Equal(t, opUpdate, row.op)
	assert.Equal(t, "to not discoverable", row.detail)
}

// TestTargetUpdateDetailWithConfigDiffs verifies that when there are config diffs
// the detail is left empty (config diff shown elsewhere).
func TestTargetUpdateDetailWithConfigDiffs(t *testing.T) {
	cmd := &apimodel.Command{
		TargetUpdates: []apimodel.TargetUpdate{
			{
				TargetLabel:    "eu-central-1",
				Operation:      "update",
				Discoverable:   true,
				ExistingConfig: []byte(`{"region":"eu-central-1"}`),
				DesiredConfig:  []byte(`{"region":"eu-west-1"}`),
			},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	row := groups[0].rows[0]
	assert.Equal(t, opUpdate, row.op)
	assert.Empty(t, row.detail, "config diffs present: detail should be empty")
}

// ---------------------------------------------------------------------------
// Read operation exclusion
// ---------------------------------------------------------------------------

// TestReadOperationExcluded verifies that ResourceUpdates with Operation=="read"
// are excluded from the output.
func TestReadOperationExcluded(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "r1", ResourceLabel: "synced", ResourceType: "T", StackName: "s", Operation: apimodel.OperationRead},
			{ResourceID: "r2", ResourceLabel: "created", ResourceType: "T", StackName: "s", Operation: apimodel.OperationCreate},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].rows, 1, "only the non-read update should appear")
	assert.Equal(t, "created", groups[0].rows[0].label)
}

// TestOnlyReadOperations verifies that a command with only read updates
// produces no groups.
func TestOnlyReadOperations(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "r1", ResourceLabel: "synced", ResourceType: "T", StackName: "s", Operation: apimodel.OperationRead},
		},
	}
	groups := buildSimGroups(cmd)
	assert.Empty(t, groups)
}

// ---------------------------------------------------------------------------
// opCounts
// ---------------------------------------------------------------------------

// TestOpCounts verifies opCounts sums per opKind across all groups.
func TestOpCounts(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "r1", ResourceLabel: "del", ResourceType: "T", StackName: "s", Operation: apimodel.OperationDelete},
			{ResourceID: "r2", ResourceLabel: "cre", ResourceType: "T", StackName: "s", Operation: apimodel.OperationCreate},
			{ResourceID: "r3", ResourceLabel: "upd", ResourceType: "T", StackName: "s", Operation: apimodel.OperationUpdate},
		},
		PolicyUpdates: []apimodel.PolicyUpdate{
			{PolicyLabel: "p1", PolicyType: "ttl", StackLabel: "s", Operation: "skip"},
		},
	}

	groups := buildSimGroups(cmd)
	counts := opCounts(groups)

	assert.Equal(t, 1, counts[opDelete])
	assert.Equal(t, 1, counts[opCreate])
	assert.Equal(t, 1, counts[opUpdate])
	assert.Equal(t, 1, counts[opKeep])
	assert.Equal(t, 0, counts[opReplace])
	assert.Equal(t, 0, counts[opDetach])
}

// TestOpCountsIncludesReplace verifies replace pairs count as opReplace.
func TestOpCountsIncludesReplace(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceID: "del", ResourceLabel: "r", ResourceType: "T", StackName: "s",
				Operation: apimodel.OperationDelete, GroupID: "g1",
				CreateOnlyPatch: []byte(`[{"op":"replace","path":"/Foo","value":"bar"}]`),
			},
			{
				ResourceID: "cre", ResourceLabel: "r", ResourceType: "T", StackName: "s",
				Operation: apimodel.OperationCreate, GroupID: "g1",
			},
		},
	}

	groups := buildSimGroups(cmd)
	counts := opCounts(groups)

	assert.Equal(t, 1, counts[opReplace])
	assert.Equal(t, 0, counts[opDelete], "paired delete should not count separately")
	assert.Equal(t, 0, counts[opCreate], "paired create should not count separately")
}

// ---------------------------------------------------------------------------
// sortRows
// ---------------------------------------------------------------------------

// TestSortRowsAsc verifies sortRows sorts by the given column in ascending order.
func TestSortRowsAsc(t *testing.T) {
	rows := []simRow{
		{label: "z-row", op: opCreate},
		{label: "a-row", op: opDelete},
		{label: "m-row", op: opUpdate},
	}

	// Sort by label (col 0 = label) ascending
	sortRows(rows, 0, components.SortAsc)
	assert.Equal(t, "a-row", rows[0].label)
	assert.Equal(t, "m-row", rows[1].label)
	assert.Equal(t, "z-row", rows[2].label)
}

// TestSortRowsDesc verifies sortRows sorts by the given column in descending order.
func TestSortRowsDesc(t *testing.T) {
	rows := []simRow{
		{label: "z-row", op: opCreate},
		{label: "a-row", op: opDelete},
		{label: "m-row", op: opUpdate},
	}

	sortRows(rows, 0, components.SortDesc)
	assert.Equal(t, "z-row", rows[0].label)
	assert.Equal(t, "m-row", rows[1].label)
	assert.Equal(t, "a-row", rows[2].label)
}

// TestSortRowsNone verifies sortRows with SortNone applies the default
// destructive-first sort (by opKind).
func TestSortRowsNone(t *testing.T) {
	rows := []simRow{
		{label: "a", op: opCreate},
		{label: "b", op: opDelete},
		{label: "c", op: opKeep},
	}

	sortRows(rows, 0, components.SortNone)
	assert.Equal(t, opDelete, rows[0].op)
	assert.Equal(t, opCreate, rows[1].op)
	assert.Equal(t, opKeep, rows[2].op)
}

// ---------------------------------------------------------------------------
// opKind word
// ---------------------------------------------------------------------------

// TestOpKindWord verifies each opKind returns the correct word.
func TestOpKindWord(t *testing.T) {
	cases := []struct {
		op   opKind
		word string
	}{
		{opDelete, "delete"},
		{opReplace, "replace"},
		{opUpdate, "update"},
		{opDetach, "detach"},
		{opCreate, "create"},
		{opKeep, "keep"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.word, tc.op.word(), "word for %v", tc.op)
	}
}

// ---------------------------------------------------------------------------
// Policy ref pointer
// ---------------------------------------------------------------------------

// TestPolicyRowCarriesPointer verifies policy rows carry a non-nil policy pointer.
func TestPolicyRowCarriesPointer(t *testing.T) {
	cmd := &apimodel.Command{
		PolicyUpdates: []apimodel.PolicyUpdate{
			{PolicyLabel: "my-ttl", PolicyType: "ttl", StackLabel: "prod", Operation: "create"},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	row := groups[0].rows[0]
	require.NotNil(t, row.policy)
	assert.Equal(t, "my-ttl", row.policy.PolicyLabel)
}

// TestResourceRowCarriesPointer verifies resource rows carry a non-nil res pointer.
func TestResourceRowCarriesPointer(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "r1", ResourceLabel: "bucket", ResourceType: "AWS::S3::Bucket", StackName: "s", Operation: apimodel.OperationCreate},
		},
	}

	groups := buildSimGroups(cmd)
	require.Len(t, groups, 1)
	row := groups[0].rows[0]
	require.NotNil(t, row.res)
	assert.Equal(t, "r1", row.res.ResourceID)
}
