// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// spawnTargetUpdater spawns a TargetUpdater actor directly on the node,
// registered under the canonical name for the given (label, operation, commandID)
// tuple. from is the PID that will receive the TargetUpdateFinished message.
func spawnTargetUpdater(
	t *testing.T,
	node gen.Node,
	from gen.PID,
	label string,
	operation string,
	commandID string,
) error {
	t.Helper()
	name := actornames.TargetUpdater(label, operation, commandID)
	_, err := node.SpawnRegister(name, target_update.NewTargetUpdater, gen.ProcessOptions{}, from)
	return err
}

// TestTargetUpdater_ResolveOp_EmptyResolvables_SkipsPersist verifies that a
// Resolve op with zero resolvables never calls persistTarget — it must not write
// the target row and must still reach TargetUpdateStateSuccess, carrying the
// (unchanged) config in the finished signal.
func TestTargetUpdater_ResolveOp_EmptyResolvables_SkipsPersist(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 1)
		helperPID, err := testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		// A Resolve TU with no resolvables: the config has no $ref fields to fill in.
		consumerConfig := json.RawMessage(`{"region":"us-west-2"}`)
		tu := target_update.NewResolveTargetUpdate(
			pkgmodel.Target{
				Label:     "empty-consumer",
				Namespace: "FakeAWS",
				Config:    consumerConfig,
			},
			[]pkgmodel.FormaeURI{}, // deliberately empty
		)

		const commandID = "empty-resolve-cmd"

		require.NoError(t, spawnTargetUpdater(t, m.Node, helperPID,
			tu.Target.Label, string(tu.Operation), commandID))

		tuName := actornames.TargetUpdater(tu.Target.Label, string(tu.Operation), commandID)
		err = testutil.Send(m.Node, tuName, target_update.StartTargetUpdate{
			TargetUpdate: tu,
			CommandID:    commandID,
		})
		require.NoError(t, err)

		// (a) The FSM must finish successfully and propagate the config.
		testutil.ExpectMessageWithPredicate(t, received, 10*time.Second,
			func(msg target_update.TargetUpdateFinished) bool {
				assert.Equal(t, target_update.TargetUpdateStateSuccess, msg.State,
					"Resolve op with empty resolvables must finish successfully")
				require.NotNil(t, msg.ResolvedConfig,
					"Resolve op must propagate config even when there are no resolvables")
				return true
			},
		)

		// (b) The target row must NOT have been written to the datastore.
		consumerTarget, err := m.Datastore.LoadTarget("empty-consumer")
		require.NoError(t, err)
		assert.Nil(t, consumerTarget,
			"Resolve op must not persist the target row even when resolvables are empty")
	})
}

