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

// TestSecretJSON_RefWithJSONPathResolvesToExtractedScalar exercises the full
// end-to-end path of a secret whose value is a JSON document and a consumer
// resource that references it via a $res envelope carrying a $json dotted path.
//
// The test verifies three invariants that together define the .json() contract:
//  1. The consumer's resolved property value equals the EXTRACTED scalar (the
//     leaf at the $json path), not the whole JSON document.
//  2. The secret's own stored SecretString is hashed at rest ($hashed:true),
//     so no plaintext of the JSON document (including the inner password it
//     contains) survives in the resources table.
//  3. No plaintext of the extracted scalar nor the outer JSON document appears
//     in any resource_updates column (DesiredState/PriorState/progress).
func TestSecretJSON_RefWithJSONPathResolvesToExtractedScalar(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// The secret value is a JSON document. The inner password is what the
		// consumer expects to receive via the $json path "db.password".
		const innerPassword = "db-pass-9x-secret"
		const secretJSONDoc = `{"db":{"password":"db-pass-9x-secret"}}`

		// Capture what the consumer plugin's Create receives to assert it got
		// the extracted scalar, not the whole JSON document.
		var consumerReceivedProps json.RawMessage

		// Override Create for FakeAWS::S3::Bucket: return immediate success with
		// no ResourceProperties so the progress result never carries the extracted
		// scalar back into a resource_updates column that is not hashed for Bucket.
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				if req.ResourceType == "FakeAWS::S3::Bucket" {
					consumerReceivedProps = req.Properties
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusSuccess,
							RequestID:       "consumer-create-1",
							NativeID:        "bucket-native-1",
							// ResourceProperties intentionally omitted: the Bucket schema
							// does not declare DbPassword as Opaque, so returning the
							// extracted scalar here would appear unhashed in
							// MostRecentProgressResult. Returning nil keeps the progress
							// sink clean, exactly mirroring write-only secret fields whose
							// cloud API never echoes them back on create.
						},
					}, nil
				}
				// Let the secret use FakeAWS's default Create (InProgress → Status
				// poll) so it exercises the full opaque-value hashing path.
				return nil, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()

		// The secret's SecretString is a JSON document (a bare string containing
		// valid JSON). The schema's Opaque hint on SecretString means the whole
		// string — the JSON document — is hashed at rest.
		secret := pkgmodel.Resource{
			Label:  "my-json-secret",
			Type:   "FakeAWS::SecretsManager::Secret",
			Stack:  stack,
			Target: "test-target",
			Schema: secretSchema(),
			Properties: json.RawMessage(
				`{"Name":"my-json-secret","SecretString":` + jsonQuote(secretJSONDoc) + `}`,
			),
		}

		// The consumer references the secret's SecretString field via $res,
		// requesting the leaf scalar at path "db.password" via $json. The
		// $visibility:Opaque propagates to the resolved Value, so the extracted
		// scalar is also hashed at rest in the consumer's stored properties.
		consumer := pkgmodel.Resource{
			Label:  "my-bucket",
			Type:   "FakeAWS::S3::Bucket",
			Stack:  stack,
			Target: "test-target",
			Properties: json.RawMessage(`{
				"BucketName": "my-bucket",
				"DbPassword": {
					"$res":      true,
					"$label":    "my-json-secret",
					"$type":     "FakeAWS::SecretsManager::Secret",
					"$stack":    "` + stack + `",
					"$property": "SecretString",
					"$visibility": "Opaque",
					"$json":    "db.password"
				}
			}`),
		}

		forma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secret, consumer},
			Targets:   []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		applyCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, applyCmd, "apply command should exist")
		require.Equal(t, forma_command.CommandStateSuccess, applyCmd.State,
			"apply with a $json cross-resource reference must succeed")

		// --- Invariant 1: consumer received the extracted scalar, not the doc ---
		require.NotNil(t, consumerReceivedProps,
			"consumer Create override should have been called")
		dbPassword := gjson.GetBytes(consumerReceivedProps, "DbPassword").String()
		assert.Equal(t, innerPassword, dbPassword,
			"consumer plugin must receive the extracted scalar from $json, not the full JSON doc")
		assert.NotContains(t, string(consumerReceivedProps), secretJSONDoc,
			"consumer plugin must not receive the whole JSON document as the field value")

		// --- Invariant 2: secret's own value is stored hashed ---
		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resources, 2, "both the secret and consumer must be stored")

		var secretResource *pkgmodel.Resource
		var consumerResource *pkgmodel.Resource
		for _, r := range resources {
			if r.Type == "FakeAWS::SecretsManager::Secret" {
				secretResource = r
			} else {
				consumerResource = r
			}
		}
		require.NotNil(t, secretResource, "secret resource must be stored")
		require.NotNil(t, consumerResource, "consumer resource must be stored")

		// Secret: stored SecretString must carry $hashed and no plaintext of the JSON doc.
		storedSecretProps := string(secretResource.Properties)
		assert.Contains(t, storedSecretProps, hashedMarker,
			"secret SecretString must be hashed at rest")
		assert.NotContains(t, storedSecretProps, secretJSONDoc,
			"secret SecretString must not store the plaintext JSON document")
		assert.NotContains(t, storedSecretProps, innerPassword,
			"secret stored properties must not contain the inner password")

		// Consumer: stored DbPassword envelope must carry $hashed and not the extracted scalar.
		storedConsumerProps := string(consumerResource.Properties)
		assert.Contains(t, storedConsumerProps, hashedMarker,
			"consumer DbPassword ($visibility:Opaque ref) must be hashed at rest")
		assert.NotContains(t, storedConsumerProps, innerPassword,
			"consumer stored properties must not contain the extracted password in plaintext")

		// --- Invariant 3: no plaintext in resource_updates ---
		// Both the extracted scalar and the outer JSON document must be absent from
		// every resource_updates sink: DesiredState/PriorState properties and
		// progress ResourceProperties. Because both the secret's SecretString and
		// the consumer's DbPassword envelope are Opaque, the transformer hashes
		// them in DesiredState before the resource_updates row is written.
		// The consumer's MostRecentProgressResult carries no ResourceProperties
		// (the Create override returns nil) so no progress sink leaks the scalar.
		assertNoPlaintextInResourceUpdates(t, m, applyCmd.ID, innerPassword)
		assertNoPlaintextInResourceUpdates(t, m, applyCmd.ID, secretJSONDoc)
	})
}

