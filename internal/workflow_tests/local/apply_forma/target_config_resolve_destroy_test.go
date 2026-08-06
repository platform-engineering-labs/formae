// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

// TestTargetConfigResolve_DestroyResolvesSecretBackedTarget is the acceptance test for
// the delete-path gap in synthetic Resolve synthesis: when a resource is destroyed on a
// target whose Config carries an opaque $ref, the resource-delete op must receive the
// resolved credential — not the raw $ref pointer — in its TargetConfig.
//
// Sequence:
//
//   - Apply: creates a secret, the consumer target (config $ref-ing the secret), and a
//     bucket resource on consumer. The stored consumer config retains only the raw $ref
//     pointer (StripOpaqueRefValues removes $value at the write boundary).
//
//   - Destroy: tears down the bucket, the consumer target, and the secret. Before the
//     fix, the consumer target's Delete TU marked it as "covered", suppressing the
//     synthetic Resolve op — so the bucket Delete received the unresolved $ref. After
//     the fix, the synthetic Resolve is synthesized even when a Delete TU exists, and
//     the bucket Delete dispatches with the plaintext credential.

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

// destroyTargetConfigCapture records the TargetConfig received on the bucket Delete call.
type destroyTargetConfigCapture struct {
	mu     sync.Mutex
	config json.RawMessage
}

func (c *destroyTargetConfigCapture) set(cfg json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make(json.RawMessage, len(cfg))
	copy(cp, cfg)
	c.config = cp
}

func (c *destroyTargetConfigCapture) get() json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.config
}

func TestTargetConfigResolve_DestroyResolvesSecretBackedTarget(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const plaintextSecret = "destroy-resolve-test-credential"

		capture := &destroyTargetConfigCapture{}

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(r *resource.CreateRequest) (*resource.CreateResult, error) {
				if r.ResourceType == "FakeAWS::S3::Bucket" {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:          resource.OperationCreate,
							OperationStatus:    resource.OperationStatusSuccess,
							RequestID:          "bucket-create-destroy-1",
							NativeID:           "test-bucket-destroy-001",
							ResourceProperties: r.Properties,
						},
					}, nil
				}
				return nil, nil
			},
			Delete: func(r *resource.DeleteRequest) (*resource.DeleteResult, error) {
				if r.ResourceType == "FakeAWS::S3::Bucket" {
					capture.set(r.TargetConfig)
				}
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationDelete,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "delete-1",
						NativeID:        r.NativeID,
					},
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		secretKsuid := "3NjE3sB2TKcMNHahUM1iDykljkw" // deterministic KSUID for the $ref
		stackSecrets := "stack-destroy-secrets-" + util.NewID()
		stackResources := "stack-destroy-resources-" + util.NewID()

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: stackSecrets},
				{Label: stackResources},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "my-secret",
					Type:       "FakeAWS::SecretsManager::Secret",
					Stack:      stackSecrets,
					Target:     "provider",
					Ksuid:      secretKsuid,
					Schema:     secretSchema(),
					Properties: json.RawMessage(`{"Name":"my-secret","SecretString":"` + plaintextSecret + `"}`),
				},
				{
					Label:  "my-bucket",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  stackResources,
					Target: "consumer",
					Schema: pkgmodel.Schema{
						Identifier: "BucketName",
						Fields:     []string{"BucketName"},
					},
					Properties: json.RawMessage(`{"BucketName":"test-destroy-bucket-001"}`),
				},
			},
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

		// ── Apply: stand up all three pieces ──────────────────────────────────────
		_, err = m.ApplyForma(forma, defaultApplyConfig(), "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		applyCmd := findNthApplyCommand(cmds, 1)
		require.NotNil(t, applyCmd, "apply command must exist")
		require.Equal(t, forma_command.CommandStateSuccess, applyCmd.State,
			"apply must succeed before the destroy can be tested")

		// Confirm stored consumer config retains only the $ref pointer.
		consumerTarget, err := m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		require.NotNil(t, consumerTarget)
		require.Contains(t, string(consumerTarget.Config), `"$ref"`)
		require.NotContains(t, string(consumerTarget.Config), plaintextSecret)

		// ── Destroy: tear down all three pieces ───────────────────────────────────
		//
		// consumer is included explicitly so it gets a Delete TargetUpdate in the
		// destroy command. Before the fix that Delete TU marked consumer "covered",
		// suppressing the synthetic Resolve — so the bucket Delete received the raw
		// $ref. After the fix the synthetic Resolve is always synthesized when the
		// persisted config carries opaque refs, regardless of the Delete TU.
		_, err = m.DestroyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		destroyCmd := findDestroyCommand(t, m)
		require.Equal(t, forma_command.CommandStateSuccess, destroyCmd.State,
			"destroy must complete successfully — a hang here indicates unresolved credentials")

		// ── Core assertion ────────────────────────────────────────────────────────
		//
		// The bucket Delete must have been called with the resolved credential,
		// not the raw {"$ref":…,"$visibility":"Opaque"} envelope.
		receivedConfig := capture.get()
		require.NotNil(t, receivedConfig,
			"FakeAWS::S3::Bucket Delete override must have been called during destroy")

		assert.NotContains(t, string(receivedConfig), `"$ref"`,
			"plugin Delete must receive a resolved TargetConfig — the raw $ref must not reach the plugin")
		assert.Contains(t, string(receivedConfig), plaintextSecret,
			"plugin Delete must receive the resolved credential value in TargetConfig")
	})
}
