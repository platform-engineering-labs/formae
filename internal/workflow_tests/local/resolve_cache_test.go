// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ergo.services/ergo/gen"

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
	"github.com/stretchr/testify/require"
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
// resolve-miss diagnosability gap: when a referenced property is
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

// persistResolveCacheTarget stores a target so LoadResource can return it as the
// resource's target.
func persistResolveCacheTarget(t *testing.T, node gen.Node, target pkgmodel.Target) {
	t.Helper()
	_, err := testutil.Call(node, "ResourcePersister", target_update.PersistTargetUpdates{
		TargetUpdates: []target_update.TargetUpdate{{
			Target:    target,
			Operation: target_update.TargetOperationCreate,
			State:     target_update.TargetUpdateStateNotStarted,
		}},
		CommandID: "test-command-1",
	})
	require.NoError(t, err)
}

// persistResolveCacheResource stores a successfully created resource so the
// ResolveCache can load it and read it back through the plugin.
func persistResolveCacheResource(t *testing.T, node gen.Node, res pkgmodel.Resource, target pkgmodel.Target) {
	t.Helper()
	hash, err := testutil.Call(node, "ResourcePersister", resource_update.PersistResourceUpdate{
		PluginOperation: resource.OperationCreate,
		ResourceUpdate: resource_update.ResourceUpdate{
			DesiredState:   res,
			ResourceTarget: target,
			State:          resource_update.ResourceUpdateStateSuccess,
			Version:        "test-persist-hash-" + res.Label,
			ProgressResult: []plugin.TrackedProgress{{
				ProgressResult: resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           res.NativeID,
					ResourceProperties: res.Properties,
				},
				ResourceType: res.Type,
				StartTs:      util.TimeNow(),
				ModifiedTs:   util.TimeNow(),
				Attempts:     1,
			}},
			RemainingResolvables: []pkgmodel.FormaeURI{},
			StackLabel:           res.Stack,
			GroupID:              "test-group-id-1",
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, hash)
}

// TestResolveCache_ThrottledReadResolvesThroughOperatorRetry: a resolve whose
// plugin Read is throttled on its first attempt resolves once the
// PluginOperator's own retry succeeds. The operator owns the retry ladder, so
// the read runs exactly once per attempt: no second operator is spawned for the
// same resolve, and the operator's pushed progress is consumed rather than
// logged as an unknown message. The resolved properties are cached like any
// other, so a second resolve of the same resource issues no read.
func TestResolveCache_ThrottledReadResolvesThroughOperatorRetry(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		logs := test_helpers.SetupTestLogger()

		var reads atomic.Int32
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				if reads.Add(1) == 1 {
					return &resource.ReadResult{
						ResourceType: "FakeAWS::S3::Bucket",
						ErrorCode:    resource.OperationErrorCodeThrottling,
					}, nil
				}
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"name":"bucket1"}`,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Retry.RetryDelay = 100 * time.Millisecond
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		target := pkgmodel.Target{Label: "test-target", Namespace: "test-namespace", Config: json.RawMessage(`{}`)}
		persistResolveCacheTarget(t, m.Node, target)
		bucket := pkgmodel.Resource{
			Label:      "resource-1",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name":"bucket1"}`),
			Stack:      "test-stack",
			Target:     target.Label,
			NativeID:   "test-native-id-1",
			Ksuid:      util.NewID(),
		}
		persistResolveCacheResource(t, m.Node, bucket, target)
		require.NoError(t, spawnResolveCache(t, m.Node, "test-command-1"))

		uri := pkgmodel.NewFormaeURI(bucket.Ksuid, "name")
		testutil.Send(m.Node, actornames.ResolveCache("test-command-1"), messages.ResolveValue{ResourceURI: uri})

		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			resolved, ok := msg.(messages.ValueResolved)
			require.True(t, ok, "expected ValueResolved, got %T", msg)
			return resolved.Value == "bucket1"
		})

		// Give any second retry ladder time to fire before counting.
		time.Sleep(5 * cfg.Agent.Retry.RetryDelay)
		assert.Equal(t, int32(2), reads.Load(), "one read per operator attempt: the throttled first attempt and its retry")
		assert.False(t, logs.ContainsAll("Received unknown message type"),
			"the operator's pushed progress must be consumed, not logged as unknown")

		testutil.Send(m.Node, actornames.ResolveCache("test-command-1"), messages.ResolveValue{ResourceURI: uri})
		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			resolved, ok := msg.(messages.ValueResolved)
			require.True(t, ok, "expected ValueResolved, got %T", msg)
			return resolved.Value == "bucket1"
		})
		assert.Equal(t, int32(2), reads.Load(), "a resolve completed via the operator's retry is cached like any other")
	})
}

