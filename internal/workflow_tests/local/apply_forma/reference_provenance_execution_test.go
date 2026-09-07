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
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestApplyForma_UnchangedReference_IsAbsentFromTheExecutedPatch pins the
// patch a provider actually receives when an unrelated field changes on a
// resource that also carries a reference the provider echoes in a different
// spelling.
//
// The reference is unchanged, so it must appear in neither the simulated
// plan nor the executed patch. The executed patch is the load-bearing half:
// the executor re-resolves every reference before the provider call and
// re-derives the patch from the resolved properties, which is a separate
// code path from the one the plan is built on.
func TestApplyForma_UnchangedReference_IsAbsentFromTheExecutedPatch(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		echoForm := func(name string) string { return "arn:fake:parent/" + name }

		var consumerUpdatePatch atomic.Value
		var consumerNote atomic.Value
		consumerNote.Store("note-v1")

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				props := json.RawMessage(`{"Name":"parent-v1","Value":"hello"}`)
				if req.ResourceType == "FakeAWS::Noted::Consumer" {
					props = json.RawMessage(fmt.Sprintf(
						`{"Name":"consumer-1","Note":"%s","ParentRef":"%s"}`,
						consumerNote.Load().(string), echoForm("parent-v1")))
				}
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           req.Label,
						ResourceProperties: props,
					},
				}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				switch req.ResourceType {
				case "FakeAWS::Noted::Parent":
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   `{"Name":"parent-v1","Value":"hello"}`,
					}, nil
				case "FakeAWS::Noted::Consumer":
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties: fmt.Sprintf(`{"Name":"consumer-1","Note":"%s","ParentRef":"%s"}`,
							consumerNote.Load().(string), echoForm("parent-v1")),
					}, nil
				}
				return nil, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				if req.ResourceType == "FakeAWS::Noted::Consumer" && req.PatchDocument != nil {
					consumerUpdatePatch.Store(fmt.Sprintf("%s", *req.PatchDocument))
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
			Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
		}
		consumerSchema := pkgmodel.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "Note", "ParentRef"},
			Hints: map[string]pkgmodel.FieldHint{
				"Name":      {CreateOnly: true},
				"ParentRef": {CreateOnly: false},
			},
		}

		formaFor := func(note string) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
				Resources: []pkgmodel.Resource{
					{
						Label:      "parent",
						Type:       "FakeAWS::Noted::Parent",
						Properties: json.RawMessage(`{"Name":"parent-v1","Value":"hello"}`),
						Stack:      "test-stack",
						Target:     "test-target",
						Managed:    true,
						Schema:     parentSchema,
					},
					{
						Label: "consumer",
						Type:  "FakeAWS::Noted::Consumer",
						Properties: json.RawMessage(fmt.Sprintf(`{
							"Name": "consumer-1",
							"Note": "%s",
							"ParentRef": {
								"$res": true,
								"$label": "parent",
								"$type": "FakeAWS::Noted::Parent",
								"$stack": "test-stack",
								"$property": "Name"
							}
						}`, note)),
						Stack:   "test-stack",
						Target:  "test-target",
						Managed: true,
						Schema:  consumerSchema,
					},
				},
				Targets: []pkgmodel.Target{{Label: "test-target"}},
			}
		}

		_, err = m.ApplyForma(formaFor("note-v1"), &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test", "", "")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			fas, _ := m.Datastore.LoadFormaCommands()
			return len(fas) > 0 && (fas[0].State == forma_command.CommandStateSuccess || fas[0].State == forma_command.CommandStateFailed)
		}, 10*time.Second, 100*time.Millisecond, "v1 should reach terminal state")
		fas, _ := m.Datastore.LoadFormaCommands()
		require.Equal(t, forma_command.CommandStateSuccess, fas[0].State, "v1 apply should succeed")

		// Change only the unrelated field. The reference still resolves to
		// the same parent identity it was applied with.
		consumerNote.Store("note-v1") // the cloud still reports the pre-update note
		v2 := formaFor("note-v2")

		simResp, err := m.ApplyForma(v2, &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: true,
		}, "test", "", "")
		require.NoError(t, err)
		require.True(t, simResp.Simulation.ChangesRequired, "changing the note should produce a change")
		var plannedPatch string
		for _, ru := range simResp.Simulation.Command.ResourceUpdates {
			if ru.ResourceLabel == "consumer" {
				plannedPatch = string(ru.PatchDocument)
			}
		}
		require.Contains(t, plannedPatch, "/Note", "the plan should carry the changed field")
		require.NotContains(t, plannedPatch, "/ParentRef",
			"the plan must not touch a reference that still resolves to what was applied")

		_, err = m.ApplyForma(v2, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test", "", "")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			fas, _ := m.Datastore.LoadFormaCommands()
			return len(fas) >= 2 && (fas[0].State == forma_command.CommandStateSuccess || fas[0].State == forma_command.CommandStateFailed)
		}, 30*time.Second, 100*time.Millisecond, "v2 should reach terminal state")
		fas, _ = m.Datastore.LoadFormaCommands()
		require.Equal(t, forma_command.CommandStateSuccess, fas[0].State, "v2 apply should succeed")

		executed := consumerUpdatePatch.Load()
		require.NotNil(t, executed, "the provider must receive the consumer's update")
		assert.Contains(t, executed.(string), "/Note", "the executed patch should carry the changed field")
		assert.NotContains(t, executed.(string), "/ParentRef",
			"the executed patch must match the plan: an unchanged reference is not rewritten")
	})
}
