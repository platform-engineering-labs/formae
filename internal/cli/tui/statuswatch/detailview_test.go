// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func TestBuildGroups_OrderAndMapping(t *testing.T) {
	c := apimodel.Command{
		State:         "InProgress",
		TargetUpdates: []apimodel.TargetUpdate{{TargetLabel: "aws-us-east-1", Operation: "create", State: "Success", Duration: 3000, Discoverable: true}},
		StackUpdates:  []apimodel.StackUpdate{{StackLabel: "production", Operation: "create", State: "Success", Description: "Production environment"}},
		PolicyUpdates: []apimodel.PolicyUpdate{{PolicyLabel: "auto-reconcile", PolicyType: "ttl", StackLabel: "production", Operation: "update", State: "Success"}},
		ResourceUpdates: []apimodel.ResourceUpdate{{
			ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket", StackName: "production",
			Operation: "create", State: "InProgress", CurrentAttempt: 1, MaxAttempts: 9, StatusMessage: "Creating resource...",
		}},
	}
	groups := buildGroups(c)
	require.Len(t, groups, 4)
	assert.Equal(t, []string{"Targets", "Stacks", "Policies", "Resources"},
		[]string{groups[0].title, groups[1].title, groups[2].title, groups[3].title})

	r := groups[3].rows[0]
	assert.Equal(t, "my-bucket", r.label)
	assert.Equal(t, "AWS::S3::Bucket", r.typeName)
	assert.Equal(t, components.StateInProgress, r.state)
	assert.Equal(t, 1, r.attempt)
	assert.Equal(t, 9, r.maxAttempt)
	assert.Equal(t, "Creating resource...", r.statusMsg)
	assert.Equal(t, 3*time.Second, groups[0].rows[0].duration)
	assert.True(t, groups[0].rows[0].discoverable)
}

func TestBuildGroups_OmitsEmptyGroupsAndSetsCancelLabels(t *testing.T) {
	c := apimodel.Command{
		State: "Canceling",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "a", State: "InProgress"},
			{ResourceLabel: "b", State: "Canceled"},
		},
	}
	groups := buildGroups(c)
	require.Len(t, groups, 1)
	assert.Equal(t, "finishing", groups[0].rows[0].stateLabel)
	assert.Equal(t, "canceled", groups[0].rows[1].stateLabel)
}

func TestBuildGroups_StableKeys(t *testing.T) {
	c := apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{ResourceLabel: "web-1", State: "Success"}}}
	k1 := buildGroups(c)[0].rows[0].key
	k2 := buildGroups(c)[0].rows[0].key
	assert.Equal(t, k1, k2)
	assert.Contains(t, k1, "web-1")
}

func TestBuildGroups_KeysDistinguishSameLabelAcrossStacks(t *testing.T) {
	c := apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{
		{ResourceLabel: "web", StackName: "production", State: "Success"},
		{ResourceLabel: "web", StackName: "staging", State: "Success"},
	}}
	rows := buildGroups(c)[0].rows
	require.Len(t, rows, 2)
	assert.NotEqual(t, rows[0].key, rows[1].key)
	assert.Contains(t, rows[0].key, "web")
	assert.Contains(t, rows[1].key, "web")
}

func TestVisibleRows_Pagination(t *testing.T) {
	g := group{kind: kindResource, title: "Resources"}
	for i := 0; i < 25; i++ {
		g.rows = append(g.rows, updateRow{label: fmt.Sprintf("r-%02d", i)})
	}
	shown, remaining := visibleRows(g, 10)
	assert.Len(t, shown, 10)
	assert.Equal(t, 15, remaining)

	shown, remaining = visibleRows(g, 20)
	assert.Len(t, shown, 20)
	assert.Equal(t, 5, remaining)

	shown, remaining = visibleRows(g, 99)
	assert.Len(t, shown, 25)
	assert.Zero(t, remaining)
}

func TestSortGroup_ByLabelAndTime(t *testing.T) {
	rows := []updateRow{
		{label: "b", duration: 2 * time.Second},
		{label: "a", duration: 3 * time.Second},
	}
	sortGroup(rows, detailColLabel, components.SortAsc)
	assert.Equal(t, "a", rows[0].label)
	sortGroup(rows, detailColTime, components.SortDesc)
	assert.Equal(t, 3*time.Second, rows[0].duration)
}

func TestValidSortCols_PerKind(t *testing.T) {
	assert.NotContains(t, validSortCols(kindTarget), detailColType)
	assert.NotContains(t, validSortCols(kindTarget), detailColStack)
	assert.Contains(t, validSortCols(kindResource), detailColType)
	assert.Contains(t, validSortCols(kindPolicy), detailColStack)
}
