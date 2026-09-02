// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// A resource update that fails at execution-time reference resolution (no
// plugin operation ever runs, so no progress entry carries a message) must
// surface its failure reason on the command loaded from the datastore, not
// come back with a blank error.
func TestApplyForma_TerminalResolveFailure_PersistsFailureReason(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       req.Label + "-create",
					NativeID:        req.Label,
					// The source never reports the referenced field, so the
					// consumer's live resolution cannot succeed.
					ResourceProperties: json.RawMessage(`{"Name":"` + req.Label + `"}`),
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"Name":"` + req.NativeID + `"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		schema := pkgmodel.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "Token", "SourceToken"},
			Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
		}
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label: "source", Type: "FakeAWS::Resolve::Source", Stack: "test-stack", Target: "test-target",
					Managed: true, Schema: schema,
					Properties: json.RawMessage(`{"Name":"source"}`),
				},
				{
					Label: "consumer", Type: "FakeAWS::Resolve::Consumer", Stack: "test-stack", Target: "test-target",
					Managed: true, Schema: schema,
					Properties: json.RawMessage(`{
						"Name": "consumer",
						"SourceToken": {
							"$res": true,
							"$label": "source",
							"$type": "FakeAWS::Resolve::Source",
							"$stack": "test-stack",
							"$property": "Token"
						}
					}`),
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target"}},
		}

		_, err = m.ApplyForma(forma, defaultApplyConfig(), "test", "", "")
		require.NoError(t, err)

		var cmds []*forma_command.FormaCommand
		require.Eventually(t, func() bool {
			cmds, err = m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			return len(cmds) > 0 && cmds[0].State != forma_command.CommandStateInProgress
		}, 60*time.Second, 200*time.Millisecond, "the apply must reach a terminal state")

		var consumerUpdate *resource_update.ResourceUpdate
		for i := range cmds[0].ResourceUpdates {
			if cmds[0].ResourceUpdates[i].DesiredState.Label == "consumer" {
				consumerUpdate = &cmds[0].ResourceUpdates[i]
			}
		}
		require.NotNil(t, consumerUpdate)
		require.Equal(t, resource_update.ResourceUpdateStateFailed, consumerUpdate.State,
			"the consumer's live resolution must fail terminally")
		require.NotEmpty(t, consumerUpdate.MostRecentFailureMessage(),
			"the resolve failure's reason must survive into the persisted command")
	})
}
