// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// A secret's value has three authorship modes: seeded and enforced, seeded
// once, and not seeded (the value is authored outside formae, which is what an
// imported secret looks like). The tests below pin what happens on the
// transitions between them, because the transitions are where a live
// credential could be destroyed by accident.
//
// The governing rule is that there is NO "clear the value" operation. Omitting
// a write-only value means "don't touch", never "remove"; a secret whose seed
// is removed becomes not-seeded; and getting rid of a value means destroying
// the secret, which its consumers see as a resolve error.

// secretUpdateRecorder captures what a secret's provider is asked to do, so a
// test can assert on the write inputs rather than only on stored state.
type secretUpdateRecorder struct {
	calls  int
	patch  string
	prior  json.RawMessage
	sought json.RawMessage
}

func recordingSecretOverrides(name string, rec *secretUpdateRecorder) *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		// Write-only: the provider never returns the value on a read, which is
		// what makes "omitted" and "unchanged" indistinguishable from state
		// alone and therefore worth pinning.
		Read: nonEnrichingSecretRead(name),
		Update: func(r *resource.UpdateRequest) (*resource.UpdateResult, error) {
			rec.calls++
			rec.prior = r.PriorProperties
			rec.sought = r.DesiredProperties
			if r.PatchDocument != nil {
				rec.patch = *r.PatchDocument
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

func secretResource(stack, name, properties string) pkgmodel.Resource {
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
			formaOf(stack, secretResource(stack, name, `{"Name":"`+name+`","SecretString":"`+seeded+`"}`)),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		// Re-apply with the value line gone, changing an unrelated field so
		// there is a genuine update to carry the omission.
		_, err = m.ApplyForma(
			formaOf(stack, secretResource(stack, name, `{"Name":"`+name+`","Description":"now-unseeded"}`)),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForCommands(t, m, 2)

		require.Equal(t, 1, rec.calls, "the unrelated change must reach the provider")
		assert.NotContains(t, rec.patch, "/SecretString",
			"an omitted write-only value must produce no operation on that field: %s", rec.patch)
		assert.NotContains(t, rec.patch, `"op":"remove"`,
			"an omitted value must never be removed: %s", rec.patch)
		assert.NotContains(t, string(rec.sought), seeded,
			"the omitted value must not be resubmitted either")
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
		unseeded := secretResource(stack, name, `{"Name":"`+name+`","Description":"unseeded"}`)

		_, err = m.ApplyForma(
			formaOf(stack, secretResource(stack, name, `{"Name":"`+name+`","SecretString":"seeded-value-v1"}`)),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		_, err = m.ApplyForma(formaOf(stack, unseeded),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForCommands(t, m, 2)
		callsAfterUnseeding := rec.calls

		// Apply the same unseeded forma again. Nothing should be asked of the
		// provider: the value is no longer formae's to enforce.
		_, err = m.ApplyForma(formaOf(stack, unseeded),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		assert.Equal(t, callsAfterUnseeding, rec.calls,
			"re-applying an unseeded secret must not touch the provider again")
	})
}

// TestSecretValueLifecycle_DestroyingAReferencedSecretFailsItsConsumer asserts
// that getting rid of a secret's value is done by destroying the secret, and
// that a consumer still pointing at it fails loudly rather than silently
// carrying on with a reference to something that no longer exists.
func TestSecretValueLifecycle_DestroyingAReferencedSecretFailsItsConsumer(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const name = "my-secret"

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				if req.ResourceType == "FakeAWS::S3::Bucket" {
					return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
						Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess,
						RequestID: "consumer-create-1", NativeID: "bucket-native-1",
					}}, nil
				}
				return nil, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		secret := secretResource(stack, name, `{"Name":"`+name+`","SecretString":"seeded-value-v1"}`)
		consumer := pkgmodel.Resource{
			Label:  "my-bucket",
			Type:   "FakeAWS::S3::Bucket",
			Stack:  stack,
			Target: "test-target",
			Schema: pkgmodel.Schema{Identifier: "BucketName", Fields: []string{"BucketName", "DbPassword"}},
			Properties: json.RawMessage(`{"BucketName":"my-bucket","DbPassword":{"$res":true,` +
				`"$label":"` + name + `","$type":"FakeAWS::SecretsManager::Secret","$stack":"` + stack +
				`","$property":"SecretString","$visibility":"Opaque"}}`),
		}

		_, err = m.ApplyForma(formaOf(stack, secret, consumer),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		// Reconcile with the secret gone from the forma, so it is destroyed,
		// while the consumer still references it.
		_, err = m.ApplyForma(formaOf(stack, consumer),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err,
			"the apply is admitted; the dangling reference surfaces during execution, not at admission")
		waitForCommands(t, m, 2)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)

		var destroyingApply *forma_command.FormaCommand
		for _, c := range cmds {
			for _, ru := range c.ResourceUpdates {
				if ru.Operation == resource_update.OperationDelete && ru.DesiredState.Label == name {
					destroyingApply = c
				}
			}
		}
		require.NotNil(t, destroyingApply, "the secret should have been planned for deletion")

		assert.Equal(t, forma_command.CommandStateFailed, destroyingApply.State,
			"destroying a secret that is still referenced must fail the command, not report success")

		var consumerUpdate *resource_update.ResourceUpdate
		for i, ru := range destroyingApply.ResourceUpdates {
			if ru.DesiredState.Label == "my-bucket" {
				consumerUpdate = &destroyingApply.ResourceUpdates[i]
			}
		}
		require.NotNil(t, consumerUpdate, "the consumer should have been drawn into the command")
		assert.Equal(t, resource_update.ResourceUpdateStateFailed, consumerUpdate.State,
			"the consumer's reference to a destroyed secret must fail to resolve")

	})
}
