// © 2026 Platform Engineering Labs Inc.
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

// Out-of-band movement of a value formae's own write witnessed is drift,
// exactly like drift on a declared field: a soft reconcile rejects showing
// it, and a forced reconcile reverts it to the witnessed value. A user who
// omits a provider-defaulted property may be relying on the default
// deliberately; the default the cloud chose at create is the state formae
// defends until the forma says otherwise.
func TestApplyForma_WitnessedDrift_RejectsSoftRevertsForced(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		schema := pkgmodel.Schema{
			Fields: []string{"foo", "rotation"},
			Hints:  map[string]pkgmodel.FieldHint{"rotation": {HasProviderDefault: true}},
		}

		rotationState := "off"
		var lastUpdatePatch string
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Label,
						ResourceProperties: json.RawMessage(`{"foo":"bar","rotation":"off"}`),
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"bar","rotation":"` + rotationState + `"}`,
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				if request.PatchDocument != nil {
					lastUpdatePatch = *request.PatchDocument
				}
				rotationState = "off" // the revert lands in the cloud
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.NativeID,
						ResourceProperties: json.RawMessage(`{"foo":"bar","rotation":"off"}`),
					},
				}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		target := pkgmodel.Target{Label: "test-target1", Namespace: "test-namespace1", Config: json.RawMessage(`{}`)}
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "wd-stack"}},
			Resources: []pkgmodel.Resource{{
				Label: "resource-one", Type: "FakeAWS::Resource", Stack: "wd-stack",
				Target: "test-target1", Schema: schema,
				Properties: json.RawMessage(`{"foo":"bar"}`),
			}},
			Targets: []pkgmodel.Target{target},
		}

		_, err = m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)
		var commands []*forma_command.FormaCommand
		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return countApplyCommands(commands) == 1 && allCommandsSuccessful(commands)
		}, 10*time.Second, 100*time.Millisecond, "initial reconcile should complete")

		// The out-of-band flip, absorbed by sync.
		rotationState = "on"
		require.NoError(t, m.ForceSync())
		require.Eventually(t, func() bool {
			drift, derr := m.ListDrift("wd-stack")
			assert.NoError(t, derr)
			return drift != nil && len(drift.ModifiedResources) == 1
		}, 10*time.Second, 100*time.Millisecond, "sync should record the OOB modification")

		// Soft reconcile of the unchanged forma: rejected, showing the drift,
		// exactly like declared drift.
		_, err = m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.Error(t, err, "witnessed movement must reject a soft reconcile")
		reject, ok := err.(apimodel.FormaReconcileRejectedError)
		if !ok {
			rejectPtr, okPtr := err.(*apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError])
			require.True(t, okPtr, "expected a reconcile rejection, got %T: %v", err, err)
			reject = rejectPtr.Data
		}
		require.Contains(t, reject.ModifiedStacks, "wd-stack")
		require.Len(t, reject.ModifiedStacks["wd-stack"].ModifiedResources, 1)

		// Simulate is rejected the same way (simulate does not bypass the
		// drift discipline).
		_, err = m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true},
			"test-client-id", "", "")
		require.Error(t, err, "simulate confronts the same drift")

		// Forced reconcile: overwrites the drift back to the witnessed value.
		_, err = m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false, Force: true},
			"test-client-id", "", "")
		require.NoError(t, err, "force proceeds past the drift")

		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return countApplyCommands(commands) == 2 && allCommandsSuccessful(commands)
		}, 10*time.Second, 100*time.Millisecond, "forced reconcile should complete")

		assert.Contains(t, lastUpdatePatch, "rotation", "the forced update reverts the witnessed field")
		assert.Contains(t, lastUpdatePatch, "off", "the revert asserts the witnessed value")

		require.Eventually(t, func() bool {
			drift, derr := m.ListDrift("wd-stack")
			assert.NoError(t, derr)
			return drift != nil && len(drift.ModifiedResources) == 0
		}, 10*time.Second, 100*time.Millisecond, "the revert clears the drift window")
	})
}

// Values formae never wrote stay the infrastructure's business: runtime
// churn on a never-witnessed collection neither rejects nor reverts, and a
// no-op apply of the unchanged forma stays a clean no-op.
func TestApplyForma_UnwitnessedChurn_NeitherRejectsNorReverts(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		schema := pkgmodel.Schema{
			Fields: []string{"foo", "targets"},
			Hints:  map[string]pkgmodel.FieldHint{"targets": {HasProviderDefault: true}},
		}

		targetsState := `[]`
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Label,
						ResourceProperties: json.RawMessage(`{"foo":"bar","targets":[]}`),
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"bar","targets":` + targetsState + `}`,
				}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		target := pkgmodel.Target{Label: "test-target1", Namespace: "test-namespace1", Config: json.RawMessage(`{}`)}
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "churn-stack"}},
			Resources: []pkgmodel.Resource{{
				Label: "tg", Type: "FakeAWS::Resource", Stack: "churn-stack",
				Target: "test-target1", Schema: schema,
				Properties: json.RawMessage(`{"foo":"bar"}`),
			}},
			Targets: []pkgmodel.Target{target},
		}

		_, err = m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)
		var commands []*forma_command.FormaCommand
		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return countApplyCommands(commands) == 1 && allCommandsSuccessful(commands)
		}, 10*time.Second, 100*time.Millisecond)

		// Runtime registration arrives through sync.
		targetsState = `[{"Id":"10.0.0.5","Port":80}]`
		require.NoError(t, m.ForceSync())
		require.Eventually(t, func() bool {
			drift, derr := m.ListDrift("churn-stack")
			assert.NoError(t, derr)
			return drift != nil && len(drift.ModifiedResources) == 1
		}, 10*time.Second, 100*time.Millisecond)

		// Soft reconcile passes: nothing witnessed moved, the churn is not
		// user drift.
		resp, err := m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err, "unwitnessed churn must not reject")
		assert.False(t, resp.Simulation.ChangesRequired)

		commands, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Equal(t, 1, countApplyCommands(commands), "a no-op apply persists no command")
	})
}

func countApplyCommands(commands []*forma_command.FormaCommand) int {
	n := 0
	for _, cmd := range commands {
		if cmd.Command == pkgmodel.CommandApply {
			n++
		}
	}
	return n
}