// TestSecretJSON_RefWithJSONPathResolvesOnUpdate exercises the same $json
// reference across a SECOND apply: the stack is re-applied with an unrelated
// field on the consumer changed, so the consumer is planned as an update rather
// than a create.
//
// The reference must resolve the same way it does on the create path. Planning
// an update resolves a reference from the source resource's PERSISTED state,
// where an opaque field is a SHA-256 digest rather than the live value, so a
// $json extraction over it has no valid JSON to read. The value handed to the
// plugin must still be the extracted scalar, and the apply must not be rejected
// while generating the patch document.
func TestSecretJSON_RefWithJSONPathResolvesOnUpdate(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const innerPassword = "db-pass-9x-secret"
		const secretJSONDoc = `{"db":{"password":"db-pass-9x-secret"}}`

		// What the consumer plugin receives as the new value on the update.
		var consumerUpdateProps json.RawMessage

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				if req.ResourceType == "FakeAWS::S3::Bucket" {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusSuccess,
							RequestID:       "consumer-create-1",
							NativeID:        "bucket-native-1",
						},
					}, nil
				}
				return nil, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				if req.ResourceType != "FakeAWS::S3::Bucket" {
					return nil, nil
				}
				consumerUpdateProps = req.DesiredProperties
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

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()

		secret := pkgmodel.Resource{
			Label:  "my-json-secret",
			Type:   "FakeAWS::SecretsManager::Secret",
			Stack:  stack,
			Target: "test-target",
			Schema: secretSchema(),
			Properties: json.RawMessage(
				`{"Name":"my-json-secret","SecretString":` + jsonQuote(secretJSONDoc) + `}`,
			),
		}

		// consumerWith builds the consumer resource with the given AccessControl
		// value; everything else, including the $json reference, is identical
		// across both applies.
		consumerWith := func(accessControl string) pkgmodel.Resource {
			return pkgmodel.Resource{
				Label:  "my-bucket",
				Type:   "FakeAWS::S3::Bucket",
				Stack:  stack,
				Target: "test-target",
				// The consumer needs a schema for the update path: patch
				// generation only emits ops for declared fields. DbPassword
				// carries no Opaque hint — its opacity comes from the $visibility
				// on the reference envelope.
				Schema: pkgmodel.Schema{
					Identifier: "BucketName",
					Fields:     []string{"BucketName", "AccessControl", "DbPassword"},
				},
				Properties: json.RawMessage(`{
					"BucketName": "my-bucket",
					"AccessControl": "` + accessControl + `",
					"DbPassword": {
						"$res":      true,
						"$label":    "my-json-secret",
						"$type":     "FakeAWS::SecretsManager::Secret",
						"$stack":    "` + stack + `",
						"$property": "SecretString",
						"$visibility": "Opaque",
						"$json":    "db.password"
					}
				}`),
			}
		}

		targets := []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}

		createForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secret, consumerWith("Private")},
			Targets:   targets,
		}
		_, err = m.ApplyForma(createForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		createCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, createCmd)
		require.Equal(t, forma_command.CommandStateSuccess, createCmd.State,
			"precondition: the create apply must succeed")

		// Precondition: the secret's value is a digest at rest, so planning the
		// update cannot read the live JSON document out of the resources table.
		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resources, 2)
		for _, r := range resources {
			if r.Type == "FakeAWS::SecretsManager::Secret" {
				require.Contains(t, string(r.Properties), hashedMarker,
					"precondition: the secret must be hashed at rest before the update apply")
			}
		}

		// Second apply: only AccessControl differs, so the consumer is an update
		// and the $json reference is planned from persisted state.
		updateForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secret, consumerWith("PublicRead")},
			Targets:   targets,
		}
		_, err = m.ApplyForma(updateForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err,
			"re-applying a stack whose resource takes a $json value from a secret must be admitted")
		waitForCommands(t, m, 2)

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		var updateCmd *forma_command.FormaCommand
		applyCount := 0
		for _, c := range cmds {
			if c.Command == pkgmodel.CommandApply {
				applyCount++
				if applyCount == 2 {
					updateCmd = c
				}
			}
		}
		require.NotNil(t, updateCmd, "the second (update) apply command should exist")
		require.Equal(t, forma_command.CommandStateSuccess, updateCmd.State,
			"the update apply must succeed")

		require.NotNil(t, consumerUpdateProps, "the consumer plugin's Update should have been called")
		assert.Equal(t, innerPassword, gjson.GetBytes(consumerUpdateProps, "DbPassword").String(),
			"on update the consumer plugin must receive the extracted scalar, the same value the create path resolved")
		assert.NotContains(t, string(consumerUpdateProps), secretJSONDoc,
			"the consumer plugin must not receive the whole JSON document as the field value")
		assertNoDigestOrHashedMarker(t, consumerUpdateProps, "DesiredProperties")
	})
}

