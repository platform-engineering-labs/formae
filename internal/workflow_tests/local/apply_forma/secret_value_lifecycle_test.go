// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// These tests guard the enforced-seed to omitted-value transition for a
// write-only secret. Omission must not clear or resubmit the value, and a
// subsequent apply must settle without another provider write.
//
// SetOnce transitions and deletion of a source with surviving consumers are
// separate contracts and are not covered here.

// secretUpdateRecorder captures what a secret's provider is asked to do, so a
// test can assert on the write inputs rather than only on stored state.
type secretUpdateRecorder struct {
	calls  atomic.Int32
	patch  atomic.Value // string
	sought atomic.Value // json.RawMessage
}

func recordingSecretOverrides(name string, rec *secretUpdateRecorder) *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		// Write-only: the provider never returns the value on a read, which is
		// what makes "omitted" and "unchanged" indistinguishable from state
		// alone and therefore worth pinning.
		Read: nonEnrichingSecretRead(name),
		Update: func(r *resource.UpdateRequest) (*resource.UpdateResult, error) {
			rec.calls.Add(1)
			rec.sought.Store(append(json.RawMessage(nil), r.DesiredProperties...))
			if r.PatchDocument != nil {
				rec.patch.Store(*r.PatchDocument)
			}
			return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
				Operation:          resource.OperationUpdate,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "update-1",
				NativeID:           "5678",
				ResourceProperties: r.DesiredProperties,
			}}, nil
		},
	}
}

func lifecycleSecretResource(stack, name, properties string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:      name,
		Type:       "FakeAWS::SecretsManager::Secret",
		Stack:      stack,
		Target:     "test-target",
		Schema:     secretSchema(),
		Properties: json.RawMessage(properties),
	}
}

func formaOf(stack string, resources ...pkgmodel.Resource) *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: stack}},
		Resources: resources,
		Targets:   []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
	}
}

// requireLifecycleCommandsSucceeded prevents a failed apply from looking like
// a quiet provider when these tests assert that no further writes occur.
func requireLifecycleCommandsSucceeded(t *testing.T, m *metastructure.Metastructure, count int) {
	t.Helper()
	waitForCommands(t, m, count)
	commands, err := m.Datastore.LoadFormaCommands()
	require.NoError(t, err)
	require.Len(t, commands, count)
	for _, command := range commands {
		require.Equal(t, forma_command.CommandStateSuccess, command.State)
	}
}

// TestSecretValueLifecycle_OmittedValueIsNeverRemoved asserts that dropping a
// secret's value from the forma does not clear it in the cloud.
//
// Under reconcile, a field absent from the forma is normally removed. For a
// write-only secret that would destroy a live credential on the next apply of
// a forma that simply stopped restating it, so the rule is the opposite: an
// omitted value means don't touch.
func TestSecretValueLifecycle_OmittedValueIsNeverRemoved(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const name = "my-secret"
		const seeded = "seeded-value-v1"

		var rec secretUpdateRecorder
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, recordingSecretOverrides(name, &rec), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()

		_, err = m.ApplyForma(
			formaOf(stack, lifecycleSecretResource(stack, name, `{"Name":"`+name+`","SecretString":"`+seeded+`"}`)),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		requireLifecycleCommandsSucceeded(t, m, 1)

		// Re-apply with the value line gone, changing an unrelated field so
		// there is a genuine update to carry the omission.
		_, err = m.ApplyForma(
			formaOf(stack, lifecycleSecretResource(stack, name, `{"Name":"`+name+`","Description":"now-unseeded"}`)),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		requireLifecycleCommandsSucceeded(t, m, 2)

		require.Equal(t, int32(1), rec.calls.Load(), "the unrelated change must reach the provider")
		patch, ok := rec.patch.Load().(string)
		require.True(t, ok, "the provider update must include a patch")
		require.Contains(t, patch, "/Description", "the unrelated change must appear in the patch")
		assert.NotContains(t, patch, "/SecretString",
			"an omitted write-only value must produce no operation on that field: %s", patch)
		assert.NotContains(t, patch, `"op":"remove"`,
			"an omitted value must never be removed: %s", patch)
		sought, ok := rec.sought.Load().(json.RawMessage)
		require.True(t, ok)
		var properties map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(sought, &properties))
		assert.NotContains(t, properties, "SecretString", "the omitted value must not be resubmitted in any form")
	})
}

// TestSecretValueLifecycle_UnseededSecretStaysQuiet asserts that a secret whose
// seed was removed settles, rather than drifting on every subsequent apply.
//
// Once the value is authored outside formae there is nothing to enforce, so a
// re-apply of the same forma must ask the provider for nothing at all.
func TestSecretValueLifecycle_UnseededSecretStaysQuiet(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const name = "my-secret"

		var rec secretUpdateRecorder
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, recordingSecretOverrides(name, &rec), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		unseeded := lifecycleSecretResource(stack, name, `{"Name":"`+name+`","Description":"unseeded"}`)

		_, err = m.ApplyForma(
			formaOf(stack, lifecycleSecretResource(stack, name, `{"Name":"`+name+`","SecretString":"seeded-value-v1"}`)),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		requireLifecycleCommandsSucceeded(t, m, 1)

		_, err = m.ApplyForma(formaOf(stack, unseeded),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		requireLifecycleCommandsSucceeded(t, m, 2)
		callsAfterUnseeding := rec.calls.Load()
		require.Equal(t, int32(1), callsAfterUnseeding, "unseeding must update the unrelated field")

		// Apply the same unseeded forma again. Nothing should be asked of the
		// provider: the value is no longer formae's to enforce.
		response, err := m.ApplyForma(formaOf(stack, unseeded),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		require.NotNil(t, response)
		require.False(t, response.Simulation.ChangesRequired, "repeat apply must be a no-op")
		requireLifecycleCommandsSucceeded(t, m, 2)

		assert.Equal(t, callsAfterUnseeding, rec.calls.Load(),
			"re-applying an unseeded secret must not touch the provider again")
	})
}
