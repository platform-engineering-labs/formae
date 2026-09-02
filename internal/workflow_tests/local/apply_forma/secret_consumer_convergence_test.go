// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit || integration

package workflow_tests_local

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// secretConsumerSchema is a consumer resource holding a secret-sourced field
// alongside plain fields.
func secretConsumerSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "BucketName",
		Fields:     []string{"BucketName", "AccessControl", "DbPassword"},
	}
}

// secretConsumerOverrides mocks the consumer's plugin (Create/Update) while
// leaving the secret on FakeAWS's real SecretsManager handling. Update calls
// against the consumer are counted and their properties captured.
func secretConsumerOverrides(updateCalls *atomic.Int32, updateProps *atomic.Value) *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
			if req.ResourceType != "FakeAWS::S3::Bucket" {
				return nil, nil
			}
			return &resource.CreateResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "consumer-create-1",
					NativeID:        "bucket-native-1",
				},
			}, nil
		},
		Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
			if req.ResourceType != "FakeAWS::S3::Bucket" {
				// Fall through to FakeAWS's real handling, which records the
				// rotated secret so later reads return the new value.
				return nil, nil
			}
			updateCalls.Add(1)
			updateProps.Store(append(json.RawMessage(nil), req.DesiredProperties...))
			return &resource.UpdateResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationUpdate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "consumer-update-1",
					NativeID:        "bucket-native-1",
				},
			}, nil
		},
	}
}

func secretResource(stack, label, value string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:      label,
		Type:       "FakeAWS::SecretsManager::Secret",
		Stack:      stack,
		Target:     "test-target",
		Schema:     secretSchema(),
		Properties: json.RawMessage(`{"Name":"` + label + `","SecretString":"` + value + `"}`),
	}
}

func secretConsumer(stack, secretLabel string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:  "my-bucket",
		Type:   "FakeAWS::S3::Bucket",
		Stack:  stack,
		Target: "test-target",
		Schema: secretConsumerSchema(),
		Properties: json.RawMessage(`{
			"BucketName": "my-bucket",
			"AccessControl": "Private",
			"DbPassword": {
				"$res":      true,
				"$label":    "` + secretLabel + `",
				"$type":     "FakeAWS::SecretsManager::Secret",
				"$stack":    "` + stack + `",
				"$property": "SecretString",
				"$visibility": "Opaque"
			}
		}`),
	}
}

