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

// A patch-mode submission never classifies drift: the suppressed-drift
// machinery is reconcile-only.
func TestApplyForma_SuppressedDriftNotes_PatchModeEmitsNone(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		schema := pkgmodel.Schema{
			Fields: []string{"foo", "rotation"},
			Hints:  map[string]pkgmodel.FieldHint{"rotation": {HasProviderDefault: true}},
		}
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
				return &resource.ReadResult{ResourceType: request.ResourceType, Properties: `{"foo":"bar","rotation":"on"}`}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.NativeID,
						ResourceProperties: json.RawMessage(`{"foo":"patched","rotation":"on"}`),
					},
				}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		target := pkgmodel.Target{Label: "test-target1", Namespace: "test-namespace1", Config: json.RawMessage(`{}`)}
		forma := func(foo string) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{{Label: "patch-stack"}},
				Resources: []pkgmodel.Resource{{
					Label: "resource-one", Type: "FakeAWS::Resource", Stack: "patch-stack",
					Target: "test-target1", Schema: schema,
					Properties: json.RawMessage(`{"foo":"` + foo + `"}`),
				}},
				Targets: []pkgmodel.Target{target},
			}
		}

		_, err = m.ApplyForma(forma("bar"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)
		var commands []*forma_command.FormaCommand
		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return countApplyCommands(commands) == 1 && allCommandsSuccessful(commands)
		}, 10*time.Second, 100*time.Millisecond)

		require.NoError(t, m.ForceSync())
		require.Eventually(t, func() bool {
			drift, derr := m.ListDrift("patch-stack")
			assert.NoError(t, derr)
			return drift != nil && len(drift.ModifiedResources) == 1
		}, 10*time.Second, 100*time.Millisecond)

		resp, err := m.ApplyForma(forma("patched"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch, Simulate: true},
			"test-client-id", "", "")
		require.NoError(t, err)
		assert.Empty(t, resp.Simulation.SuppressedDrift, "patch mode must not classify suppressed drift")
	})
}

// A forced reconcile proceeds past unabsorbed drift as it always has, and its
// command now records notes for the suppressed movement on absorbed
// modifications.
func TestApplyForma_SuppressedDriftNotes_ForcedReconcileCarriesNotes(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		schema := pkgmodel.Schema{
			Fields: []string{"foo", "rotation"},
			Hints:  map[string]pkgmodel.FieldHint{"rotation": {HasProviderDefault: true}},
		}
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				// Echo the requested foo; the cloud sets rotation at create.
				var req map[string]any
				_ = json.Unmarshal(request.Properties, &req)
				echoed, _ := json.Marshal(map[string]any{"foo": req["foo"], "rotation": "off"})
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Label,
						ResourceProperties: json.RawMessage(echoed),
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				// Both the declared field and the suppressed field moved OOB.
				return &resource.ReadResult{ResourceType: request.ResourceType, Properties: `{"foo":"oob","rotation":"on"}`}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.NativeID,
						ResourceProperties: json.RawMessage(`{"foo":"bar","rotation":"on"}`),
					},
				}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		target := pkgmodel.Target{Label: "test-target1", Namespace: "test-namespace1", Config: json.RawMessage(`{}`)}
		declared := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "force-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label: "drifting", Type: "FakeAWS::Resource", Stack: "force-stack",
					Target: "test-target1", Schema: schema,
					Properties: json.RawMessage(`{"foo":"bar"}`),
				},
				{
					// Declared at the value the cloud reports, so no update is
					// ever generated for it: its modification is the suppressed
					// rotation flip alone, which absorbs.
					Label: "quiet", Type: "FakeAWS::Resource", Stack: "force-stack",
					Target: "test-target1", Schema: schema,
					Properties: json.RawMessage(`{"foo":"oob"}`),
				},
			},
			Targets: []pkgmodel.Target{target},
		}

		_, err = m.ApplyForma(declared,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)
		var commands []*forma_command.FormaCommand
		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return countApplyCommands(commands) == 1 && allCommandsSuccessful(commands)
		}, 10*time.Second, 100*time.Millisecond)

		require.NoError(t, m.ForceSync())
		require.Eventually(t, func() bool {
			drift, derr := m.ListDrift("force-stack")
			assert.NoError(t, derr)
			return drift != nil && len(drift.ModifiedResources) >= 1
		}, 10*time.Second, 100*time.Millisecond)

		// Non-forced: the declared-field drift on "drifting" rejects.
		_, err = m.ApplyForma(declared,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.Error(t, err, "unabsorbed drift must still reject a non-forced reconcile")

		// Forced: proceeds, and the command carries suppressed-drift notes
		// for the absorbed modifications' suppressed movement.
		resp, err := m.ApplyForma(declared,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false, Force: true},
			"test-client-id", "", "")
		require.NoError(t, err)
		var paths []string
		for _, n := range resp.Simulation.SuppressedDrift {
			paths = append(paths, n.Label+"."+n.Path)
		}
		assert.Contains(t, paths, "quiet.rotation", "the absorbed resource's suppressed movement is noted on the forced apply")
	})
}

