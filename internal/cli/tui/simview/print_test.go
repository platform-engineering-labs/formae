// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package simview

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// makePlainFixtureSim returns a rich Simulation covering creates, updates,
// replaces, deletes across resources, plus a target and a policy.
func makePlainFixtureSim() *apimodel.Simulation {
	return &apimodel.Simulation{
		ChangesRequired: true,
		Command: apimodel.Command{
			CommandID: "plain-pin-001",
			Command:   "apply",
			Mode:      "reconcile",
			TargetUpdates: []apimodel.TargetUpdate{
				{TargetLabel: "aws-us-east-1", Operation: "create"},
			},
			StackUpdates: []apimodel.StackUpdate{
				{StackLabel: "staging", Operation: "create"},
			},
			PolicyUpdates: []apimodel.PolicyUpdate{
				{PolicyLabel: "ttl-policy", PolicyType: "ttl", StackLabel: "staging", Operation: "create"},
			},
			ResourceUpdates: []apimodel.ResourceUpdate{
				// create
				{ResourceID: "r-cre1", ResourceLabel: "web-bucket", ResourceType: "AWS::S3::Bucket", StackName: "staging", Operation: "create"},
				{ResourceID: "r-cre2", ResourceLabel: "app-server", ResourceType: "AWS::EC2::Instance", StackName: "staging", Operation: "create"},
				// update
				{ResourceID: "r-upd1", ResourceLabel: "db-instance", ResourceType: "AWS::RDS::DBInstance", StackName: "staging", Operation: "update",
					PatchDocument: []byte(`[{"op":"replace","path":"/InstanceClass","value":"db.t3.large"}]`),
					Properties:    []byte(`{"InstanceClass":"db.t3.large"}`),
					OldProperties: []byte(`{"InstanceClass":"db.t3.medium"}`),
				},
				// replace pair
				{ResourceID: "r-rep-del", ResourceLabel: "cache-node", ResourceType: "AWS::ElastiCache::CacheCluster", StackName: "staging", Operation: "delete", GroupID: "grp-cache"},
				{ResourceID: "r-rep-cre", ResourceLabel: "cache-node", ResourceType: "AWS::ElastiCache::CacheCluster", StackName: "staging", Operation: "create", GroupID: "grp-cache"},
				// delete
				{ResourceID: "r-del1", ResourceLabel: "legacy-queue", ResourceType: "AWS::SQS::Queue", StackName: "staging", Operation: "delete"},
			},
		},
	}
}

// TestRenderSimulationPlain_Golden pins the styled output as a golden file.
func TestRenderSimulationPlain_Golden(t *testing.T) {
	tuitest.PinRendering()
	th := theme.New("formae")
	sim := makePlainFixtureSim()
	out := RenderSimulationPlain(th, sim, 100)
	tuitest.RequireGolden(t, []byte(out))
}

// TestRenderSimulationPlain_Content checks structural content in stripped output.
func TestRenderSimulationPlain_Content(t *testing.T) {
	tuitest.PinRendering()
	th := theme.New("formae")
	sim := makePlainFixtureSim()
	out := RenderSimulationPlain(th, sim, 100)
	stripped := plain(out)

	// Section headers present
	assert.Contains(t, stripped, "Targets", "Targets section must appear")
	assert.Contains(t, stripped, "Stacks", "Stacks section must appear")
	assert.Contains(t, stripped, "Policies", "Policies section must appear")
	assert.Contains(t, stripped, "Resources", "Resources section must appear")

	// Operation words present
	assert.Contains(t, stripped, "create", "create operation must appear")
	assert.Contains(t, stripped, "update", "update operation must appear")
	assert.Contains(t, stripped, "replace", "replace operation must appear")
	assert.Contains(t, stripped, "delete", "delete operation must appear")

	// Resource labels present
	assert.Contains(t, stripped, "web-bucket", "web-bucket label must appear")
	assert.Contains(t, stripped, "app-server", "app-server label must appear")
	assert.Contains(t, stripped, "db-instance", "db-instance label must appear")
	assert.Contains(t, stripped, "cache-node", "cache-node label must appear")
	assert.Contains(t, stripped, "legacy-queue", "legacy-queue label must appear")

	// Target and stack labels
	assert.Contains(t, stripped, "aws-us-east-1", "target label must appear")
	assert.Contains(t, stripped, "staging", "stack label must appear")

	// Policy
	assert.Contains(t, stripped, "ttl-policy", "policy label must appear")

	// Property change from update card
	assert.Contains(t, stripped, "InstanceClass", "updated property must appear in card")

	// No stray ANSI in stripped form
	assert.NotContains(t, stripped, "\x1b[", "stripped output must contain no ANSI escapes")

	// Non-empty output
	assert.Greater(t, len(strings.TrimSpace(stripped)), 0, "output must be non-empty")
}