// A consumer holds an opaque reference to a secret. Rotating only the secret's
// value must plan the consumer, deliver the rotated value to the consumer's
// plugin, and leave the new plaintext out of every at-rest and logged sink:
// resource rows, resource_updates rows, provenance carriers, the simulate
// response, and the slog output. The consumer's stored envelope must carry a
// well-formed current-domain resolution provenance digest, never a value.
func TestApplyForma_RotatedSecret_ConsumerReceivesValueWithoutSinkLeaks(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const secretV1 = "rotation-secret-v1"
		const secretV2 = "rotation-secret-v2"

		logCapture := test_helpers.SetupTestLogger()

		var consumerUpdateCalls atomic.Int32
		var consumerUpdateProps atomic.Value
		overrides := secretConsumerOverrides(&consumerUpdateCalls, &consumerUpdateProps)

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		targets := []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}

		createForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secretResource(stack, "my-secret", secretV1), secretConsumer(stack, "my-secret")},
			Targets:   targets,
		}
		_, err = m.ApplyForma(createForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		createCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, createCmd)
		require.Equal(t, forma_command.CommandStateSuccess, createCmd.State,
			"precondition: the create apply must succeed")

		// The consumer's stored envelope carries resolution provenance: a
		// well-formed digest, and no plaintext anywhere in the row.
		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		var consumerRow *pkgmodel.Resource
		for i := range resources {
			if resources[i].Label == "my-bucket" {
				consumerRow = resources[i]
			}
			assert.NotContains(t, string(resources[i].Properties), secretV1,
				"resources.properties leaked plaintext for %s", resources[i].Label)
		}
		require.NotNil(t, consumerRow)
		resolvedFrom := gjson.GetBytes(consumerRow.Properties, "DbPassword.$resolvedFrom").String()
		assert.True(t, provenance.Valid(resolvedFrom),
			"the written occurrence must carry a current-domain provenance digest, got %q", resolvedFrom)

		// Rotation: only the secret's value changes. Simulate first, to pin
		// the plan; then apply, to pin the delivery.
		rotateForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secretResource(stack, "my-secret", secretV2), secretConsumer(stack, "my-secret")},
			Targets:   targets,
		}
		simResp, err := m.ApplyForma(rotateForma, &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: true,
		}, "test-client-id", "", "")
		require.NoError(t, err)
		require.True(t, simResp.Simulation.ChangesRequired, "rotating the secret must plan a change")
		plannedLabels := map[string]bool{}
		for _, ru := range simResp.Simulation.Command.ResourceUpdates {
			plannedLabels[ru.ResourceLabel] = true
		}
		assert.True(t, plannedLabels["my-bucket"], "the rotation must plan the consumer, got %v", plannedLabels)

		// The response is presentation data: no plaintext (stored or newly
		// declared) and no at-rest digest may appear anywhere in it.
		simJSON, err := json.Marshal(simResp)
		require.NoError(t, err)
		assert.NotContains(t, string(simJSON), secretV1, "the simulate response must never carry the stored secret")
		assert.NotContains(t, string(simJSON), secretV2, "the simulate response must never carry the declared rotation")
		assert.NotContains(t, string(simJSON), pkgmodel.ComputeValueHash(secretV1), "digests live at rest, never in a response")
		assert.NotContains(t, string(simJSON), pkgmodel.ComputeValueHash(secretV2), "digests live at rest, never in a response")

		_, err = m.ApplyForma(rotateForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		var rotateCmd *forma_command.FormaCommand
		for _, c := range cmds {
			if c.Command == pkgmodel.CommandApply && c.ID != createCmd.ID {
				rotateCmd = c
			}
		}
		require.NotNil(t, rotateCmd, "the rotation apply command must exist")
		require.Equal(t, forma_command.CommandStateSuccess, rotateCmd.State)

		// Delivery: the only way the rotated value reaches the provider is a
		// plugin call carrying it.
		require.Greater(t, consumerUpdateCalls.Load(), int32(0),
			"the consumer plugin must be invoked during the rotation apply")
		props, _ := consumerUpdateProps.Load().(json.RawMessage)
		require.Equal(t, secretV2, gjson.GetBytes(props, "DbPassword").String(),
			"the consumer must receive the rotated value")

		// At-rest sinks after rotation: no plaintext in any resource_updates
		// column, and the provenance carriers hold only well-formed digests.
		assertNoPlaintextInResourceUpdates(t, m, rotateCmd.ID, secretV2)
		assertNoPlaintextInResourceUpdates(t, m, rotateCmd.ID, secretV1)
		updates, err := m.Datastore.LoadResourceUpdates(rotateCmd.ID)
		require.NoError(t, err)
		for _, ru := range updates {
			for _, rec := range ru.ProvenanceRecords {
				for _, d := range []string{rec.SourceRootDigest, rec.WrittenProvenance, rec.WrittenDigest} {
					if d != "" {
						assert.True(t, provenance.Valid(d), "provenance record digest must be well-formed, got %q", d)
					}
				}
			}
			for uri, d := range ru.ResolvedRootDigests {
				assert.True(t, provenance.Valid(d), "resolved root digest for %s must be well-formed, got %q", uri, d)
			}
		}

		resourcesAfter, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		for i := range resourcesAfter {
			assert.NotContains(t, string(resourcesAfter[i].Properties), secretV2,
				"resources.properties leaked rotated plaintext for %s", resourcesAfter[i].Label)
		}

		// Logs: the plaintext exists only in memory and the in-flight plugin
		// request, never in the slog output.
		for _, entry := range logCapture.GetEntries() {
			assert.NotContains(t, entry, secretV1, "log entry leaked the original secret")
			assert.NotContains(t, entry, secretV2, "log entry leaked the rotated secret")
		}
	})
}

// Re-applying an identical forma whose resource carries a secret-sourced field
// must plan nothing: the occurrence's provenance proves the source did not
// move, so the second reconcile is clean.
func TestApplyForma_SecretConsumer_IdenticalReapplyPlansNothing(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const secretV1 = "steady-state-secret"

		var consumerUpdateCalls atomic.Int32
		var consumerUpdateProps atomic.Value
		overrides := secretConsumerOverrides(&consumerUpdateCalls, &consumerUpdateProps)

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		targets := []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}
		buildForma := func() *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:    []pkgmodel.Stack{{Label: stack}},
				Resources: []pkgmodel.Resource{secretResource(stack, "my-secret", secretV1), secretConsumer(stack, "my-secret")},
				Targets:   targets,
			}
		}

		_, err = m.ApplyForma(buildForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		resp, err := m.ApplyForma(buildForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		assert.False(t, resp.Simulation.ChangesRequired,
			"an identical re-apply of a secret-sourced consumer must plan nothing")

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Len(t, cmds, 1, "no second command may be created")
		assert.Zero(t, consumerUpdateCalls.Load(), "the consumer plugin must not be called again")
	})
}

