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

// TestApplyForma_ReconcileRemoval_SurvivesLiveReferenceResolution pins that a
// reconcile-planned removal reaches the provider even when the same update
// also carries a reference that only resolves at execution time.
//
// The consumer both references the producer's Name (forcing a live resolve
// on every apply, per the executor's re-resolve-before-dispatch path — see
// TestApplyForma_UnchangedReference_IsAbsentFromTheExecutedPatch) and
// declares an EntitySet-hinted Tags list. A plain scalar field dropped from
// a desired state produces no diff under either apply mode (jsonpatch only
// emits removes for EntitySet-hinted collections), so an EntitySet member is
// the shape that actually distinguishes reconcile from patch semantics; see
// regenerate_mode_test.go in the resource_update package for the same
// finding at the unit level.
//
// Before threading the command's apply mode into execution-time patch
// regeneration, the live reference resolve regenerated the patch under
// hardcoded patch-mode (EnsureExists) semantics regardless of how the
// command was actually planned, so a reconcile-planned Tags removal never
// reached the provider.
func TestApplyForma_ReconcileRemoval_SurvivesLiveReferenceResolution(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		echoForm := func(name string) string { return "arn:fake:producer/" + name }

		var consumerState atomic.Value // current provider-side consumer properties (JSON string)
		var consumerUpdatePatch atomic.Value

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				var props string
				switch req.ResourceType {
				case "FakeAWS::Tagged::Producer":
					props = `{"Name":"producer-1","Value":"hello"}`
				case "FakeAWS::Tagged::Consumer":
					props = fmt.Sprintf(
						`{"Name":"consumer-1","ParentRef":"%s","Tags":[{"Key":"env","Value":"prod"},{"Key":"legacy","Value":"true"}]}`,
						echoForm("producer-1"))
					consumerState.Store(props)
				}
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           req.Label,
						ResourceProperties: json.RawMessage(props),
					},
				}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				switch req.ResourceType {
				case "FakeAWS::Tagged::Producer":
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   `{"Name":"producer-1","Value":"hello"}`,
					}, nil
				case "FakeAWS::Tagged::Consumer":
					if stored, ok := consumerState.Load().(string); ok {
						return &resource.ReadResult{ResourceType: req.ResourceType, Properties: stored}, nil
					}
				}
				return nil, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				if req.ResourceType == "FakeAWS::Tagged::Consumer" && req.PatchDocument != nil {
					consumerUpdatePatch.Store(*req.PatchDocument)
					// Reflect the applied removal so a subsequent Read (e.g. the
					// next apply's plan) sees the post-update state.
					consumerState.Store(fmt.Sprintf(
						`{"Name":"consumer-1","ParentRef":"%s","Tags":[{"Key":"env","Value":"prod"}]}`,
						echoForm("producer-1")))
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

		producerSchema := pkgmodel.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "Value"},
			Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
		}
		consumerSchema := pkgmodel.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "ParentRef", "Tags"},
			Hints: map[string]pkgmodel.FieldHint{
				"Name":      {CreateOnly: true},
				"ParentRef": {CreateOnly: false},
				"Tags":      {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
			},
		}

		formaFor := func(tagsJSON string) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
				Resources: []pkgmodel.Resource{
					{
						Label:      "producer",
						Type:       "FakeAWS::Tagged::Producer",
						Properties: json.RawMessage(`{"Name":"producer-1","Value":"hello"}`),
						Stack:      "test-stack",
						Target:     "test-target",
						Managed:    true,
						Schema:     producerSchema,
					},
					{
						Label: "consumer",
						Type:  "FakeAWS::Tagged::Consumer",
						Properties: json.RawMessage(fmt.Sprintf(`{
							"Name": "consumer-1",
							"ParentRef": {
								"$res": true,
								"$label": "producer",
								"$type": "FakeAWS::Tagged::Producer",
								"$stack": "test-stack",
								"$property": "Name"
							},
							"Tags": %s
						}`, tagsJSON)),
						Stack:   "test-stack",
						Target:  "test-target",
						Managed: true,
						Schema:  consumerSchema,
					},
				},
				Targets: []pkgmodel.Target{{Label: "test-target"}},
			}
		}

		v1Tags := `[{"Key":"env","Value":"prod"},{"Key":"legacy","Value":"true"}]`
		v2Tags := `[{"Key":"env","Value":"prod"}]`

		_, err = m.ApplyForma(formaFor(v1Tags), &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test", "", "")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			fas, _ := m.Datastore.LoadFormaCommands()
			return len(fas) > 0 && (fas[0].State == forma_command.CommandStateSuccess || fas[0].State == forma_command.CommandStateFailed)
		}, 10*time.Second, 100*time.Millisecond, "v1 should reach terminal state")
		fas, _ := m.Datastore.LoadFormaCommands()
		require.Equal(t, forma_command.CommandStateSuccess, fas[0].State, "v1 apply should succeed")

		// v2 drops the "legacy" Tags member from the declaration while the
		// ParentRef reference to producer is unchanged (still requires a live
		// resolve at execution — see the executor's re-resolve-before-dispatch
		// path exercised by TestApplyForma_UnchangedReference_IsAbsentFromTheExecutedPatch).
		_, err = m.ApplyForma(formaFor(v2Tags), &config.FormaCommandConfig{
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
		patchStr := executed.(string)
		assert.Contains(t, patchStr, "remove", "the executed patch must carry the reconcile-planned removal")
		assert.Contains(t, patchStr, "Tags", "the removal must target the dropped Tags member")
	})
}
