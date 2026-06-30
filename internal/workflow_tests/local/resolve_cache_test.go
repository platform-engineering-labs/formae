// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
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
)

func TestResolveCache(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		callsToReadOperation := 0
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				callsToReadOperation++
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"name":"bucket1"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		// start test helper actor to interact with the actors in the metastructure
		received := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, received)
		assert.NoError(t, err)

		target := pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
			Config:    json.RawMessage(`{}`),
		}

		targetUpdate := target_update.TargetUpdate{
			Target:    target,
			Operation: target_update.TargetOperationCreate,
			State:     target_update.TargetUpdateStateNotStarted,
		}

		_, err = testutil.Call(m.Node, "ResourcePersister", target_update.PersistTargetUpdates{
			TargetUpdates: []target_update.TargetUpdate{targetUpdate},
			CommandID:     "test-command-1",
		})
		assert.NoError(t, err)

		// store the resource
		resourceUpdate := &resource_update.ResourceUpdate{
			DesiredState: pkgmodel.Resource{
				Label:      "resource-1",
				Type:       "FakeAWS::S3::Bucket",
				Properties: json.RawMessage(`{"name":"bucket1"}`),
				Stack:      "test-stack",
				Target:     "test-target",
				NativeID:   "test-native-id-1",
				Ksuid:      util.NewID(),
			},
			ResourceTarget: target,
			State:          resource_update.ResourceUpdateStateSuccess,
			Version:        "test-persist-hash-1",
			ProgressResult: []plugin.TrackedProgress{
				{
					ProgressResult: resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "test-request-id-1",
						NativeID:           "test-native-id-1",
						ResourceProperties: json.RawMessage(`{"name":"bucket1"}`),
					},
					ResourceType: "FakeAWS::S3::Bucket",
					StartTs:      util.TimeNow(),
					ModifiedTs:   util.TimeNow(),
					Attempts:     1,
				},
			},
			RemainingResolvables: []pkgmodel.FormaeURI{},
			StackLabel:           "test-stack",
			GroupID:              "test-group-id-1",
		}

		hash, err := testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  *resourceUpdate,
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, hash)

		// ensure the resolve cache exists
		err = spawnResolveCache(t, m.Node, "test-command-1")

		uri := pkgmodel.NewFormaeURI(resourceUpdate.DesiredState.Ksuid, "name")

		// resolve the value
		testutil.Send(m.Node,
			actornames.ResolveCache("test-command-1"),
			messages.ResolveValue{
				ResourceURI: uri,
			})

		// assert that the value is correctly resolved
		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			resolvedValue, ok := msg.(messages.ValueResolved)
			if !ok {
				t.Fatalf("Expected ValueResolved message, got %T", resolvedValue)
			}
			return resolvedValue.Value == "bucket1"
		})

		// assert we called the plugin once (cache miss)
		assert.Equal(t, 1, callsToReadOperation)

		// resolve the value again
		testutil.Send(m.Node,
			actornames.ResolveCache("test-command-1"),
			messages.ResolveValue{
				ResourceURI: uri,
			})

		// assert that the value is correctly resolved
		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			resolvedValue, ok := msg.(messages.ValueResolved)
			if !ok {
				t.Fatalf("Expected ValueResolved message, got %T", resolvedValue)
			}
			return resolvedValue.Value == "bucket1"
		})

		// assert we didn't call the plugin again (cache hit)
		assert.Equal(t, 1, callsToReadOperation)
	})
}

// TestResolveCache_MissingPropertyReportsReason covers the terminal
// resolve-miss diagnosability gap (PLA-25): when a referenced property is
// absent from the source resource after a successful Read, the ResolveCache
// must report *why* it failed — naming the reference and the missing
// property — rather than sending an empty failure that surfaces as a blank
// ErrorMessage. It exercises both terminal-miss branches: the post-read miss
// (cache miss → read → property absent) and the subsequent cache-hit miss.
func TestResolveCache_MissingPropertyReportsReason(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"name":"bucket1"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		received := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, received)
		assert.NoError(t, err)

		target := pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
			Config:    json.RawMessage(`{}`),
		}

		targetUpdate := target_update.TargetUpdate{
			Target:    target,
			Operation: target_update.TargetOperationCreate,
			State:     target_update.TargetUpdateStateNotStarted,
		}

		_, err = testutil.Call(m.Node, "ResourcePersister", target_update.PersistTargetUpdates{
			TargetUpdates: []target_update.TargetUpdate{targetUpdate},
			CommandID:     "test-command-1",
		})
		assert.NoError(t, err)

		resourceUpdate := &resource_update.ResourceUpdate{
			DesiredState: pkgmodel.Resource{
				Label:      "resource-1",
				Type:       "FakeAWS::S3::Bucket",
				Properties: json.RawMessage(`{"name":"bucket1"}`),
				Stack:      "test-stack",
				Target:     "test-target",
				NativeID:   "test-native-id-1",
				Ksuid:      util.NewID(),
			},
			ResourceTarget: target,
			State:          resource_update.ResourceUpdateStateSuccess,
			Version:        "test-persist-hash-1",
			ProgressResult: []plugin.TrackedProgress{
				{
					ProgressResult: resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "test-request-id-1",
						NativeID:           "test-native-id-1",
						ResourceProperties: json.RawMessage(`{"name":"bucket1"}`),
					},
					ResourceType: "FakeAWS::S3::Bucket",
					StartTs:      util.TimeNow(),
					ModifiedTs:   util.TimeNow(),
					Attempts:     1,
				},
			},
			RemainingResolvables: []pkgmodel.FormaeURI{},
			StackLabel:           "test-stack",
			GroupID:              "test-group-id-1",
		}

		hash, err := testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  *resourceUpdate,
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, hash)

		err = spawnResolveCache(t, m.Node, "test-command-1")
		assert.NoError(t, err)

		// "arn" is not a property of the read result — this resolves terminally.
		missingURI := pkgmodel.NewFormaeURI(resourceUpdate.DesiredState.Ksuid, "arn")

		// First attempt: cache miss -> read -> property absent (post-read miss).
		testutil.Send(m.Node,
			actornames.ResolveCache("test-command-1"),
			messages.ResolveValue{ResourceURI: missingURI})

		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			failed, ok := msg.(messages.FailedToResolveValue)
			if !ok {
				t.Fatalf("Expected FailedToResolveValue message, got %T", msg)
			}
			assert.NotEmpty(t, failed.Reason,
				"a terminal resolve miss must carry a Reason so the operator sees the cause")
			assert.Contains(t, failed.Reason, "arn",
				"Reason must name the property that could not be resolved")
			assert.Contains(t, failed.Reason, "resource-1",
				"Reason must name the source resource the property is missing from")
			return true
		})

		// Second attempt for the same property: the resource is now cached, so
		// this exercises the cache-hit miss branch, which must also report a Reason.
		testutil.Send(m.Node,
			actornames.ResolveCache("test-command-1"),
			messages.ResolveValue{ResourceURI: missingURI})

		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			failed, ok := msg.(messages.FailedToResolveValue)
			if !ok {
				t.Fatalf("Expected FailedToResolveValue message, got %T", msg)
			}
			assert.NotEmpty(t, failed.Reason,
				"a cache-hit terminal miss must also carry a Reason")
			assert.Contains(t, failed.Reason, "arn",
				"Reason must name the property that could not be resolved")
			return true
		})
	})
}