// Repointing the consumer's reference at a DIFFERENT secret is a real change:
// it must plan the consumer and deliver the new source's value to its plugin.
func TestApplyForma_SecretConsumer_RepointPlansAndDelivers(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const secretA = "first-source-value"
		const secretB = "second-source-value"

		var consumerUpdateCalls atomic.Int32
		var consumerUpdateProps atomic.Value

		// The two secrets need distinct native identities so reads resolve
		// the right one; secretStore maps NativeID to created properties.
		var secretStore sync.Map
		base := secretConsumerOverrides(&consumerUpdateCalls, &consumerUpdateProps)
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				if req.ResourceType == "FakeAWS::SecretsManager::Secret" {
					nativeID := "secret-native-" + gjson.GetBytes(req.Properties, "Name").String()
					secretStore.Store(nativeID, append(json.RawMessage(nil), req.Properties...))
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusSuccess,
							RequestID:       "create-" + nativeID,
							NativeID:        nativeID,
						},
					}, nil
				}
				return base.Create(req)
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				if props, ok := secretStore.Load(req.NativeID); ok {
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   string(props.(json.RawMessage)),
					}, nil
				}
				return nil, nil
			},
			Update: base.Update,
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		targets := []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}
		formaPointing := func(secretLabel string) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{{Label: stack}},
				Resources: []pkgmodel.Resource{
					secretResource(stack, "secret-a", secretA),
					secretResource(stack, "secret-b", secretB),
					secretConsumer(stack, secretLabel),
				},
				Targets: targets,
			}
		}

		_, err = m.ApplyForma(formaPointing("secret-a"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		resp, err := m.ApplyForma(formaPointing("secret-b"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		require.True(t, resp.Simulation.ChangesRequired, "a repointed reference must plan")
		waitForApplyComplete(t, m)

		require.Greater(t, consumerUpdateCalls.Load(), int32(0),
			"the consumer plugin must be invoked for the repoint")
		props, _ := consumerUpdateProps.Load().(json.RawMessage)
		require.Equal(t, secretB, gjson.GetBytes(props, "DbPassword").String(),
			"the consumer must receive the new source's value")
	})
}

// An existing resource gains a NEW reference field to a hashed secret in the
// same apply as an ordinary field change: the update plans, dispatch waits
// for resolution, and the plugin receives BOTH the resolved secret and the
// ordinary change, with no placeholder left anywhere.
func TestApplyForma_NewSecretReferenceOnExistingResource_PlansAndDelivers(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const secretV1 = "first-declared-secret"

		var consumerUpdateCalls atomic.Int32
		var consumerUpdateProps atomic.Value
		overrides := secretConsumerOverrides(&consumerUpdateCalls, &consumerUpdateProps)

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		targets := []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}

		bareConsumer := pkgmodel.Resource{
			Label: "my-bucket", Type: "FakeAWS::S3::Bucket", Stack: stack, Target: "test-target",
			Schema:     secretConsumerSchema(),
			Properties: json.RawMessage(`{"BucketName":"my-bucket","AccessControl":"Private"}`),
		}
		_, err = m.ApplyForma(&pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secretResource(stack, "my-secret", secretV1), bareConsumer},
			Targets:   targets,
		}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		// The mixed change: the consumer gains DbPassword (a reference that
		// cannot resolve at plan time) AND changes AccessControl.
		withRef := secretConsumer(stack, "my-secret")
		withRefProps := string(withRef.Properties)
		withRef.Properties = json.RawMessage(strings.Replace(withRefProps, `"AccessControl": "Private"`, `"AccessControl": "PublicRead"`, 1))
		resp, err := m.ApplyForma(&pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secretResource(stack, "my-secret", secretV1), withRef},
			Targets:   targets,
		}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		require.True(t, resp.Simulation.ChangesRequired, "the new reference must plan")
		waitForApplyComplete(t, m)

		require.Greater(t, consumerUpdateCalls.Load(), int32(0), "the consumer plugin must be invoked")
		props, _ := consumerUpdateProps.Load().(json.RawMessage)
		assert.Equal(t, secretV1, gjson.GetBytes(props, "DbPassword").String(),
			"the plugin receives the resolved secret, never a placeholder")
		assert.Equal(t, "PublicRead", gjson.GetBytes(props, "AccessControl").String(),
			"the ordinary change rides the same update")

		// Steady state: the written occurrence is stamped; re-apply plans nothing.
		resp, err = m.ApplyForma(&pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secretResource(stack, "my-secret", secretV1), withRef},
			Targets:   targets,
		}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		assert.False(t, resp.Simulation.ChangesRequired, "re-apply after the first declaration plans nothing")

		// The persisted executed patch keeps the placeholder, never the
		// plaintext: the live value exists only in memory and the in-flight
		// plugin request (the delivery assertions above), and at rest the
		// occurrence op stays a resolution placeholder.
		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		for _, c := range cmds {
			updates, err := m.Datastore.LoadResourceUpdates(c.ID)
			require.NoError(t, err)
			for _, ru := range updates {
				if ru.DesiredState.Label != "my-bucket" {
					continue
				}
				assert.NotContains(t, string(ru.DesiredState.PatchDocument), secretV1,
					"the persisted executed patch must never carry the plaintext")
			}
		}
	})
}
