// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

// TestTargetConfigResolve_UnchangedTargetWithOpaqueRef is the acceptance test for
// the "synthetic Resolve target op" gap: when a target's Config carries an opaque
// $ref and that target is NOT being created/updated in the changeset, the $ref must
// be resolved before resource ops dispatch to the plugin.
//
// Variant built: two-command sequence (full variant).
//
//   - Apply 1: creates the secret resource (FakeAWS::SecretsManager::Secret) and
//     the consumer target, whose Config carries an opaque $ref to the secret's
//     SecretString. Because consumer IS new in apply 1, a TargetUpdate fires and
//     propagateResolvedTargetConfig resolves the $ref for that command's resource
//     ops. The stored Config retains the raw $ref; the resolved $value is stripped
//     at the write boundary (StripOpaqueRefValues), so the DB holds only the pointer.
//
//   - Apply 2: creates a bucket resource (FakeAWS::S3::Bucket) on the consumer
//     target. consumer is NOT listed in apply 2's Targets — GenerateTargetUpdates
//     only processes declared targets and emits no TargetUpdate for consumer.
//     consumer IS loaded from the DB into desiredTargetMap (so the resource
//     reference resolves), but ConvertToPluginFormat cannot recover the resolved
//     credential (no $value in the stored opaque config). Without the synthetic
//     Resolve op, the bucket Create reaches the plugin with the raw
//     {"$ref":…,"$visibility":"Opaque"} object still in TargetConfig instead of
//     the plaintext credential.
//
// The synthetic Resolve op wires up the resolution so the
// bucket Create receives the plaintext credential in its TargetConfig.

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// targetConfigResolveCapture records the TargetConfig the plugin receives on
// the bucket Create call in apply 2.
type targetConfigResolveCapture struct {
	mu     sync.Mutex
	config json.RawMessage
}

func (c *targetConfigResolveCapture) set(cfg json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make(json.RawMessage, len(cfg))
	copy(cp, cfg)
	c.config = cp
}

func (c *targetConfigResolveCapture) get() json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.config
}

// findNthApplyCommand returns the n-th (1-based) FormaCommand with Command ==
// CommandApply, or nil if fewer than n apply commands exist.
func findNthApplyCommand(cmds []*forma_command.FormaCommand, n int) *forma_command.FormaCommand {
	count := 0
	for _, c := range cmds {
		if c.Command == pkgmodel.CommandApply {
			count++
			if count == n {
				return c
			}
		}
	}
	return nil
}

