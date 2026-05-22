// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceUpdater_Initialization(t *testing.T) {
	sender := gen.PID{Node: "test", ID: 100}

	ds := newMockDatastore()
	env := map[gen.Env]any{
		"RetryConfig": pkgmodel.RetryConfig{
			StatusCheckInterval: 1 * time.Second,
		},
		"DiscoveryConfig": pkgmodel.DiscoveryConfig{},
		"Datastore":       ds,
	}

	updater, err := unit.Spawn(t, newResourceUpdater,
		unit.WithArgs(sender),
		unit.WithEnv(env))

	assert.NoError(t, err, "Failed to spawn resource updater")
	assert.NotNil(t, updater, "Resource updater should not be nil")
	assert.False(t, updater.IsTerminated(), "Resource updater should not be terminated after spawning")
}

// TestRegenerateCascadePatch_ResolvableValueChanged mirrors the RFC-0042
// cascade-update scenario: the dependent's PriorState carries a $ref+$value
// pointing at a parent property (e.g. ECS Service.TaskDefinition →
// TaskDef.TaskDefinitionArn), and the executor's resolver has substituted
// the post-Create value into DesiredState's $value. The regen helper must
// flatten both sides and produce a JSON-Patch that replaces the resolvable's
// target path with the new value — that's the patch the AWS plugin sends to
// CloudControl UpdateResource.
func TestRegenerateCascadePatch_ResolvableValueChanged(t *testing.T) {
	parentKsuid := "3E3wKW8YqVCQEyfKjsGpbsoE8bl"

	priorProps := json.RawMessage(`{
		"ServiceName": "formae-sdk-test-svc",
		"Cluster": {
			"$ref": "formae://cluster-ksuid#/Arn",
			"$value": "arn:aws:ecs:us-east-1:0:cluster/test"
		},
		"TaskDefinition": {
			"$ref": "formae://` + parentKsuid + `#/TaskDefinitionArn",
			"$value": "arn:aws:ecs:us-east-1:0:task-definition/test:1"
		},
		"DesiredCount": 0
	}`)

	// DesiredState mirrors PriorState except the cascading ref's $value has
	// been bumped by the executor's resolver after the parent's Create
	// completed (TaskDef revision 2 of the same family).
	desiredProps := json.RawMessage(`{
		"ServiceName": "formae-sdk-test-svc",
		"Cluster": {
			"$ref": "formae://cluster-ksuid#/Arn",
			"$value": "arn:aws:ecs:us-east-1:0:cluster/test"
		},
		"TaskDefinition": {
			"$ref": "formae://` + parentKsuid + `#/TaskDefinitionArn",
			"$value": "arn:aws:ecs:us-east-1:0:task-definition/test:2"
		},
		"DesiredCount": 0
	}`)

	schema := pkgmodel.Schema{
		Identifier: "ServiceName",
		Fields:     []string{"ServiceName", "Cluster", "TaskDefinition", "DesiredCount"},
		Hints: map[string]pkgmodel.FieldHint{
			"ServiceName":    {CreateOnly: true},
			"Cluster":        {CreateOnly: true},
			"TaskDefinition": {CreateOnly: false},
			"DesiredCount":   {CreateOnly: false},
		},
	}

	patchDoc, err := regenerateCascadePatch(priorProps, desiredProps, schema)
	require.NoError(t, err, "regenerateCascadePatch should succeed")
	require.NotEmpty(t, patchDoc, "regenerated patch must not be empty — that's the whole point of the helper")
	patchStr := string(patchDoc)
	assert.NotEqual(t, "[]", patchStr, "regenerated patch must contain at least one op")
	assert.Contains(t, patchStr, "TaskDefinition", "patch must target the TaskDefinition path")
	assert.Contains(t, patchStr, "task-definition/test:2", "patch must carry the post-resolution $value (revision 2)")
	assert.NotContains(t, patchStr, "task-definition/test:1", "patch must NOT carry the old $value (revision 1)")
	// Unchanged fields must not appear in the patch.
	assert.False(t, strings.Contains(patchStr, "DesiredCount") || strings.Contains(patchStr, "Cluster"),
		"patch should only mention the changed resolvable, not stable fields: %s", patchStr)
}

// TestRegenerateCascadePatch_NoChange covers a degenerate input where prior
// and desired are byte-identical (e.g. the resolver didn't surface a new
// $value because the parent's relevant property didn't actually change after
// all). The helper must succeed and return an empty patch — the executor's
// upstream check then short-circuits the plugin call instead of sending
// noise.
func TestRegenerateCascadePatch_NoChange(t *testing.T) {
	props := json.RawMessage(`{
		"ServiceName": "svc",
		"TaskDefinition": {
			"$ref": "formae://parent-ksuid#/TaskDefinitionArn",
			"$value": "arn:aws:ecs:us-east-1:0:task-definition/test:1"
		}
	}`)
	schema := pkgmodel.Schema{
		Identifier: "ServiceName",
		Fields:     []string{"ServiceName", "TaskDefinition"},
		Hints: map[string]pkgmodel.FieldHint{
			"ServiceName":    {CreateOnly: true},
			"TaskDefinition": {CreateOnly: false},
		},
	}
	patchDoc, err := regenerateCascadePatch(props, props, schema)
	require.NoError(t, err)
	if len(patchDoc) > 0 {
		assert.Equal(t, "[]", string(patchDoc), "no diff should produce empty patch ops, got %s", string(patchDoc))
	}
}
