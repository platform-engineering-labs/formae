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

// TestResolveValue_KeepsPatchDocumentInSync covers the ResolveValue
// contract: PatchDocument is derived from (PriorState, DesiredState,
// Schema), so whenever ResolveValue mutates DesiredState.Properties it
// must re-derive the patch. Mirrors the cascade-update scenario where a
// parent (e.g. ECS TaskDefinition) is being replaced and the dependent's
// $value tracks the parent's new identifier — the eventual plugin Update
// must see a patch reflecting that change.
func TestResolveValue_KeepsPatchDocumentInSync(t *testing.T) {
	parentKsuid := "3E3wKW8YqVCQEyfKjsGpbsoE8bl"
	parentURI := pkgmodel.FormaeURI("formae://" + parentKsuid + "#/TaskDefinitionArn")

	priorProps := json.RawMessage(`{
		"ServiceName": "formae-sdk-test-svc",
		"TaskDefinition": {
			"$ref": "formae://` + parentKsuid + `#/TaskDefinitionArn",
			"$value": "arn:aws:ecs:us-east-1:0:task-definition/test:1"
		},
		"DesiredCount": 0
	}`)

	schema := pkgmodel.Schema{
		Identifier: "ServiceName",
		Fields:     []string{"ServiceName", "TaskDefinition", "DesiredCount"},
		Hints: map[string]pkgmodel.FieldHint{
			"ServiceName":    {CreateOnly: true},
			"TaskDefinition": {CreateOnly: false},
			"DesiredCount":   {CreateOnly: false},
		},
	}

	ru := &ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label:      "svc",
			Properties: priorProps,
			Schema:     schema,
		},
		DesiredState: pkgmodel.Resource{
			Label: "svc",
			// DesiredState starts byte-identical to prior — typical for a
			// cascade-update emitted by the planner where the user didn't
			// directly modify the dependent.
			Properties: append(json.RawMessage(nil), priorProps...),
			Schema:     schema,
		},
	}

	err := ru.ResolveValue(parentURI, "arn:aws:ecs:us-east-1:0:task-definition/test:2")
	require.NoError(t, err)
	require.NotEmpty(t, ru.DesiredState.PatchDocument, "ResolveValue must populate PatchDocument when DesiredState changes")
	patchStr := string(ru.DesiredState.PatchDocument)
	assert.Contains(t, patchStr, "TaskDefinition", "patch must target the path whose $value changed")
	assert.Contains(t, patchStr, "task-definition/test:2", "patch must carry the post-resolution $value")
	assert.NotContains(t, patchStr, "task-definition/test:1", "patch must not carry the pre-resolution $value")
	assert.False(t, strings.Contains(patchStr, "DesiredCount"),
		"patch should only diff the changed field, not stable ones: %s", patchStr)
}

// TestResolveValue_NoChangeProducesEmptyPatch confirms that resolving a
// $ref to the same $value it already has yields an empty/no-op patch —
// no spurious provider calls.
func TestResolveValue_NoChangeProducesEmptyPatch(t *testing.T) {
	parentKsuid := "parent-ksuid"
	parentURI := pkgmodel.FormaeURI("formae://" + parentKsuid + "#/Arn")
	props := json.RawMessage(`{
		"ServiceName": "svc",
		"ParentRef": {"$ref": "formae://` + parentKsuid + `#/Arn", "$value": "v1"}
	}`)
	schema := pkgmodel.Schema{
		Identifier: "ServiceName",
		Fields:     []string{"ServiceName", "ParentRef"},
		Hints: map[string]pkgmodel.FieldHint{
			"ServiceName": {CreateOnly: true},
			"ParentRef":   {CreateOnly: false},
		},
	}
	ru := &ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Properties: props,
			Schema:     schema,
		},
		DesiredState: pkgmodel.Resource{
			Properties: append(json.RawMessage(nil), props...),
			Schema:     schema,
		},
	}
	err := ru.ResolveValue(parentURI, "v1")
	require.NoError(t, err)
	if len(ru.DesiredState.PatchDocument) > 0 {
		assert.Equal(t, "[]", string(ru.DesiredState.PatchDocument),
			"no $value change should produce no patch ops, got %s", string(ru.DesiredState.PatchDocument))
	}
}

// TestResolveValue_NonUpdateLeavesPatchDocumentUntouched ensures the
// patch-regen branch only fires for Updates. Creates and Deletes carry
// full state to the provider rather than a diff, so they don't need —
// and shouldn't pay for — a regenerated PatchDocument when resolvables
// are substituted along the way.
func TestResolveValue_NonUpdateLeavesPatchDocumentUntouched(t *testing.T) {
	parentURI := pkgmodel.FormaeURI("formae://parent-ksuid#/Arn")
	props := json.RawMessage(`{"Ref": {"$ref": "formae://parent-ksuid#/Arn", "$value": "v1"}}`)
	for _, op := range []OperationType{OperationCreate, OperationDelete, OperationReplace} {
		t.Run(string(op), func(t *testing.T) {
			ru := &ResourceUpdate{
				Operation: op,
				PriorState: pkgmodel.Resource{Properties: props},
				DesiredState: pkgmodel.Resource{
					Properties: append(json.RawMessage(nil), props...),
					// Schema with Fields so the regen WOULD fire if the
					// Operation guard weren't in place.
					Schema: pkgmodel.Schema{
						Fields: []string{"Ref"},
						Hints:  map[string]pkgmodel.FieldHint{"Ref": {CreateOnly: false}},
					},
					PatchDocument: json.RawMessage(`["pre-existing-untouched"]`),
				},
			}
			err := ru.ResolveValue(parentURI, "v2")
			require.NoError(t, err)
			assert.Equal(t, `["pre-existing-untouched"]`, string(ru.DesiredState.PatchDocument),
				"non-Update operations must not re-derive PatchDocument")
		})
	}
}
