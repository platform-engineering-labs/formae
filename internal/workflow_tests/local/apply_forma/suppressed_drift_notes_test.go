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
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// A hasProviderDefault field the user omits ("rotation") is set by the cloud
// at create time and flipped out of band afterwards. The suppressed movement
// must surface on every reconcile-mode response: as a disposition-remaining
// note on a no-changes apply (which absorbs nothing), and as a
// disposition-absorbed note persisted on a reconcile that executes with a
// co-located change (whose completion advances the drift window past the
// movement).
func TestApplyForma_SuppressedDriftNotes_EndToEnd(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		providerDefaultSchema := pkgmodel.Schema{
			Fields: []string{"foo", "rotation"},
			Hints:  map[string]pkgmodel.FieldHint{"rotation": {HasProviderDefault: true}},
		}

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				props := `{"foo":"bar","rotation":"off"}`
				if request.Label == "resource-two" {
					props = `{"foo":"new"}`
				}
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Label,
						ResourceProperties: json.RawMessage(props),
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				// The cloud state after the out-of-band flip.
				props := `{"foo":"bar","rotation":"on"}`
				if request.NativeID == "resource-two" {
					props = `{"foo":"new"}`
				}
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   props,
				}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		resourceOne := pkgmodel.Resource{
			Label:      "resource-one",
			Type:       "FakeAWS::Resource",
			Stack:      "drift-stack",
			Target:     "test-target1",
			Schema:     providerDefaultSchema,
			Properties: json.RawMessage(`{"foo":"bar"}`),
		}
		target := pkgmodel.Target{
			Label:     "test-target1",
			Namespace: "test-namespace1",
			Config:    json.RawMessage(`{}`),
		}
		initial := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "drift-stack"}},
			Resources: []pkgmodel.Resource{resourceOne},
			Targets:   []pkgmodel.Target{target},
		}

		_, err = m.ApplyForma(initial,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)

		var commands []*forma_command.FormaCommand
		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return len(commands) == 1 && allCommandsSuccessful(commands)
		}, 10*time.Second, 100*time.Millisecond, "initial reconcile should complete")

		// Sync absorbs the out-of-band flip and records the modification.
		require.NoError(t, m.ForceSync())
		require.Eventually(t, func() bool {
			drift, derr := m.ListDrift("drift-stack")
			assert.NoError(t, derr)
			return drift != nil && len(drift.ModifiedResources) == 1
		}, 10*time.Second, 100*time.Millisecond, "sync should record the OOB modification")

		// A simulate of the unchanged forma is a no-changes response that
		// carries the suppressed movement, disposition remaining.
		resp, err := m.ApplyForma(initial,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true},
			"test-client-id", "", "")
		require.NoError(t, err)
		assert.False(t, resp.Simulation.ChangesRequired)
		require.Len(t, resp.Simulation.SuppressedDrift, 1, "simulate must surface the suppressed movement")
		note := resp.Simulation.SuppressedDrift[0]
		assert.Equal(t, "drift-stack", note.Stack)
		assert.Equal(t, "resource-one", note.Label)
		assert.Equal(t, "rotation", note.Path)
		assert.JSONEq(t, `"off"`, string(note.From))
		assert.JSONEq(t, `"on"`, string(note.To))
		assert.Equal(t, "remaining", note.Disposition)

		// A non-simulate apply of the unchanged forma is a no-op: it
		// persists no command, absorbs nothing, and the drift stays in the
		// window.
		resp, err = m.ApplyForma(initial,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)
		assert.False(t, resp.Simulation.ChangesRequired)
		require.Len(t, resp.Simulation.SuppressedDrift, 1)
		assert.Equal(t, "remaining", resp.Simulation.SuppressedDrift[0].Disposition)

		commands, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Equal(t, 1, countApplyCommands(commands), "a no-op apply must not persist a command")

		drift, err := m.ListDrift("drift-stack")
		require.NoError(t, err)
		require.NotNil(t, drift)
		assert.Len(t, drift.ModifiedResources, 1, "a no-op apply must not advance the drift window")

		// A reconcile with a co-located change executes; its response and
		// its persisted command carry the note, disposition absorbed, and
		// its completion advances the window.
		withNewResource := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "drift-stack"}},
			Resources: []pkgmodel.Resource{
				resourceOne,
				{
					Label:      "resource-two",
					Type:       "FakeAWS::Resource",
					Stack:      "drift-stack",
					Target:     "test-target1",
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Properties: json.RawMessage(`{"foo":"new"}`),
				},
			},
			Targets: []pkgmodel.Target{target},
		}

		resp, err = m.ApplyForma(withNewResource,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)
		assert.True(t, resp.Simulation.ChangesRequired)
		require.Len(t, resp.Simulation.SuppressedDrift, 1, "the executing reconcile must carry the note")
		assert.Equal(t, "absorbed", resp.Simulation.SuppressedDrift[0].Disposition)

		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return countApplyCommands(commands) == 2 && allCommandsSuccessful(commands)
		}, 10*time.Second, 100*time.Millisecond, "co-located reconcile should complete")

		var absorbed *forma_command.FormaCommand
		for _, cmd := range commands {
			if len(cmd.SuppressedDriftNotes) > 0 {
				absorbed = cmd
			}
		}
		require.NotNil(t, absorbed, "the persisted command must carry the suppressed-drift note durably")
		require.Len(t, absorbed.SuppressedDriftNotes, 1)
		assert.Equal(t, "rotation", absorbed.SuppressedDriftNotes[0].Path)
		assert.Equal(t, forma_command.SuppressedDriftAbsorbed, absorbed.SuppressedDriftNotes[0].Disposition)

		require.Eventually(t, func() bool {
			drift, derr := m.ListDrift("drift-stack")
			assert.NoError(t, derr)
			return drift != nil && len(drift.ModifiedResources) == 0
		}, 10*time.Second, 100*time.Millisecond, "the completed reconcile advances the window past the drift")
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