// TestResolveCache_ExhaustedReadRetriesFailTheResolve: when every attempt of
// the PluginOperator's ladder is throttled, the resolve fails with a Reason
// naming the error, and only after the operator has given up: no read is
// issued once the failure has been reported.
func TestResolveCache_ExhaustedReadRetriesFailTheResolve(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var reads atomic.Int32
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				reads.Add(1)
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					ErrorCode:    resource.OperationErrorCodeThrottling,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Retry.MaxRetries = 0
		cfg.Agent.Retry.RetryDelay = 50 * time.Millisecond
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		target := pkgmodel.Target{Label: "test-target", Namespace: "test-namespace", Config: json.RawMessage(`{}`)}
		persistResolveCacheTarget(t, m.Node, target)
		bucket := pkgmodel.Resource{
			Label:      "resource-1",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name":"bucket1"}`),
			Stack:      "test-stack",
			Target:     target.Label,
			NativeID:   "test-native-id-1",
			Ksuid:      util.NewID(),
		}
		persistResolveCacheResource(t, m.Node, bucket, target)
		require.NoError(t, spawnResolveCache(t, m.Node, "test-command-1"))

		uri := pkgmodel.NewFormaeURI(bucket.Ksuid, "name")
		testutil.Send(m.Node, actornames.ResolveCache("test-command-1"), messages.ResolveValue{ResourceURI: uri})

		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			failed, ok := msg.(messages.FailedToResolveValue)
			require.True(t, ok, "expected FailedToResolveValue, got %T", msg)
			assert.Contains(t, failed.Reason, string(resource.OperationErrorCodeThrottling),
				"a terminal read failure must name the error the operator gave up on")
			return true
		})

		readsAtFailure := reads.Load()
		time.Sleep(5 * cfg.Agent.Retry.RetryDelay)
		assert.Equal(t, readsAtFailure, reads.Load(),
			"the failure is reported once the operator has given up; no orphaned operator keeps reading")
	})
}

