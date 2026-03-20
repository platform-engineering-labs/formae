// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestApplyForma_TargetWithResolvables(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// The "cluster" resource returns properties with an Endpoint field.
		clusterProps := `{"BucketName":"my-cluster","Endpoint":"https://my-cluster.example.com"}`
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "1234",
					NativeID:           "native-" + request.Label,
					ResourceProperties: request.Properties,
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   clusterProps,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Step 1: Apply a forma with:
		//   - Target "provider" (plain config)
		//   - Resource "cluster" on "provider" with an Endpoint property
		//   - Target "consumer" whose config references the cluster's Endpoint via $ref
		clusterKsuid := "2MiD2rA1SJbLMGZgTL0hCxjkjjr" // deterministic KSUID for test
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "infra"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "cluster",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "infra",
					Target:  "provider",
					Managed: true,
					Ksuid:   clusterKsuid,
					Schema:  pkgmodel.Schema{Identifier: "BucketName", Portable: true},
					Properties: json.RawMessage(`{
						"BucketName": "my-cluster",
						"Endpoint": "https://my-cluster.example.com"
					}`),
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "provider",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region": "us-east-1"}`),
				},
				{
					Label:     "consumer",
					Namespace: "FakeAWS",
					Config: json.RawMessage(fmt.Sprintf(`{
						"endpoint": {"$ref": "formae://%s#/Endpoint"}
					}`, clusterKsuid)),
				},
			},
		}

		_, err = m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)

		// Wait for the command to complete
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(fas) == 1 && len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "apply command should complete")

		// Verify the consumer target was created with the resolved config.
		// The stored config preserves the $ref structure with $value filled in.
		consumerTarget, err := m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		require.NotNil(t, consumerTarget, "consumer target should exist")

		var storedConfig map[string]any
		require.NoError(t, json.Unmarshal(consumerTarget.Config, &storedConfig))
		endpointObj, ok := storedConfig["endpoint"].(map[string]any)
		require.True(t, ok, "endpoint should be a $ref object")
		assert.Equal(t, "https://my-cluster.example.com", endpointObj["$value"])

		// Verify the cluster resource exists
		resources, err := m.Datastore.LoadResourcesByStack("infra")
		require.NoError(t, err)
		require.Len(t, resources, 1)
		assert.Equal(t, "cluster", resources[0].Label)
	})
}

func TestApplyForma_TargetWithResolvables_FromTripletURI(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// The cluster resource returns properties including an Endpoint field.
		clusterProps := `{"BucketName":"my-cluster","Endpoint":"https://my-cluster.example.com"}`
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "1234",
					NativeID:           "native-" + request.Label,
					ResourceProperties: request.Properties,
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   clusterProps,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Apply a forma where the target config uses $res triplet format (as PKL would produce),
		// NOT pre-translated $ref URIs. This exercises the full flow:
		//   1. translateFormaeReferencesToKsuid converts $res → $ref
		//   2. ExtractResolvableURIsFromJSON finds the $ref in target config
		//   3. DAG waits for cluster before resolving consumer target
		//   4. TargetUpdater resolves endpoint value into target config
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "infra"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "cluster",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "infra",
					Target:  "provider",
					Managed: true,
					Schema:  pkgmodel.Schema{Identifier: "BucketName", Portable: true},
					Properties: json.RawMessage(`{
						"BucketName": "my-cluster",
						"Endpoint": "https://my-cluster.example.com"
					}`),
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "provider",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region": "us-east-1"}`),
				},
				{
					Label:     "consumer",
					Namespace: "FakeAWS",
					// $res triplet format — as PKL produces. No KSUID, no $ref.
					Config: json.RawMessage(`{
						"endpoint": {
							"$res": true,
							"$label": "cluster",
							"$type": "FakeAWS::S3::Bucket",
							"$stack": "infra",
							"$property": "Endpoint",
							"$visibility": "Clear"
						}
					}`),
				},
			},
		}

		_, err = m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)

		// Wait for the command to complete
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(fas) == 1 && len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "apply command should complete")

		// Verify the consumer target was created with the resolved config
		consumerTarget, err := m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		require.NotNil(t, consumerTarget, "consumer target should exist")

		var storedConfig map[string]any
		require.NoError(t, json.Unmarshal(consumerTarget.Config, &storedConfig))
		endpointObj, ok := storedConfig["endpoint"].(map[string]any)
		require.True(t, ok, "endpoint should be a $ref object")
		assert.Equal(t, "https://my-cluster.example.com", endpointObj["$value"])
	})
}

func TestApplyForma_ReapplyTargetResolvablesSameValue_NoReplace(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		clusterProps := `{"BucketName":"my-cluster","Endpoint":"https://my-cluster.example.com"}`
		createCount := 0
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				createCount++
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "1234",
					NativeID:           "native-" + request.Label,
					ResourceProperties: request.Properties,
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   clusterProps,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		clusterKsuid := "2MiD2rA1SJbLMGZgTL0hCxjkjjr"
		makeForma := func() *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{{Label: "infra"}},
				Resources: []pkgmodel.Resource{
					{
						Label: "cluster", Type: "FakeAWS::S3::Bucket", Stack: "infra", Target: "provider",
						Managed: true, Ksuid: clusterKsuid,
						Schema:     pkgmodel.Schema{Identifier: "BucketName", Portable: true},
						Properties: json.RawMessage(clusterProps),
					},
				},
				Targets: []pkgmodel.Target{
					{Label: "provider", Namespace: "FakeAWS", Config: json.RawMessage(`{"region":"us-east-1"}`)},
					{Label: "consumer", Namespace: "FakeAWS", Config: json.RawMessage(fmt.Sprintf(`{
						"endpoint": {"$ref": "formae://%s#/Endpoint"}
					}`, clusterKsuid))},
				},
			}
		}

		// First apply
		_, err = m.ApplyForma(makeForma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			incomplete, _ := m.Datastore.LoadIncompleteFormaCommands()
			return len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "first apply should complete")

		createCountAfterFirst := createCount

		// Second apply — same forma, same resolved values
		resp, err := m.ApplyForma(makeForma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)

		// No changes required — target config resolves to the same value
		assert.False(t, resp.Simulation.ChangesRequired, "reapply with same resolved values should require no changes")

		// No new creates should have happened — target was NOT replaced
		assert.Equal(t, createCountAfterFirst, createCount,
			"no new creates should happen on reapply with same resolved values")
	})
}
