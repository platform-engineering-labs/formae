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
// Resolve op with zero resolvables never re-writes the target row — the row's
// Version must be identical before and after the op — and must still reach
// TargetUpdateStateSuccess, carrying the (unchanged) config in the finished signal.
func TestTargetUpdater_ResolveOp_EmptyResolvables_SkipsPersist(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 1)
		helperPID, err := testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		// Persist the consumer target so revalidation (which re-reads the live row at
		// execute time) can confirm it still exists. In production, synthesizeResolveTargetUpdates
		// always operates on already-persisted targets; this mirrors that precondition.
		consumerConfig := json.RawMessage(`{"region":"us-west-2"}`)
		_, err = testutil.Call(m.Node, "ResourcePersister", target_update.PersistTargetUpdates{
			TargetUpdates: []target_update.TargetUpdate{
				{
					Target: pkgmodel.Target{
						Label:     "empty-consumer",
						Namespace: "FakeAWS",
						Config:    consumerConfig,
					},
					Operation: target_update.TargetOperationCreate,
					State:     target_update.TargetUpdateStateNotStarted,
				},
			},
			CommandID: "empty-resolve-cmd",
		})
		require.NoError(t, err)

		// Load the persisted target to capture its Version, then build the Resolve TU
		// from it so the snapshot Version matches the live row (no stale-snapshot rebuild).
		persistedConsumer, err := m.Datastore.LoadTarget("empty-consumer")
		require.NoError(t, err)
		require.NotNil(t, persistedConsumer)
		versionBeforeOp := persistedConsumer.Version

		tu := target_update.NewResolveTargetUpdate(
			*persistedConsumer,
			[]pkgmodel.FormaeURI{}, // deliberately empty — no resolvables to process
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

		// (b) The Resolve op must not re-write the target row: the Version must be
		// identical to what was in place before the op ran.
		consumerTarget, err := m.Datastore.LoadTarget("empty-consumer")
		require.NoError(t, err)
		require.NotNil(t, consumerTarget,
			"target row must still exist after a Resolve op")
		assert.Equal(t, versionBeforeOp, consumerTarget.Version,
			"Resolve op must not bump the target's Version — it must not write the target row")
	})
}

// TestTargetUpdater_ResolveOp_SkipsPersistAndSignalsResolvedConfig verifies that
// a Resolve op drives the full resolvable loop (mutating Target.Config), then
// terminates with TargetUpdateStateSuccess and the resolved config — without
// re-writing the target row to the datastore (Version must be unchanged).
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

		// Persist the consumer target so revalidation (which re-reads the live row at
		// execute time) can confirm it still exists and its config is current.
		// In production, synthesizeResolveTargetUpdates always operates on
		// already-persisted targets; this mirrors that precondition.
		resolvableURI := pkgmodel.NewFormaeURI(resourceKsuid, "Endpoint")
		consumerConfig := json.RawMessage(`{
			"endpoint": {"$ref": "` + string(resolvableURI) + `"}
		}`)
		_, err = testutil.Call(m.Node, "ResourcePersister", target_update.PersistTargetUpdates{
			TargetUpdates: []target_update.TargetUpdate{
				{
					Target: pkgmodel.Target{
						Label:     "consumer",
						Namespace: "FakeAWS",
						Config:    consumerConfig,
					},
					Operation: target_update.TargetOperationCreate,
					State:     target_update.TargetUpdateStateNotStarted,
				},
			},
			CommandID: "resolve-test-cmd",
		})
		require.NoError(t, err)

		// Load the persisted consumer target to capture its Version, then build the
		// Resolve TU from it so the snapshot Version matches the live row (revalidation
		// sees an unchanged version and does not rebuild resolvables).
		persistedConsumer, err := m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		require.NotNil(t, persistedConsumer)
		versionBeforeOp := persistedConsumer.Version

		// Build the Resolve TU from the persisted target snapshot. The $ref in Config
		// will be resolved in-memory during the op without touching the persisted row.
		tu := target_update.NewResolveTargetUpdate(
			*persistedConsumer,
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

		// (b) The Resolve op must not re-write the target row: the Version must be
		// identical to what was in place before the op ran.
		consumerTarget, err := m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		require.NotNil(t, consumerTarget,
			"target row must still exist after a Resolve op")
		assert.Equal(t, versionBeforeOp, consumerTarget.Version,
			"Resolve op must not bump the target's Version — it must not write the target row")
	})
}
