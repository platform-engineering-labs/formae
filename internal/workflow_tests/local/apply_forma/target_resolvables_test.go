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
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
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

// TestApplyForma_DestroyThenReapplyTargetWithResolvables exercises the full
// apply → destroy → re-apply cycle for a target whose config references a
// resource property via $ref. Destroy deletes both the resource and all targets
// in the forma. Re-apply recreates them from scratch.
func TestApplyForma_DestroyThenReapplyTargetWithResolvables(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
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
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					NativeID:        request.NativeID,
				}}, nil
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

		// Step 1: Apply
		_, err = m.ApplyForma(makeForma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)
		assert.Eventually(t, func() bool {
			incomplete, _ := m.Datastore.LoadIncompleteFormaCommands()
			return len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "apply should complete")

		// Verify consumer target exists with resolved config
		consumerTarget, err := m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		require.NotNil(t, consumerTarget)

		// Step 2: Destroy — both the resource and the targets are deleted.
		_, err = m.DestroyForma(makeForma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			"test-client-id")
		require.NoError(t, err)
		assert.Eventually(t, func() bool {
			cmds, _ := m.Datastore.LoadFormaCommands()
			incomplete, _ := m.Datastore.LoadIncompleteFormaCommands()
			return len(cmds) == 2 && len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "destroy should complete")

		// Verify resource was deleted and targets are also gone
		resources, err := m.Datastore.LoadResourcesByStack("infra")
		require.NoError(t, err)
		assert.Empty(t, resources, "resources should be deleted")

		consumerTarget, err = m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		assert.Nil(t, consumerTarget, "consumer target should be deleted after destroy")

		// Step 3: Re-apply — both targets are re-created from scratch.
		_, err = m.ApplyForma(makeForma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)
		assert.Eventually(t, func() bool {
			cmds, _ := m.Datastore.LoadFormaCommands()
			incomplete, _ := m.Datastore.LoadIncompleteFormaCommands()
			return len(cmds) == 3 && len(incomplete) == 0
		}, 15*time.Second, 100*time.Millisecond, "re-apply should complete")

		// Verify everything is back
		consumerTarget, err = m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		require.NotNil(t, consumerTarget)

		resources, err = m.Datastore.LoadResourcesByStack("infra")
		require.NoError(t, err)
		assert.Len(t, resources, 1, "cluster resource should be re-created")
	})
}