func TestTargetConfigResolve_UnchangedTargetWithOpaqueRef(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const plaintextSecret = "target-config-resolve-test-credential"

		capture := &targetConfigResolveCapture{}

		// Create override: capture TargetConfig only for the bucket Create in apply 2.
		// For all other resource types (SecretsManager::Secret in apply 1) return nil
		// so the default FakeAWS handler runs.
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(r *resource.CreateRequest) (*resource.CreateResult, error) {
				if r.ResourceType == "FakeAWS::S3::Bucket" {
					capture.set(r.TargetConfig)
					// Synchronous success: keeps apply 2 deterministic (no Status poll).
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:          resource.OperationCreate,
							OperationStatus:    resource.OperationStatusSuccess,
							RequestID:          "bucket-create-1",
							NativeID:           "test-bucket-001",
							ResourceProperties: r.Properties,
						},
					}, nil
				}
				// Fall through to the default FakeAWS handler.
				return nil, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		secretKsuid := "2MiD2rA1SJbLMGZgTL0hCxjkjjv" // deterministic KSUID for the $ref
		stackOne := "stack-secrets-" + util.NewID()
		stackTwo := "stack-resources-" + util.NewID()

		// ── Apply 1: create the secret and the consumer target ────────────────────
		//
		// consumer's Config has an opaque $ref to the secret's SecretString.
		// Because consumer is a NEW target in this apply, a TargetUpdate fires.
		// The changeset executor's propagateResolvedTargetConfig resolves the $ref
		// for any resource ops in this command. The datastore strips the resolved
		// $value before persisting (StripOpaqueRefValues), so the stored config
		// retains only the raw $ref pointer (no $value).
		applyOne := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stackOne}},
			Resources: []pkgmodel.Resource{{
				Label:      "my-secret",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      stackOne,
				Target:     "provider",
				Ksuid:      secretKsuid,
				Schema:     secretSchema(),
				Properties: json.RawMessage(`{"Name":"my-secret","SecretString":"` + plaintextSecret + `"}`),
			}},
			Targets: []pkgmodel.Target{
				{Label: "provider", Namespace: "test-namespace"},
				{
					Label:     "consumer",
					Namespace: "test-namespace",
					Config: json.RawMessage(fmt.Sprintf(`{
						"apiKey": {"$ref": "formae://%s#/SecretString", "$visibility": "Opaque"}
					}`, secretKsuid)),
				},
			},
		}

		_, err = m.ApplyForma(applyOne, defaultApplyConfig(), "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		firstApply := findNthApplyCommand(cmds, 1)
		require.NotNil(t, firstApply, "first apply command must exist")
		require.Equal(t, forma_command.CommandStateSuccess, firstApply.State,
			"first apply (create secret + consumer target) must succeed")

		// Verify the consumer target is stored with the raw $ref pointer and no
		// resolved $value (StripOpaqueRefValues drops $value for opaque refs).
		consumerTarget, err := m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		require.NotNil(t, consumerTarget, "consumer target must have been persisted")
		require.Contains(t, string(consumerTarget.Config), `"$ref"`,
			"stored consumer config must retain the $ref pointer")
		require.NotContains(t, string(consumerTarget.Config), plaintextSecret,
			"stored consumer config must not contain the plaintext credential")

		// ── Apply 2: new bucket on consumer, no TargetUpdate for consumer ─────────
		//
		// consumer is NOT listed in apply 2's Targets. GenerateTargetUpdates only
		// iterates forma.Targets, so it emits no TargetUpdate for consumer.
		// consumer IS in existingTargetMap (loaded from DB) and therefore in
		// desiredTargetMap, but its Config has had only ConvertToPluginFormat applied
		// (resource_update_generator.go lines 73-79). Because the stored opaque config
		// has no $value, ConvertToPluginFormat cannot recover the credential and leaves
		// the raw {"$ref":…,"$visibility":"Opaque"} object in place.
		//
		// Without the synthetic Resolve op, propagateResolvedTargetConfig never fires
		// for consumer (no TargetUpdate), so the bucket Create dispatches with the
		// unresolved $ref still in TargetConfig.
		applyTwo := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stackTwo}},
			Resources: []pkgmodel.Resource{{
				Label:  "r1",
				Type:   "FakeAWS::S3::Bucket",
				Stack:  stackTwo,
				Target: "consumer",
				Schema: pkgmodel.Schema{
					Identifier: "BucketName",
					Fields:     []string{"BucketName"},
				},
				Properties: json.RawMessage(`{"BucketName":"test-bucket-001"}`),
			}},
			// consumer is intentionally absent from Targets so no TargetUpdate fires.
			// The system resolves the target from the datastore (existingTargetMap).
			Targets: []pkgmodel.Target{},
		}

		_, err = m.ApplyForma(applyTwo, defaultApplyConfig(), "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		secondApply := findNthApplyCommand(cmds, 2)
		require.NotNil(t, secondApply, "second apply command must exist")
		require.Equal(t, forma_command.CommandStateSuccess, secondApply.State,
			"second apply (create bucket on consumer) must succeed")

		// ── Core assertion ────────────────────────────────────────────────────────
		//
		// The bucket Create override must have been called with a TargetConfig that
		// carries the RESOLVED credential — not the raw {"$ref":…} object.
		//
		//   Before the fix: TargetConfig = {"apiKey":{"$ref":"formae://…","$visibility":"Opaque"}}
		//   After the fix:  TargetConfig = {"apiKey":"target-config-resolve-test-credential"}
		receivedConfig := capture.get()
		require.NotNil(t, receivedConfig,
			"FakeAWS::S3::Bucket Create override must have been called during apply 2")

		assert.NotContains(t, string(receivedConfig), `"$ref"`,
			"plugin Create must receive a resolved TargetConfig — the raw $ref object must not reach the plugin")

		assert.Contains(t, string(receivedConfig), plaintextSecret,
			"plugin Create must receive the resolved credential value in TargetConfig")
	})
}

// defaultApplyConfig returns a standard reconcile FormaCommandConfig for tests.
func defaultApplyConfig() *config.FormaCommandConfig {
	return &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}
}
