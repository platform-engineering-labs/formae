// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestApplyForma_ParentReplace_NonCreateOnlyRef_DependentIsUpdated covers
// the cascade-update path end-to-end: when a parent is replaced (a
// CreateOnly field on the parent changed) and a dependent references it
// via a *non*-CreateOnly field, the planner emits a single Update on the
// dependent — not a cascade delete+create — and the executor regenerates
// the dependent's PatchDocument so the provider's Update actually carries
// the new resolved value.
//
// Motivating real-world case: an ECS Service consuming a versioned
// TaskDefinition. Tearing the Service down on every TaskDef revision
// bump (~2-4 min outage per image deploy) was the previous behavior;
// this test pins the rolling-update behavior in place locally so we
// don't have to rely solely on the AWS-side conformance run.
//
// Two phases share the same v1 setup:
//   - Simulate v2 to confirm the planner emits cascade-update with
//     IsCascade=true and a synthesized PatchDocument the CLI can preview.
//   - Real-apply v2 to confirm the executor runs the resolver, regenerates
//     the patch with the post-replace parent value, and hands that patch
//     to the provider's Update.
//
// Fixture: FakeAWS::Versioned::Parent has a CreateOnly Name field;
// changing it forces Replace. FakeAWS::Versioned::Consumer.ParentRef
// resolves to the Parent and is *not* CreateOnly.
func TestApplyForma_ParentReplace_NonCreateOnlyRef_DependentIsUpdated(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Stateful Read so the resolve cache returns parent-v1 during v1
		// and parent-v2 once we flip the flag for v2. Without this the
		// executor's resolver would substitute the old value into
		// DesiredState at v2 apply time, the patch regen would produce
		// no diff, and the test would silently pass the wrong thing.
		var parentName atomic.Value
		parentName.Store("parent-v1")

		// Captures the PatchDocument the plugin actually receives for the
		// consumer's Update — the end-to-end signal that the regenerated
		// patch made it all the way through.
		var consumerUpdatePatch atomic.Value

		overrides := &plugin.ResourcePluginOverrides{
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				switch req.ResourceType {
				case "FakeAWS::Versioned::Parent":
					name := parentName.Load().(string)
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   fmt.Sprintf(`{"Name":"%s","Value":"hello"}`, name),
					}, nil
				case "FakeAWS::Versioned::Consumer":
					// The drift-detection Read at the start of an Update
					// compares the plugin's properties to the stored ones
					// (with $ref/$value stripped). Return the pre-update
					// cloud state — the consumer's ParentRef still tracks
					// parent-v1 because Update hasn't fired yet.
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   `{"Name":"consumer-1","ParentRef":"parent-v1"}`,
					}, nil
				}
				return nil, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				if req.ResourceType == "FakeAWS::Versioned::Consumer" && req.PatchDocument != nil {
					consumerUpdatePatch.Store(*req.PatchDocument)
				}
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationUpdate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "update-req-1",
						NativeID:        req.NativeID,
					},
				}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		parentSchema := pkgmodel.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "Value"},
			Hints: map[string]pkgmodel.FieldHint{
				"Name": {CreateOnly: true},
			},
		}
		consumerSchema := pkgmodel.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "ParentRef"},
			Hints: map[string]pkgmodel.FieldHint{
				"Name":      {CreateOnly: true},
				"ParentRef": {CreateOnly: false},
			},
		}

		v1Forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label:      "parent",
					Type:       "FakeAWS::Versioned::Parent",
					Properties: json.RawMessage(`{"Name":"parent-v1","Value":"hello"}`),
					Stack:      "test-stack",
					Target:     "test-target",
					Managed:    true,
					Schema:     parentSchema,
				},
				{
					Label: "consumer",
					Type:  "FakeAWS::Versioned::Consumer",
					Properties: json.RawMessage(`{
						"Name": "consumer-1",
						"ParentRef": {
							"$res": true,
							"$label": "parent",
							"$type": "FakeAWS::Versioned::Parent",
							"$stack": "test-stack",
							"$property": "Name"
						}
					}`),
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
					Schema:  consumerSchema,
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target"}},
		}

		_, err = m.ApplyForma(v1Forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			fas, _ := m.Datastore.LoadFormaCommands()
			if len(fas) == 0 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess || fas[0].State == forma_command.CommandStateFailed
		}, 10*time.Second, 100*time.Millisecond, "v1 should reach terminal state")
		fas, _ := m.Datastore.LoadFormaCommands()
		require.Equal(t, forma_command.CommandStateSuccess, fas[0].State, "v1 apply should succeed")

		// v2: bump parent.Name (CreateOnly, forces Replace) and flip the
		// stateful Read so the executor's resolver picks up the new
		// identity when the consumer re-resolves at apply time.
		parentName.Store("parent-v2")

		v2Forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label:      "parent",
					Type:       "FakeAWS::Versioned::Parent",
					Properties: json.RawMessage(`{"Name":"parent-v2","Value":"hello"}`),
					Stack:      "test-stack",
					Target:     "test-target",
					Managed:    true,
					Schema:     parentSchema,
				},
				{
					Label: "consumer",
					Type:  "FakeAWS::Versioned::Consumer",
					Properties: json.RawMessage(`{
						"Name": "consumer-1",
						"ParentRef": {
							"$res": true,
							"$label": "parent",
							"$type": "FakeAWS::Versioned::Parent",
							"$stack": "test-stack",
							"$property": "Name"
						}
					}`),
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
					Schema:  consumerSchema,
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target"}},
		}

		// --- Phase 1: simulate v2, assert on the planner's API-level output.
		// Simulate doesn't persist a FormaCommand, so the IsCascade flag
		// (which BulkStoreResourceUpdates currently drops on the floor)
		// survives in the response.
		simResp, err := m.ApplyForma(v2Forma, &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: true,
		}, "test")
		require.NoError(t, err)
		require.True(t, simResp.Simulation.ChangesRequired, "v2 should produce changes")

		var simParentOps, simConsumerOps []string
		var simConsumerUpdate *apimodel.ResourceUpdate
		for i, ru := range simResp.Simulation.Command.ResourceUpdates {
			switch ru.ResourceLabel {
			case "parent":
				simParentOps = append(simParentOps, ru.Operation)
			case "consumer":
				simConsumerOps = append(simConsumerOps, ru.Operation)
				simConsumerUpdate = &simResp.Simulation.Command.ResourceUpdates[i]
			}
		}
		assert.Len(t, simParentOps, 2, "simulate: parent should be Replaced (delete + create)")
		assert.Contains(t, simParentOps, apimodel.OperationDelete)
		assert.Contains(t, simParentOps, apimodel.OperationCreate)
		require.Len(t, simConsumerOps, 1, "simulate: consumer with non-CreateOnly ref should NOT cascade-replace")
		assert.Equal(t, apimodel.OperationUpdate, simConsumerOps[0])
		require.NotNil(t, simConsumerUpdate)
		assert.True(t, simConsumerUpdate.IsCascade,
			"simulate response must surface the IsCascade flag so the CLI can render '(cascade)'")
		patchStr := string(simConsumerUpdate.PatchDocument)
		assert.Contains(t, patchStr, `"path":"/ParentRef"`,
			"synthesized patch must target the dependent's referring field")
		assert.Contains(t, patchStr, `"parent-v2"`,
			"user-provided source field: synthesized patch should carry the concrete new value from the forma")

		// --- Phase 2: real-apply v2. The simulate above didn't persist;
		// this run drives the executor through resolving → updating, the
		// resolver substitutes parent-v2 via the stateful Read, ResolveValue
		// re-derives the patch, and the provider's Update receives it.
		_, err = m.ApplyForma(v2Forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			fas, _ := m.Datastore.LoadFormaCommands()
			if len(fas) < 2 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess || fas[0].State == forma_command.CommandStateFailed
		}, 30*time.Second, 100*time.Millisecond, "v2 should reach terminal state")
		fas, _ = m.Datastore.LoadFormaCommands()
		require.Equal(t, forma_command.CommandStateSuccess, fas[0].State, "v2 apply should succeed end-to-end")

		v2cmd := fas[0]
		var realParentOps, realConsumerOps []resource_update.OperationType
		for _, ru := range v2cmd.ResourceUpdates {
			switch ru.DesiredState.Label {
			case "parent":
				realParentOps = append(realParentOps, ru.Operation)
			case "consumer":
				realConsumerOps = append(realConsumerOps, ru.Operation)
			}
		}
		assert.Len(t, realParentOps, 2, "real-apply: parent should be Replaced (delete + create)")
		assert.Contains(t, realParentOps, resource_update.OperationDelete)
		assert.Contains(t, realParentOps, resource_update.OperationCreate)
		require.Len(t, realConsumerOps, 1, "real-apply: consumer should be Updated, not Replaced")
		assert.Equal(t, resource_update.OperationUpdate, realConsumerOps[0])

		// The end-to-end proof: the executor's resolver substituted
		// parent-v2 into DesiredState, ResolveValue re-derived the patch,
		// and that patch made it all the way to the provider's Update
		// call. Without the apply-time regen this would either be empty
		// (silently no-opping the Update) or carry the stale parent-v1
		// value.
		rawPatch, ok := consumerUpdatePatch.Load().(string)
		require.True(t, ok, "plugin Update was never called for the consumer")
		assert.Contains(t, rawPatch, "/ParentRef",
			"the patch the provider receives must target the resolvable's path")
		assert.Contains(t, rawPatch, "parent-v2",
			"the patch the provider receives must carry the post-resolution value, not the stale plan-time one")
		assert.NotContains(t, rawPatch, "parent-v1",
			"the patch the provider receives must NOT carry the pre-resolution value")
	})
}
