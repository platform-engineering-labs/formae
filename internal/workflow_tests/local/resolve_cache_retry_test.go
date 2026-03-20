// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveCache_WaitsForRecoverableReadRetry(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var readCalls atomic.Int32
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				attempt := readCalls.Add(1)
				if attempt == 1 {
					return &resource.ReadResult{ResourceType: request.ResourceType, ErrorCode: resource.OperationErrorCodeNetworkFailure}, nil
				}
				return &resource.ReadResult{ResourceType: request.ResourceType, Properties: `{"name":"bucket1"}`}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Retry.StatusCheckInterval = 100 * time.Millisecond
		cfg.Agent.Retry.RetryDelay = 50 * time.Millisecond
		cfg.Agent.Retry.MaxRetries = 2

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 2)
		_, err = testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		target := pkgmodel.Target{Label: "test-target", Namespace: "test-namespace", Config: json.RawMessage(`{}`)}
		_, err = testutil.Call(m.Node, "ResourcePersister", target_update.PersistTargetUpdates{TargetUpdates: []target_update.TargetUpdate{{Target: target, Operation: target_update.TargetOperationCreate, State: target_update.TargetUpdateStateNotStarted}}, CommandID: "test-command-1"})
		require.NoError(t, err)

		resourceUpdate := &resource_update.ResourceUpdate{
			DesiredState:         pkgmodel.Resource{Label: "resource-1", Type: "FakeAWS::S3::Bucket", Properties: json.RawMessage(`{"name":"bucket1"}`), Stack: "test-stack", Target: "test-target", NativeID: "test-native-id-1", Ksuid: util.NewID()},
			ResourceTarget:       target,
			State:                resource_update.ResourceUpdateStateSuccess,
			Version:              "test-persist-hash-1",
			ProgressResult:       []plugin.TrackedProgress{{ProgressResult: resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, RequestID: "test-request-id-1", NativeID: "test-native-id-1", ResourceProperties: json.RawMessage(`{"name":"bucket1"}`)}, ResourceType: "FakeAWS::S3::Bucket", StartTs: util.TimeNow(), ModifiedTs: util.TimeNow(), Attempts: 1}},
			RemainingResolvables: []pkgmodel.FormaeURI{},
			StackLabel:           "test-stack",
			GroupID:              "test-group-id-1",
		}

		_, err = testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{PluginOperation: resource.OperationCreate, ResourceUpdate: *resourceUpdate})
		require.NoError(t, err)

		_, err = testutil.Call(m.Node, "ChangesetSupervisor", changeset.EnsureResolveCache{CommandID: "test-command-1"})
		require.NoError(t, err)

		uri := pkgmodel.NewFormaeURI(resourceUpdate.DesiredState.Ksuid, "name")
		testutil.Send(m.Node, actornames.ResolveCache("test-command-1"), messages.ResolveValue{ResourceURI: uri})

		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			resolvedValue, ok := msg.(messages.ValueResolved)
			return ok && resolvedValue.Value == "bucket1"
		})

		assert.Equal(t, int32(2), readCalls.Load())
	})
}

func TestResolveCache_FailsAfterFinalRecoverableReadFailure(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var readCalls atomic.Int32
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				readCalls.Add(1)
				return &resource.ReadResult{ResourceType: request.ResourceType, ErrorCode: resource.OperationErrorCodeNetworkFailure}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Retry.StatusCheckInterval = 100 * time.Millisecond
		cfg.Agent.Retry.RetryDelay = 50 * time.Millisecond
		cfg.Agent.Retry.MaxRetries = 2

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 2)
		_, err = testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		target := pkgmodel.Target{Label: "test-target", Namespace: "test-namespace", Config: json.RawMessage(`{}`)}
		_, err = testutil.Call(m.Node, "ResourcePersister", target_update.PersistTargetUpdates{TargetUpdates: []target_update.TargetUpdate{{Target: target, Operation: target_update.TargetOperationCreate, State: target_update.TargetUpdateStateNotStarted}}, CommandID: "test-command-1"})
		require.NoError(t, err)

		resourceUpdate := &resource_update.ResourceUpdate{
			DesiredState:         pkgmodel.Resource{Label: "resource-1", Type: "FakeAWS::S3::Bucket", Properties: json.RawMessage(`{"name":"bucket1"}`), Stack: "test-stack", Target: "test-target", NativeID: "test-native-id-1", Ksuid: util.NewID()},
			ResourceTarget:       target,
			State:                resource_update.ResourceUpdateStateSuccess,
			Version:              "test-persist-hash-1",
			ProgressResult:       []plugin.TrackedProgress{{ProgressResult: resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, RequestID: "test-request-id-1", NativeID: "test-native-id-1", ResourceProperties: json.RawMessage(`{"name":"bucket1"}`)}, ResourceType: "FakeAWS::S3::Bucket", StartTs: util.TimeNow(), ModifiedTs: util.TimeNow(), Attempts: 1}},
			RemainingResolvables: []pkgmodel.FormaeURI{},
			StackLabel:           "test-stack",
			GroupID:              "test-group-id-1",
		}

		_, err = testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{PluginOperation: resource.OperationCreate, ResourceUpdate: *resourceUpdate})
		require.NoError(t, err)

		_, err = testutil.Call(m.Node, "ChangesetSupervisor", changeset.EnsureResolveCache{CommandID: "test-command-1"})
		require.NoError(t, err)

		uri := pkgmodel.NewFormaeURI(resourceUpdate.DesiredState.Ksuid, "name")
		testutil.Send(m.Node, actornames.ResolveCache("test-command-1"), messages.ResolveValue{ResourceURI: uri})

		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			_, ok := msg.(messages.FailedToResolveValue)
			return ok
		})

		assert.Equal(t, int32(4), readCalls.Load())
	})
}
