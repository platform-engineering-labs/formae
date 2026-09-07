// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// A provider-generated secret is never declared: the forma asks for a secret
// and the provider mints the value, so the plaintext reaches formae only
// through the plugin's Create/Read and lives at rest as a digest. A consumer
// takes one key out of that JSON document via a $json sub-key extraction.
//
// Extracting the stack and re-applying the extracted forma in patch mode must
// be a zero-operation apply: nothing about the world changed between the two
// commands.
func TestApplyForma_GeneratedSecretJSONConsumer_ExtractReapplyPlansNothing(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// The provider mints this; it is never written in any forma.
		const generatedDoc = `{"username":"owner","password":"gen-pass-abc123"}`
		const generatedPassword = "gen-pass-abc123"

		var secretStore sync.Map // nativeID -> provider-side properties JSON
		var consumerCreateProps atomic.Value
		var consumerUpdateProps atomic.Value
		var consumerUpdateCalls atomic.Int32

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				switch req.ResourceType {
				case "FakeAWS::SecretsManager::Secret":
					// generateSecretString: the value is minted provider-side
					// and echoed back on create; it is absent from the request.
					require.False(t, gjson.GetBytes(req.Properties, "SecretString").Exists(),
						"precondition: the secret's value must not be declared")
					nativeID := "secret-native-1"
					props := `{"Name":"gen-secret","SecretString":` + jsonQuote(generatedDoc) + `}`
					secretStore.Store(nativeID, props)
					return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "secret-create-1",
						NativeID:           nativeID,
						ResourceProperties: json.RawMessage(props),
					}}, nil
				case "FakeAWS::S3::Bucket":
					consumerCreateProps.Store(append(json.RawMessage(nil), req.Properties...))
					return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "consumer-create-1",
						NativeID:        "bucket-native-1",
					}}, nil
				}
				return nil, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				if props, ok := secretStore.Load(req.NativeID); ok {
					return &resource.ReadResult{ResourceType: req.ResourceType, Properties: props.(string)}, nil
				}
				return &resource.ReadResult{ResourceType: req.ResourceType, Properties: "{}"}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				if req.ResourceType != "FakeAWS::S3::Bucket" {
					return nil, nil
				}
				consumerUpdateCalls.Add(1)
				consumerUpdateProps.Store(append(json.RawMessage(nil), req.DesiredProperties...))
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationUpdate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "consumer-update-1",
					NativeID:        "bucket-native-1",
				}}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		targets := []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}

		// The secret declares only its name: the value is provider-generated.
		secret := pkgmodel.Resource{
			Label:      "gen-secret",
			Type:       "FakeAWS::SecretsManager::Secret",
			Stack:      stack,
			Target:     "test-target",
			Schema:     secretSchema(),
			Properties: json.RawMessage(`{"Name":"gen-secret"}`),
		}
		// The consumer takes one key out of the secret document.
		consumer := pkgmodel.Resource{
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
					"$label":    "gen-secret",
					"$type":     "FakeAWS::SecretsManager::Secret",
					"$stack":    "` + stack + `",
					"$property": "SecretString",
					"$visibility": "Opaque",
					"$json":    "password"
				}
			}`),
		}

		_, err = m.ApplyForma(&pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secret, consumer},
			Targets:   targets,
		}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		createCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, createCmd)
		require.Equal(t, forma_command.CommandStateSuccess, createCmd.State,
			"precondition: the create apply must succeed")

		// The consumer really received the extracted leaf, not the document.
		createProps, _ := consumerCreateProps.Load().(json.RawMessage)
		require.Equal(t, generatedPassword, gjson.GetBytes(createProps, "DbPassword").String(),
			"precondition: the consumer must receive the extracted leaf")

		// --- Stored shapes after the first apply -------------------------
		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		var storedSecret, storedConsumer *pkgmodel.Resource
		for _, r := range resources {
			if r.Type == "FakeAWS::SecretsManager::Secret" {
				storedSecret = r
			} else {
				storedConsumer = r
			}
		}
		require.NotNil(t, storedSecret)
		require.NotNil(t, storedConsumer)
		t.Logf("STORED source SecretString  = %s", gjson.GetBytes(storedSecret.Properties, "SecretString").Raw)
		t.Logf("STORED source (whole row)   = %s", string(storedSecret.Properties))
		t.Logf("STORED consumer DbPassword  = %s", gjson.GetBytes(storedConsumer.Properties, "DbPassword").Raw)
		t.Logf("STORED source $hashed       = %v", gjson.GetBytes(storedSecret.Properties, "SecretString.$hashed").Bool())
		t.Logf("STORED consumer $resolvedFrom = %q", gjson.GetBytes(storedConsumer.Properties, "DbPassword.$resolvedFrom").String())

		// --- Control: re-applying the ORIGINAL forma plans nothing -------
		ctrl, err := m.ApplyForma(&pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secret, consumer},
			Targets:   targets,
		}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch, Simulate: true}, "test-client-id", "", "")
		require.NoError(t, err)
		t.Logf("CONTROL (original forma, patch+simulate) ChangesRequired=%v", ctrl.Simulation.ChangesRequired)
		for _, ru := range ctrl.Simulation.Command.ResourceUpdates {
			t.Logf("CONTROL planned: %s %s (%s) patch: %s",
				ru.Operation, ru.ResourceLabel, ru.ResourceType, string(ru.PatchDocument))
		}
		require.False(t, ctrl.Simulation.ChangesRequired,
			"control: re-applying the SAME forma must plan nothing, so any diff below "+
				"belongs to the extract round trip and not to the generated secret")

		// --- The regression: extract, then re-apply the extracted forma --
		extracted, err := m.ExtractResources("stack:" + stack)
		require.NoError(t, err)
		require.NotNil(t, extracted)
		for i := range extracted.Resources {
			t.Logf("EXTRACTED %s properties = %s",
				extracted.Resources[i].Label, string(extracted.Resources[i].Properties))
		}
		extracted.Targets = targets

		// The reference's extraction selector is part of the occurrence's
		// identity: an envelope that loses it reads as a repoint of the same
		// reference at a different key, which always plans.
		for i := range extracted.Resources {
			if extracted.Resources[i].Label != "my-bucket" {
				continue
			}
			assert.Equal(t, "password",
				gjson.GetBytes(extracted.Resources[i].Properties, "DbPassword.$json").String(),
				"the extracted reference must keep its $json selector")
		}

		resp, err := m.ApplyForma(extracted,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch, Simulate: true},
			"test-client-id", "", "")
		require.NoError(t, err)
		t.Logf("EXTRACT RE-APPLY ChangesRequired=%v", resp.Simulation.ChangesRequired)
		for _, ru := range resp.Simulation.Command.ResourceUpdates {
			t.Logf("EXTRACT RE-APPLY planned: %s %s (%s) patch: %s",
				ru.Operation, ru.ResourceLabel, ru.ResourceType, string(ru.PatchDocument))
		}

		assert.False(t, resp.Simulation.ChangesRequired,
			"re-applying the extracted forma must be a zero-operation apply")
	})
}