// TestResolveCache_ThrottledSecretSourceReadResolvesThroughOperatorRetry: a
// resource on a target whose config holds an opaque $ref resolves that ref by
// reading the secret through the same operator-owned retry ladder as any other
// read. A throttled first attempt on the secret is retried by its operator
// once, the resolved plaintext reaches the resource's Read, and the secret's
// properties are cached so a second resource on the same target reads the
// secret no further time.
func TestResolveCache_ThrottledSecretSourceReadResolvesThroughOperatorRetry(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		logs := test_helpers.SetupTestLogger()
		const plaintext = "resolve-cache-secret-value"

		var secretReads atomic.Int32
		var bucketConfigs sync.Map
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				switch request.ResourceType {
				case "FakeAWS::SecretsManager::Secret":
					if secretReads.Add(1) == 1 {
						return &resource.ReadResult{ResourceType: request.ResourceType, ErrorCode: resource.OperationErrorCodeThrottling}, nil
					}
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   fmt.Sprintf(`{"Name":"my-secret","SecretString":%q}`, plaintext),
					}, nil
				case "FakeAWS::S3::Bucket":
					bucketConfigs.Store(request.NativeID, string(request.TargetConfig))
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   fmt.Sprintf(`{"name":%q}`, request.NativeID),
					}, nil
				}
				return nil, fmt.Errorf("unexpected resource type in Read: %s", request.ResourceType)
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Retry.RetryDelay = 100 * time.Millisecond
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		provider := pkgmodel.Target{Label: "provider", Namespace: "test-namespace", Config: json.RawMessage(`{}`)}
		persistResolveCacheTarget(t, m.Node, provider)
		secret := pkgmodel.Resource{
			Label:    "my-secret",
			Type:     "FakeAWS::SecretsManager::Secret",
			Stack:    "test-stack",
			Target:   provider.Label,
			NativeID: "secret-native-id",
			Ksuid:    util.NewID(),
			Schema: pkgmodel.Schema{
				Identifier: "Id",
				Fields:     []string{"Name", "SecretString"},
				Hints:      map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true}},
			},
			Properties: json.RawMessage(fmt.Sprintf(`{"Name":"my-secret","SecretString":%q}`, plaintext)),
		}
		persistResolveCacheResource(t, m.Node, secret, provider)

		secretTarget := pkgmodel.Target{
			Label:     "secret-target",
			Namespace: "test-namespace",
			Config: json.RawMessage(fmt.Sprintf(
				`{"region":"us-east-1","apiKey":{"$ref":"formae://%s#/SecretString","$visibility":"Opaque"}}`, secret.Ksuid)),
		}
		persistResolveCacheTarget(t, m.Node, secretTarget)
		bucket1 := pkgmodel.Resource{
			Label:      "bucket-1",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name":"bucket-1"}`),
			Stack:      "test-stack",
			Target:     secretTarget.Label,
			NativeID:   "bucket-1",
			Ksuid:      util.NewID(),
		}
		bucket2 := bucket1
		bucket2.Label, bucket2.NativeID, bucket2.Ksuid = "bucket-2", "bucket-2", util.NewID()
		bucket2.Properties = json.RawMessage(`{"name":"bucket-2"}`)
		persistResolveCacheResource(t, m.Node, bucket1, secretTarget)
		persistResolveCacheResource(t, m.Node, bucket2, secretTarget)
		require.NoError(t, spawnResolveCache(t, m.Node, "test-command-1"))

		testutil.Send(m.Node, actornames.ResolveCache("test-command-1"),
			messages.ResolveValue{ResourceURI: pkgmodel.NewFormaeURI(bucket1.Ksuid, "name")})
		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			resolved, ok := msg.(messages.ValueResolved)
			require.True(t, ok, "expected ValueResolved, got %T", msg)
			return resolved.Value == "bucket-1"
		})

		time.Sleep(5 * cfg.Agent.Retry.RetryDelay)
		assert.Equal(t, int32(2), secretReads.Load(), "the throttled secret read is retried by its own operator exactly once")
		assert.False(t, logs.ContainsAll("Received unknown message type"),
			"the operator's pushed progress must be consumed, not logged as unknown")
		cfg1, ok := bucketConfigs.Load("bucket-1")
		require.True(t, ok, "bucket-1 must have been read")
		assert.Contains(t, cfg1.(string), plaintext, "the resource Read must receive the resolved credential")
		assert.NotContains(t, cfg1.(string), "$ref", "the resource Read must not receive a raw $ref")

		testutil.Send(m.Node, actornames.ResolveCache("test-command-1"),
			messages.ResolveValue{ResourceURI: pkgmodel.NewFormaeURI(bucket2.Ksuid, "name")})
		testutil.ExpectMessageWithPredicate(t, received, 5*time.Second, func(msg any) bool {
			resolved, ok := msg.(messages.ValueResolved)
			require.True(t, ok, "expected ValueResolved, got %T", msg)
			return resolved.Value == "bucket-2"
		})
		assert.Equal(t, int32(2), secretReads.Load(), "a second resource on the same target reuses the cached secret")
		cfg2, ok := bucketConfigs.Load("bucket-2")
		require.True(t, ok, "bucket-2 must have been read")
		assert.Contains(t, cfg2.(string), plaintext)
	})
}
