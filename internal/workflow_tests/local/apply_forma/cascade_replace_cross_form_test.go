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
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestApplyForma_ParentReplace_CrossFormRef_DependentIsRepointed covers a
// dependent whose provider echoes the reference in a DIFFERENT FORM than the
// one the reference resolves to (an identifier spelled as an ARN on read but
// as a bare name on write). That combination is what puts an applied-baseline
// marker on the dependent's stored envelope, and it is the case where the
// dependent's own property diff compares equal and therefore plans nothing.
//
// The dependent must still be repointed within the same apply when the
// referenced resource is replaced: the planner reaches it through the
// dependency pass rather than its own diff, and the executor re-resolves the
// reference after the replacement completes.
//
// This guards behavior that already holds: the dependency pass is what keeps
// the repointing correct once a dependent's own diff falls silent. It is here
// because nothing else pins that interaction, and a change to either half
// would otherwise strand dependents pointing at a resource that no longer
// exists.
func TestApplyForma_ParentReplace_CrossFormRef_DependentIsRepointed(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// The parent's identity as the cloud reports it; flipped between the
		// two applies so the executor's re-resolution observes the new one.
		var parentName atomic.Value
		parentName.Store("parent-v1")

		// The form the provider echoes for the dependent's reference field.
		// It never equals the form the reference resolves to, which is the
		// whole point of this fixture.
		echoForm := func(name string) string { return "arn:fake:parent/" + name }

		var consumerUpdatePatch atomic.Value

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				props := json.RawMessage(fmt.Sprintf(`{"Name":"%s","Value":"hello"}`,
					parentName.Load().(string)))
				if req.ResourceType == "FakeAWS::Formed::Consumer" {
					// The provider reports the reference in its own spelling,
					// which is never the spelling the reference resolves to.
					props = json.RawMessage(fmt.Sprintf(`{"Name":"consumer-1","ParentRef":"%s"}`,
						echoForm(parentName.Load().(string))))
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
				case "FakeAWS::Formed::Parent":
					name := parentName.Load().(string)
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   fmt.Sprintf(`{"Name":"%s","Value":"hello"}`, name),
					}, nil
				case "FakeAWS::Formed::Consumer":
					// Pre-update cloud state: the dependent still carries the
					// echoed form of the OLD parent identity, because its own
					// Update has not fired yet.
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties: fmt.Sprintf(`{"Name":"consumer-1","ParentRef":"%s"}`,
							echoForm("parent-v1")),
					}, nil
				}
				return nil, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				if req.ResourceType == "FakeAWS::Formed::Consumer" && req.PatchDocument != nil {
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

		consumerProperties := json.RawMessage(`{
			"Name": "consumer-1",
			"ParentRef": {
				"$res": true,
				"$label": "parent",
				"$type": "FakeAWS::Formed::Parent",
				"$stack": "test-stack",
				"$property": "Name"
			}
		}`)

		formaFor := func(parentValue string) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
				Resources: []pkgmodel.Resource{
					{
						Label:      "parent",
						Type:       "FakeAWS::Formed::Parent",
						Properties: json.RawMessage(fmt.Sprintf(`{"Name":"%s","Value":"hello"}`, parentValue)),
						Stack:      "test-stack",
						Target:     "test-target",
						Managed:    true,
						Schema:     parentSchema,
					},
					{
						Label:      "consumer",
						Type:       "FakeAWS::Formed::Consumer",
						Properties: consumerProperties,
						Stack:      "test-stack",
						Target:     "test-target",
						Managed:    true,
						Schema:     consumerSchema,
					},
				},
				Targets: []pkgmodel.Target{{Label: "test-target"}},
			}
		}

		_, err = m.ApplyForma(formaFor("parent-v1"), &config.FormaCommandConfig{
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

		// Guard the fixture itself: the dependent's stored envelope must hold
		// the echoed form as its value and the written form as its applied
		// baseline. Without both, the scenario under test is not set up and
		// the assertions below would pass for the wrong reason.
		stored, err := m.Datastore.LoadResourcesByStack("test-stack")
		require.NoError(t, err)
		var consumerStored *pkgmodel.Resource
		for _, r := range stored {
			if r.Label == "consumer" {
				consumerStored = r
			}
		}
		require.NotNil(t, consumerStored, "consumer must be stored after v1")
		envelope := gjson.GetBytes(consumerStored.Properties, "ParentRef")
		require.Equal(t, echoForm("parent-v1"), envelope.Get("$value").String(),
			"fixture guard: stored value must be the echoed form")
		require.Equal(t, "parent-v1", envelope.Get("$applied").String(),
			"fixture guard: stored envelope must carry the written form as its applied baseline")

		// v2: the parent's CreateOnly identity changes, forcing a replacement.
		parentName.Store("parent-v2")

		_, err = m.ApplyForma(formaFor("parent-v2"), &config.FormaCommandConfig{
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

		var parentOps, consumerOps []resource_update.OperationType
		var consumerRU *resource_update.ResourceUpdate
		for i, ru := range fas[0].ResourceUpdates {
			switch ru.DesiredState.Label {
			case "parent":
				parentOps = append(parentOps, ru.Operation)
			case "consumer":
				consumerOps = append(consumerOps, ru.Operation)
				consumerRU = &fas[0].ResourceUpdates[i]
			}
		}
		assert.Len(t, parentOps, 2, "parent should be replaced (delete + create)")
		assert.Contains(t, parentOps, resource_update.OperationDelete)
		assert.Contains(t, parentOps, resource_update.OperationCreate)
		require.Len(t, consumerOps, 1, "dependent must be repointed within the same apply")
		assert.Equal(t, resource_update.OperationUpdate, consumerOps[0])
		// The dependent's own property diff compares equal here (its reference
		// resolves to the same value it last applied), so the update it gets
		// must come from the dependency pass. Asserting the provenance of the
		// update keeps this test honest: without the flag, a dependent that
		// planned an update for some unrelated reason would satisfy the count
		// above and hide a genuine repointing failure.
		require.NotNil(t, consumerRU)
		assert.True(t, consumerRU.IsCascade,
			"the dependent's update must come from the dependency pass, not its own diff")
		assert.Equal(t, "parent", consumerRU.CascadeSource)

		patch := consumerUpdatePatch.Load()
		require.NotNil(t, patch, "the provider must receive an Update for the dependent")
		assert.Contains(t, fmt.Sprintf("%s", patch), "parent-v2",
			"the patch handed to the provider must carry the replaced parent's new identity")
	})
}