// TestSecret_BareRefResolvesOnUpdate is the same update path as
// TestSecretJSON_RefWithJSONPathResolvesOnUpdate for a reference with no $json:
// the consumer takes the secret's whole value.
//
// Nothing parses the value on this path, so a digest read out of persisted
// state is not rejected the way an unparseable $json source is — it is simply
// planned as the value. The patch the plugin receives, and the value it is
// given to write, must both be the live secret.
func TestSecret_BareRefResolvesOnUpdate(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const plaintextSecret = "bare-ref-super-secret"

		var consumerUpdateProps json.RawMessage
		var consumerPatchDoc string

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				if req.ResourceType == "FakeAWS::S3::Bucket" {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusSuccess,
							RequestID:       "consumer-create-1",
							NativeID:        "bucket-native-1",
						},
					}, nil
				}
				return nil, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				if req.ResourceType != "FakeAWS::S3::Bucket" {
					return nil, nil
				}
				consumerUpdateProps = req.DesiredProperties
				if req.PatchDocument != nil {
					consumerPatchDoc = *req.PatchDocument
				}
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

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()

		secret := pkgmodel.Resource{
			Label:      "my-secret",
			Type:       "FakeAWS::SecretsManager::Secret",
			Stack:      stack,
			Target:     "test-target",
			Schema:     secretSchema(),
			Properties: json.RawMessage(`{"Name":"my-secret","SecretString":"` + plaintextSecret + `"}`),
		}

		consumerWith := func(accessControl string) pkgmodel.Resource {
			return pkgmodel.Resource{
				Label:  "my-bucket",
				Type:   "FakeAWS::S3::Bucket",
				Stack:  stack,
				Target: "test-target",
				Schema: pkgmodel.Schema{
					Identifier: "BucketName",
					Fields:     []string{"BucketName", "AccessControl", "DbPassword"},
				},
				Properties: json.RawMessage(`{
					"BucketName": "my-bucket",
					"AccessControl": "` + accessControl + `",
					"DbPassword": {
						"$res":      true,
						"$label":    "my-secret",
						"$type":     "FakeAWS::SecretsManager::Secret",
						"$stack":    "` + stack + `",
						"$property": "SecretString",
						"$visibility": "Opaque"
					}
				}`),
			}
		}

		targets := []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}

		createForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secret, consumerWith("Private")},
			Targets:   targets,
		}
		_, err = m.ApplyForma(createForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		createCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, createCmd)
		require.Equal(t, forma_command.CommandStateSuccess, createCmd.State,
			"precondition: the create apply must succeed")

		updateForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secret, consumerWith("PublicRead")},
			Targets:   targets,
		}
		_, err = m.ApplyForma(updateForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForCommands(t, m, 2)

		require.NotNil(t, consumerUpdateProps, "the consumer plugin's Update should have been called")
		assert.Equal(t, plaintextSecret, gjson.GetBytes(consumerUpdateProps, "DbPassword").String(),
			"on update the consumer plugin must receive the live secret value")
		assertNoDigestOrHashedMarker(t, consumerUpdateProps, "DesiredProperties")
		assertNoDigestOrHashedMarker(t, json.RawMessage(consumerPatchDoc), "PatchDocument")
	})
}

// jsonQuote returns s as a JSON-encoded string literal, for embedding a
// JSON document as a string value in another JSON document.
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic("jsonQuote: " + err.Error())
	}
	return string(b)
}
