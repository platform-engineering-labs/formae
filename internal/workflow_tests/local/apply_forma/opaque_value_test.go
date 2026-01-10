// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetastructure_ApplyFormaHashesOpaqueValues(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Track what the plugin receives
		var receivedProperties json.RawMessage
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				receivedProperties = request.Properties
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "test-123",
						NativeID:        "secret-arn",
					},
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{{
				Label:  "test-secret",
				Type:   "FakeAWS::SecretsManager::Secret",
				Stack:  "test-stack",
				Target: "test-target",
				Properties: json.RawMessage(`{
					"Name": "my-secret",
					"SecretString": {
						"$value": "super-secret-password",
						"$visibility": "Opaque",
						"$strategy": "Update"
					}
				}`),
			}},
			Targets: []pkgmodel.Target{{
				Label:     "test-target",
				Namespace: "test-namespace",
			}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client-id")
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			return err == nil && len(fas) == 1 && len(fas[0].ResourceUpdates) == 1 &&
				fas[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess
		}, 3*time.Second, 100*time.Millisecond)

		fas, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)

		// Database should contain hashed values in the completed resource update
		completedResourceUpdate := fas[0].ResourceUpdates[0]
		var storedProps map[string]any
		err = json.Unmarshal(completedResourceUpdate.DesiredState.Properties, &storedProps)
		require.NoError(t, err)

		secretString := storedProps["SecretString"].(map[string]interface{})
		hashedValue := secretString["$value"].(string)

		assert.Len(t, hashedValue, 64, "Should be 64-char hash")
		assert.NotEqual(t, "super-secret-password", hashedValue)
		assert.Regexp(t, "^[a-f0-9]{64}$", hashedValue)

		// Plugin should receive raw value as simple string (ConvertToPluginFormat extracts $value)
		var pluginProps map[string]any
		err = json.Unmarshal(receivedProperties, &pluginProps)
		require.NoError(t, err)

		pluginSecretString := pluginProps["SecretString"].(string)
		assert.Equal(t, "super-secret-password", pluginSecretString)
	})
}
