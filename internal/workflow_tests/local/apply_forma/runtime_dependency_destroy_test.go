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
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestRuntimeDependency_DestroyOrder verifies that the runtimeDependency
// EdgeKind on a consumer→producer reference produces the destroy order
// consumer → child → producer end-to-end.
//
// Topology:
//   - A: producer (FakeAWS::Test::Container) — carries identifier
//     ContainerId and an Arn property used by the containee for the
//     default destroy edge.
//   - B: child of A (FakeAWS::Test::Containee), Schema.Parent = A's
//     type, ParentMappings link parent-side ContainerId → child-side
//     ContainerId. Its ContainerId is a literal that matches A's
//     identifier so destroySideMatches recognizes it as A's child;
//     its ContainerArn is a $ref to A.Arn so the default destroy
//     edge places A after B.
//   - C: consumer (FakeAWS::Test::Consumer), references A via field
//     "ContainerRef" ($ref to A.ContainerId) whose hint declares
//     EdgeKind = runtimeDependency.
//
// Expected destroy order under runtimeDependency:
//   - C deletes first (no incoming edges)
//   - B.delete waits for C.delete (runtimeDependency child edge: every
//     containment child of the referenced producer must wait for the
//     consumer)
//   - A.delete waits for B.delete (default destroy edge from B's
//     ContainerArn $ref to A.Arn) and also waits for C.delete (default
//     consumer→producer edge under runtimeDependency)
//
// Net order: C → B → A.
func TestRuntimeDependency_DestroyOrder(t *testing.T) {
	// Deterministic KSUIDs so the resources can $ref each other.
	const (
		containerKsuid = "2MiD2rA1SJbLMGZgTL0hCxjkjj1"
		containeeKsuid = "2MiD2rA1SJbLMGZgTL0hCxjkjj2"
		consumerKsuid  = "2MiD2rA1SJbLMGZgTL0hCxjkjj3"

		containerType = "FakeAWS::Test::Container"
		containeeType = "FakeAWS::Test::Containee"
		consumerType  = "FakeAWS::Test::Consumer"
	)

	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Per-label plugin overrides keep Create/Read/Delete behavior simple
		// and deterministic for arbitrary FakeAWS::Test::* resource types.
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
				// Return properties consistent with what was created so the
				// resolver can populate $value fields when other resources
				// $ref this one.
				switch request.ResourceType {
				case containerType:
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"ContainerId":"container-1","Arn":"arn-container"}`,
					}, nil
				case containeeType:
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"ContaineeId":"containee-1","ContainerId":"container-1","ContainerArn":"arn-container"}`,
					}, nil
				case consumerType:
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"ConsumerId":"consumer-1","ContainerRef":"container-1"}`,
					}, nil
				}
				return &resource.ReadResult{ResourceType: request.ResourceType, Properties: "{}"}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "delete-123",
					NativeID:        request.NativeID,
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Build the forma. The consumer ($ref to container via ContainerRef)
		// gets the runtimeDependency hint on that field. The containee
		// declares Schema.Parent = container type and ParentMappings so the
		// destroy DAG recognizes it as a containment child of the container;
		// it carries a literal ContainerId (for the destroySideMatches
		// identity check) plus a separate ContainerArn $ref to the
		// container so the default destroy edge places the container
		// after the containee.
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test"}},
			Resources: []pkgmodel.Resource{
				{
					Label:   "container",
					Type:    containerType,
					Stack:   "test",
					Target:  "provider",
					Managed: true,
					Ksuid:   containerKsuid,
					Schema: pkgmodel.Schema{
						Identifier: "ContainerId",
						Fields:     []string{"ContainerId", "Arn"},
					},
					Properties: json.RawMessage(`{
						"ContainerId": "container-1",
						"Arn": "arn-container"
					}`),
				},
				{
					Label:   "containee",
					Type:    containeeType,
					Stack:   "test",
					Target:  "provider",
					Managed: true,
					Ksuid:   containeeKsuid,
					Schema: pkgmodel.Schema{
						Identifier: "ContaineeId",
						Fields:     []string{"ContaineeId", "ContainerId", "ContainerArn"},
						// Schema.Parent + ParentMappings make the containee a
						// containment child of the container — the
						// runtimeDependency rule on the consumer side will
						// inject an edge from the consumer to *this* child.
						Parent: containerType,
						ParentMappings: []pkgmodel.ParentMapping{
							{ParentProperty: "ContainerId", ChildProperty: "ContainerId"},
						},
					},
					// Two independent links to the container:
					//
					//   ContainerId (literal) — used by destroySideMatches
					//     to identify this resource as a containment child
					//     of the container instance during destroy. The
					//     current implementation reads literal values on
					//     both sides via Resource.GetProperty, so a $ref
					//     blob would not match the producer's literal
					//     identifier (see test note in commit body).
					//
					//   ContainerArn ($ref) — drives the default destroy
					//     edge container.delete-after-containee.delete
					//     (B → A) by appearing in the containee's
					//     RemainingResolvables. Without this edge the
					//     containee and container would destroy in
					//     parallel after the consumer.
					Properties: json.RawMessage(fmt.Sprintf(`{
						"ContaineeId": "containee-1",
						"ContainerId": "container-1",
						"ContainerArn": {"$ref":"formae://%s#/Arn","$value":"arn-container"}
					}`, containerKsuid)),
				},
				{
					Label:   "consumer",
					Type:    consumerType,
					Stack:   "test",
					Target:  "provider",
					Managed: true,
					Ksuid:   consumerKsuid,
					Schema: pkgmodel.Schema{
						Identifier: "ConsumerId",
						Fields:     []string{"ConsumerId", "ContainerRef"},
						// Declare the runtimeDependency edge on the consumer's
						// reference to the container — this is the rule under
						// test.
						Hints: map[string]pkgmodel.FieldHint{
							"ContainerRef": {EdgeKind: pkgmodel.EdgeKindRuntimeDependency},
						},
					},
					Properties: json.RawMessage(fmt.Sprintf(`{
						"ConsumerId": "consumer-1",
						"ContainerRef": {"$ref":"formae://%s#/ContainerId","$value":"container-1"}
					}`, containerKsuid)),
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "provider",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"us-east-1"}`),
				},
			},
		}

		// Step 1: Apply.
		_, err = m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err, "ApplyForma should not error")

		assert.Eventually(t, func() bool {
			incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "apply should complete")

		resources, err := m.Datastore.LoadResourcesByStack("test")
		require.NoError(t, err)
		require.Len(t, resources, 3, "should have container, containee, and consumer resources")

		// Step 2: Destroy.
		_, err = m.DestroyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			"test-client-id")
		require.NoError(t, err, "DestroyForma should not error")

		assert.Eventually(t, func() bool {
			cmds, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(cmds) == 2 && len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "destroy should complete")

		// Step 3: Locate the destroy command and verify resource update order.
		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)

		var destroyCmd *forma_command.FormaCommand
		for _, cmd := range cmds {
			if cmd.Command == pkgmodel.CommandDestroy {
				destroyCmd = cmd
				break
			}
		}
		require.NotNil(t, destroyCmd, "should have a destroy command")
		require.Len(t, destroyCmd.ResourceUpdates, 3, "destroy command should have 3 resource updates")

		modifiedTsOfResource := func(label string) time.Time {
			for _, ru := range destroyCmd.ResourceUpdates {
				if ru.DesiredState.Label == label {
					return ru.ModifiedTs
				}
			}
			t.Fatalf("no ResourceUpdate for %q", label)
			return time.Time{}
		}

		consumerTs := modifiedTsOfResource("consumer")
		containeeTs := modifiedTsOfResource("containee")
		containerTs := modifiedTsOfResource("container")

		// Core assertions: C → B → A under runtimeDependency.
		assert.True(t,
			consumerTs.Before(containeeTs),
			"consumer.delete must complete before containee.delete (runtimeDependency child edge: child waits for consumer) — consumer=%v containee=%v",
			consumerTs, containeeTs)
		assert.True(t,
			containeeTs.Before(containerTs),
			"containee.delete must complete before container.delete (default destroy edge: B→A) — containee=%v container=%v",
			containeeTs, containerTs)
		// Transitive: consumer also before container (runtimeDependency
		// producer-side default edge: producer waits for consumer).
		assert.True(t,
			consumerTs.Before(containerTs),
			"consumer.delete must complete before container.delete (default destroy edge from runtimeDependency) — consumer=%v container=%v",
			consumerTs, containerTs)

		for _, ru := range destroyCmd.ResourceUpdates {
			assert.Equal(t, resource_update.ResourceUpdateStateSuccess, ru.State,
				"ResourceUpdate for %q should be successful", ru.DesiredState.Label)
		}
	})
}
