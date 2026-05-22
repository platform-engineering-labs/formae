// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestApplyForma_ParentReplace_NonCreateOnlyRef_DependentIsUpdated covers
// the cascade-update path: when a parent is replaced (a CreateOnly field
// on the parent changed) and a dependent references it via a
// *non*-CreateOnly field, the planner must emit a single Update on the
// dependent — not a cascade delete+create. The motivating case is an ECS
// Service consuming a versioned TaskDefinition: tearing the Service down
// on every TaskDef revision bump (~2-4 min outage per image deploy) was
// the previous behavior; this test pins the rolling-update behavior in
// place.
//
// Fixture: FakeAWS::Versioned::Parent has a CreateOnly Name field; changing
// it forces Replace. FakeAWS::Versioned::Consumer.ParentRef resolves to the
// Parent and is *not* CreateOnly.
func TestApplyForma_ParentReplace_NonCreateOnlyRef_DependentIsUpdated(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// The resolve cache performs a plugin Read whenever a $ref needs
		// fresh resolution. The default fakeaws Read returns "{}", which
		// fails to surface the parent's Name. Provide canonical responses
		// per resource type so the resolver succeeds.
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				switch request.ResourceType {
				case "FakeAWS::Versioned::Parent":
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"Name":"parent-v1","Value":"hello"}`,
					}, nil
				case "FakeAWS::Versioned::Consumer":
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"Name":"consumer-1","ParentRef":"parent-v1"}`,
					}, nil
				}
				return nil, nil
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
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) == 0 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess
		}, 10*time.Second, 100*time.Millisecond, "v1 apply should succeed")

		// v2: change Parent's CreateOnly field, forcing Replace. The Consumer
		// is unchanged in the forma — only the parent's identity is shifting,
		// and the resolvable URI (by ksuid) is stable across the parent's
		// Replace.
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

		// Simulate v2 — we only care about what the planner emits, not what
		// the executor does with it. Simulate skips execution and returns the
		// resolved plan directly.
		resp, err := m.ApplyForma(v2Forma, &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: true,
		}, "test")
		require.NoError(t, err)
		require.True(t, resp.Simulation.ChangesRequired,
			"v2 should produce changes — parent's CreateOnly field is shifting")

		var parentOps, consumerOps []string
		var consumerUpdate *apimodel.ResourceUpdate
		for i, ru := range resp.Simulation.Command.ResourceUpdates {
			switch ru.ResourceLabel {
			case "parent":
				parentOps = append(parentOps, ru.Operation)
			case "consumer":
				consumerOps = append(consumerOps, ru.Operation)
				consumerUpdate = &resp.Simulation.Command.ResourceUpdates[i]
			}
		}

		assert.Len(t, parentOps, 2, "parent should be Replaced (delete + create)")
		assert.Contains(t, parentOps, apimodel.OperationDelete, "parent should have a Delete op")
		assert.Contains(t, parentOps, apimodel.OperationCreate, "parent should have a Create op")

		require.Len(t, consumerOps, 1,
			"consumer with non-CreateOnly ref should NOT cascade-replace when parent is replaced")
		assert.Equal(t, apimodel.OperationUpdate, consumerOps[0],
			"consumer with non-CreateOnly ref should be Updated, not Replaced")

		// Verify the simulate output surfaces the cascading change. The
		// parent's Name is a user-provided field, so the new value lives in
		// the v2 forma and the synthesized patch should carry a concrete
		// `replace` op on /ParentRef with "parent-v2".
		require.NotNil(t, consumerUpdate)
		require.NotEmpty(t, consumerUpdate.PatchDocument,
			"cascade-update must carry a synthesized patch so simulate output names the cascading field change")
		patchStr := string(consumerUpdate.PatchDocument)
		assert.Contains(t, patchStr, `"path":"/ParentRef"`,
			"synthesized patch must target the dependent's referring field")
		assert.Contains(t, patchStr, `"parent-v2"`,
			"user-provided source field: synthesized patch should carry the concrete new value from the forma")
		assert.True(t, consumerUpdate.IsCascade, "cascade-update must propagate the IsCascade flag to the API model")
	})
}