// TestApplyForma_DestroyOrder_TargetReachabilityComposesWithAttachesTo is a
// composition test: the pre-existing target-reachability edge and the new
// AttachesTo edge must chain correctly so that when a plugin target points at
// a resource R and another resource S attaches to R, destroying the stack
// deletes things in the order:
//
//  1. resource on target (needs the target alive to CRUD via plugin)
//  2. target (waits for #1 via buildTargetResourceEdges)
//  3. R — the resource the target points at (waits for target via buildTargetResolvableEdges)
//  4. S — attaches to R (waits for R via AttachesTo)
//
// This is the exact topology that produces the LGTM "endpoint goes dark
// mid-destroy" bug when the AttachesTo edge is missing: S would delete in
// parallel with #1, taking down the control-plane that the plugin target
// depends on before the plugin has finished CRUD'ing the things on that target.
//
// Mapping to the LGTM case:
//
//	dashboard  ↔ Grafana dashboard / folder
//	grafana    ↔ lgtm-grafana target
//	vpc        ↔ lgtm-listener (what the target's URL points at)
//	cidr       ↔ lgtm-service (attaches to the listener's TG; AttachesTo)
//
// We reuse FakeAWS::EC2::VPC and FakeAWS::EC2::VPCCidrBlock because
// VPCCidrBlock.VpcId already carries AttachesTo=true in the fakeaws schema.
func TestApplyForma_DestroyOrder_TargetReachabilityComposesWithAttachesTo(t *testing.T) {
	const vpcKsuid = "2MiD2rA1SJbLMGZgTL0hCxjkjjr"

	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
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
				switch request.ResourceType {
				case "FakeAWS::EC2::VPC":
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"Id":"vpc-1","CidrBlock":"10.0.0.0/16"}`,
					}, nil
				case "FakeAWS::EC2::VPCCidrBlock":
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"Id":"cidr-1","CidrBlock":"10.0.1.0/24","VpcId":"vpc-1"}`,
					}, nil
				case "FakeAWS::S3::Bucket":
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"BucketName":"dashboard","Endpoint":"https://dashboard.example.com"}`,
					}, nil
				}
				return &resource.ReadResult{ResourceType: request.ResourceType, Properties: "{}"}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					NativeID:        request.NativeID,
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test"}},
			Resources: []pkgmodel.Resource{
				{
					Label:   "vpc",
					Type:    "FakeAWS::EC2::VPC",
					Stack:   "test",
					Target:  "provider",
					Managed: true,
					Ksuid:   vpcKsuid,
					Schema:  pkgmodel.Schema{Identifier: "Id", Fields: []string{"Id", "CidrBlock"}},
					Properties: json.RawMessage(`{
						"Id": "vpc-1",
						"CidrBlock": "10.0.0.0/16"
					}`),
				},
				{
					Label:   "cidr",
					Type:    "FakeAWS::EC2::VPCCidrBlock",
					Stack:   "test",
					Target:  "provider",
					Managed: true,
					Schema: pkgmodel.Schema{
						Identifier: "Id",
						Fields:     []string{"Id", "CidrBlock", "VpcId"},
						// AttachesTo here must keep cidr alive until vpc is gone.
						Hints: map[string]pkgmodel.FieldHint{"VpcId": {AttachesTo: true}},
					},
					Properties: json.RawMessage(fmt.Sprintf(`{
						"Id": "cidr-1",
						"CidrBlock": "10.0.1.0/24",
						"VpcId": {"$ref":"formae://%s#/Id","$value":"vpc-1"}
					}`, vpcKsuid)),
				},
				{
					// "dashboard" stands in for a Grafana resource on the plugin target.
					// It does not $ref anything — it's just a resource on the "grafana"
					// target, so its delete must happen before the target's delete.
					Label:   "dashboard",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "test",
					Target:  "grafana",
					Managed: true,
					Schema:  pkgmodel.Schema{Identifier: "BucketName", Portable: true},
					Properties: json.RawMessage(`{
						"BucketName": "dashboard",
						"Endpoint": "https://dashboard.example.com"
					}`),
				},
			},
			Targets: []pkgmodel.Target{
				{Label: "provider", Namespace: "FakeAWS", Config: json.RawMessage(`{"region":"us-east-1"}`)},
				{
					// "grafana" target's endpoint $refs vpc.Id — this produces the
					// target-reachability edge that pins vpc.delete after the
					// target.delete (which itself waits for "dashboard").
					Label:     "grafana",
					Namespace: "FakeAWS",
					Config: json.RawMessage(fmt.Sprintf(`{
						"endpoint": {"$ref":"formae://%s#/Id","$value":"vpc-1"}
					}`, vpcKsuid)),
				},
			},
		}

		// Apply
		_, err = m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)
		assert.Eventually(t, func() bool {
			incomplete, _ := m.Datastore.LoadIncompleteFormaCommands()
			return len(incomplete) == 0
		}, 15*time.Second, 100*time.Millisecond, "apply should complete")

		resources, err := m.Datastore.LoadResourcesByStack("test")
		require.NoError(t, err)
		require.Len(t, resources, 3, "vpc, cidr and dashboard should all be created")

		// Destroy
		_, err = m.DestroyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			"test-client-id")
		require.NoError(t, err)
		assert.Eventually(t, func() bool {
			cmds, _ := m.Datastore.LoadFormaCommands()
			incomplete, _ := m.Datastore.LoadIncompleteFormaCommands()
			return len(cmds) == 2 && len(incomplete) == 0
		}, 15*time.Second, 100*time.Millisecond, "destroy should complete")

		// Find the destroy command and extract ModifiedTs per label.
		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)

		var destroy *forma_command.FormaCommand
		for _, cmd := range cmds {
			if cmd.Command == pkgmodel.CommandDestroy {
				destroy = cmd
				break
			}
		}
		require.NotNil(t, destroy, "destroy command should be persisted")
		require.Len(t, destroy.ResourceUpdates, 3)

		modifiedTsOf := func(label string) time.Time {
			for _, ru := range destroy.ResourceUpdates {
				if ru.DesiredState.Label == label {
					return ru.ModifiedTs
				}
			}
			t.Fatalf("no ResourceUpdate for %q", label)
			return time.Time{}
		}
		dashboardTs := modifiedTsOf("dashboard")
		vpcTs := modifiedTsOf("vpc")
		cidrTs := modifiedTsOf("cidr")

		// Assertion 1 — pre-existing target-reachability edge:
		// vpc must outlive the dashboard, because dashboard's target ("grafana")
		// refs vpc, and the target can only be deleted after its users are gone.
		assert.True(t,
			dashboardTs.Before(vpcTs),
			"dashboard.delete must complete before vpc.delete via target-reachability (dashboard=%v vpc=%v)",
			dashboardTs, vpcTs)

		// Assertion 2 — the new AttachesTo edge:
		// cidr attaches to vpc; its delete must wait for vpc.delete.
		assert.True(t,
			vpcTs.Before(cidrTs),
			"vpc.delete must complete before cidr.delete via AttachesTo (vpc=%v cidr=%v)",
			vpcTs, cidrTs)
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

		// A target update IS expected: the raw config format changed (existing has
		// resolved plain values, new has $ref wrappers). But no resources should
		// be created or replaced — only the target config format is persisted.
		_ = resp

		// No new creates should have happened — target was NOT replaced
		assert.Equal(t, createCountAfterFirst, createCount,
			"no new creates should happen on reapply with same resolved values")
	})
}