// Documents a pre-existing property of the drift-window anchor: the most
// recent reconcile command with a persisted resource row anchors the window
// regardless of the command's final state, so a partially failed reconcile
// still takes prior drift out of the window. The suppressed-drift note
// persisted with that failed command is then the surviving record of the
// movement.
func TestApplyForma_SuppressedDriftNotes_FailedReconcileStillAnchorsWindow(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		schema := pkgmodel.Schema{
			Fields: []string{"foo", "rotation"},
			Hints:  map[string]pkgmodel.FieldHint{"rotation": {HasProviderDefault: true}},
		}
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if request.Label == "failing" {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusFailure,
							NativeID:        request.Label,
						},
					}, nil
				}
				var req map[string]any
				_ = json.Unmarshal(request.Properties, &req)
				echoed, _ := json.Marshal(map[string]any{"foo": req["foo"], "rotation": "off"})
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Label,
						ResourceProperties: json.RawMessage(echoed),
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{ResourceType: request.ResourceType, Properties: `{"foo":"bar","rotation":"on"}`}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		target := pkgmodel.Target{Label: "test-target1", Namespace: "test-namespace1", Config: json.RawMessage(`{}`)}
		res := func(label string) pkgmodel.Resource {
			return pkgmodel.Resource{
				Label: label, Type: "FakeAWS::Resource", Stack: "anchor-stack",
				Target: "test-target1", Schema: schema,
				Properties: json.RawMessage(`{"foo":"bar"}`),
			}
		}

		initial := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "anchor-stack"}},
			Resources: []pkgmodel.Resource{res("steady")},
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
			return countApplyCommands(commands) == 1 && allCommandsSuccessful(commands)
		}, 10*time.Second, 100*time.Millisecond)

		require.NoError(t, m.ForceSync())
		require.Eventually(t, func() bool {
			drift, derr := m.ListDrift("anchor-stack")
			assert.NoError(t, derr)
			return drift != nil && len(drift.ModifiedResources) == 1
		}, 10*time.Second, 100*time.Millisecond)

		// A reconcile adding one succeeding and one failing resource: the
		// command fails, but the succeeding create persists a resource row,
		// which anchors the window.
		expanded := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "anchor-stack"}},
			Resources: []pkgmodel.Resource{res("steady"), res("added"), res("failing")},
			Targets:   []pkgmodel.Target{target},
		}
		resp, err := m.ApplyForma(expanded,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)
		require.Len(t, resp.Simulation.SuppressedDrift, 1, "the failing reconcile still records the note")

		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			for _, cmd := range commands {
				if cmd.Command == pkgmodel.CommandApply && cmd.State == forma_command.CommandStateFailed {
					return true
				}
			}
			return false
		}, 15*time.Second, 100*time.Millisecond, "the expanded reconcile should fail")

		// Pre-existing behavior, pinned: the failed reconcile anchors the
		// window and the prior drift record leaves it.
		require.Eventually(t, func() bool {
			drift, derr := m.ListDrift("anchor-stack")
			assert.NoError(t, derr)
			return drift != nil && len(drift.ModifiedResources) == 0
		}, 10*time.Second, 100*time.Millisecond, "a failed reconcile with a persisted resource row advances the window (pre-existing behavior)")

		// The durable record of the movement is the note on the failed
		// command.
		var noted *forma_command.FormaCommand
		for _, cmd := range commands {
			if len(cmd.SuppressedDriftNotes) > 0 {
				noted = cmd
			}
		}
		require.NotNil(t, noted)
		assert.Equal(t, "rotation", noted.SuppressedDriftNotes[0].Path)
	})
}