// TestTargetUpdater_ResolveOp_SkipsPersistAndSignalsResolvedConfig verifies that
// a Resolve op drives the full resolvable loop (mutating Target.Config), then
// terminates with TargetUpdateStateSuccess and the resolved config — without
// writing any target row to the datastore.
func TestTargetUpdater_ResolveOp_SkipsPersistAndSignalsResolvedConfig(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Plugin Read returns the resolved value so the ResolveCache can serve it.
		resourceKsuid := util.NewID()
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"BucketName":"my-cluster","Endpoint":"https://my-cluster.example.com"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 1)
		helperPID, err := testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		// Persist the provider target so the ResourcePersister can serve it
		// (required for the ResolveCache to look up the resource's target config).
		providerTarget := pkgmodel.Target{
			Label:     "provider",
			Namespace: "FakeAWS",
			Config:    json.RawMessage(`{"region":"us-east-1"}`),
		}
		_, err = testutil.Call(m.Node, "ResourcePersister", target_update.PersistTargetUpdates{
			TargetUpdates: []target_update.TargetUpdate{
				{
					Target:    providerTarget,
					Operation: target_update.TargetOperationCreate,
					State:     target_update.TargetUpdateStateNotStarted,
				},
			},
			CommandID: "resolve-test-cmd",
		})
		require.NoError(t, err)

		// Persist a resource so the ResolveCache can read the Endpoint property.
		resourceUpdate := &resource_update.ResourceUpdate{
			DesiredState: pkgmodel.Resource{
				Label:    "cluster",
				Type:     "FakeAWS::S3::Bucket",
				Stack:    "infra",
				Target:   "provider",
				NativeID: "native-cluster",
				Ksuid:    resourceKsuid,
				Schema:   pkgmodel.Schema{Identifier: "BucketName", Portable: true},
				Properties: json.RawMessage(
					`{"BucketName":"my-cluster","Endpoint":"https://my-cluster.example.com"}`,
				),
			},
			ResourceTarget: providerTarget,
			State:          resource_update.ResourceUpdateStateSuccess,
			Operation:      resource_update.OperationCreate,
			ProgressResult: []plugin.TrackedProgress{
				{
					ProgressResult: resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "req-1",
						NativeID:           "native-cluster",
						ResourceProperties: json.RawMessage(`{"BucketName":"my-cluster","Endpoint":"https://my-cluster.example.com"}`),
					},
					ResourceType: "FakeAWS::S3::Bucket",
					StartTs:      util.TimeNow(),
					ModifiedTs:   util.TimeNow(),
					Attempts:     1,
				},
			},
			RemainingResolvables: []pkgmodel.FormaeURI{},
			StackLabel:           "infra",
			GroupID:              "resolve-test-group",
		}
		_, err = testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  *resourceUpdate,
		})
		require.NoError(t, err)

		// Spawn the ResolveCache for this command so the TargetUpdater's resolve
		// loop can request values.
		require.NoError(t, spawnResolveCache(t, m.Node, "resolve-test-cmd"))

		// Build the Resolve TU: unchanged target whose config has a $ref to the
		// cluster's Endpoint. ExistingTarget is nil (Resolve ops are synthetic).
		resolvableURI := pkgmodel.NewFormaeURI(resourceKsuid, "Endpoint")
		consumerConfig := json.RawMessage(`{
			"endpoint": {"$ref": "` + string(resolvableURI) + `"}
		}`)
		tu := target_update.NewResolveTargetUpdate(
			pkgmodel.Target{
				Label:     "consumer",
				Namespace: "FakeAWS",
				Config:    consumerConfig,
			},
			[]pkgmodel.FormaeURI{resolvableURI},
		)

		const commandID = "resolve-test-cmd"

		// Spawn the TargetUpdater with the TestHelperActor as requester.
		require.NoError(t, spawnTargetUpdater(t, m.Node, helperPID,
			tu.Target.Label, string(tu.Operation), commandID))

		// Send StartTargetUpdate to kick off the FSM.
		tuName := actornames.TargetUpdater(tu.Target.Label, string(tu.Operation), commandID)
		err = testutil.Send(m.Node, tuName, target_update.StartTargetUpdate{
			TargetUpdate: tu,
			CommandID:    commandID,
		})
		require.NoError(t, err)

		// (a) Finished signal carries success state and the resolved config.
		testutil.ExpectMessageWithPredicate(t, received, 10*time.Second,
			func(msg target_update.TargetUpdateFinished) bool {
				assert.Equal(t, target_update.TargetUpdateStateSuccess, msg.State,
					"Resolve op must finish successfully")

				require.NotNil(t, msg.ResolvedConfig,
					"Resolve op must propagate resolved config")

				var cfg map[string]any
				require.NoError(t, json.Unmarshal(msg.ResolvedConfig, &cfg))
				endpointObj, ok := cfg["endpoint"].(map[string]any)
				require.True(t, ok, "endpoint must be a $ref object in resolved config")
				assert.Equal(t,
					"https://my-cluster.example.com",
					endpointObj["$value"],
					"resolved config must carry the resolved $value",
				)
				return true
			},
		)

		// (b) The target row must NOT have been written — Resolve ops are never persisted.
		consumerTarget, err := m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		assert.Nil(t, consumerTarget,
			"Resolve op must not persist the target row to the datastore")
	})
}
