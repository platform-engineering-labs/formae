// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package workflow_tests_local

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

// resourceOperationFailuresMetric is the counter the resource-update tier emits
// when an operation reaches terminal failure.
const resourceOperationFailuresMetric = "formae.resource.operation.failures"

// operationFailureCount sums resourceOperationFailuresMetric over the data
// points carrying every label in want, and returns 0 while the counter has not
// been emitted yet. It takes no *testing.T because it is polled from
// assert.Eventually's goroutine.
func operationFailureCount(reader *sdkmetric.ManualReader, want map[string]string) int64 {
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		return -1
	}

	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != resourceOperationFailuresMetric {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				return -1
			}
			for _, dp := range sum.DataPoints {
				matches := true
				for k, v := range want {
					got, found := dp.Attributes.Value(attribute.Key(k))
					if !found || got.Emit() != v {
						matches = false
						break
					}
				}
				if matches {
					total += dp.Value
				}
			}
		}
	}
	return total
}

func TestMetastructure_Stats(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// This test drives a whole metastructure, so the counter is read through
		// a provider installed for the test binary rather than injected per
		// spawn. That also exercises the production path, where the
		// ResourceUpdater resolves the global provider at spawn time.
		reader := sdkmetric.NewManualReader()
		otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))

		// The FakeAWS::S3::Bucket create below is driven to terminal failure by
		// the Create override, so it is counted at the create stage.
		failedCreate := map[string]string{
			"resource_type": "FakeAWS::S3::Bucket",
			"operation":     "create",
			"plugin":        "FakeAWS",
			"failure_stage": "creating",
		}

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if request.Label == "test-resource-fail" {
					return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusFailure,
						RequestID:       "1234",
						NativeID:        "5678",
						StatusMessage:   "Simulated failure",
					}}, nil
				} else {
					return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "1234",
						NativeID:        "5678",
					}}, nil
				}
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		formaInitial := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack",
					Target:     "test-target",
					Managed:    true,
				},
				{
					Label:      "test-resource-fail",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack",
					Target:     "test-target",
					Managed:    true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "FakeAWS",
				},
			},
		}

		_, err = m.ApplyForma(formaInitial, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client-id")

		assert.NoError(t, err)

		time.Sleep(2 * time.Second)

		stats, err := m.Stats()
		assert.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Equal(t, stats.Version, formae.Version)
		assert.Equal(t, stats.AgentID, m.AgentID)
		assert.Equal(t, stats.Clients, 1)

		// There should be 2 commands: 1 apply and 1 destroy
		assert.Equal(t, 1, len(stats.Commands))
		assert.Equal(t, 1, stats.Commands[string(pkgmodel.CommandApply)])
		assert.Equal(t, 0, stats.Commands[string(pkgmodel.CommandDestroy)])

		assert.Equal(t, 1, len(stats.States))
		assert.Equal(t, 0, stats.States[string(forma_command.CommandStateSuccess)])
		assert.Equal(t, 1, stats.States[string(forma_command.CommandStateFailed)])

		assert.Equal(t, 1, stats.Stacks)
		assert.Equal(t, 1, stats.ManagedResources["FakeAWS"])
		assert.Equal(t, 0, stats.UnmanagedResources["FakeAWS"])

		assert.Equal(t, 1, stats.Targets["FakeAWS"])

		assert.Equal(t, 1, len(stats.ResourceTypes))

		// Apply completion and the terminal state callback are not ordered with
		// respect to each other, so poll rather than sampling once.
		assert.Eventually(t, func() bool {
			return operationFailureCount(reader, failedCreate) == 1
		}, 10*time.Second, 100*time.Millisecond,
			"the failed create must be counted once at the resource-update tier")

		_, err = m.DestroyForma(formaInitial, &config.FormaCommandConfig{}, "test-client-id2")

		assert.NoError(t, err)

		time.Sleep(2 * time.Second)

		stats, err = m.Stats()
		assert.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Equal(t, stats.Version, formae.Version)
		assert.Equal(t, stats.AgentID, m.AgentID)
		assert.Equal(t, stats.Clients, 2)

		// There should be 2 commands: 1 apply and 1 destroy
		assert.Equal(t, 2, len(stats.Commands))
		assert.Equal(t, 1, stats.Commands[string(pkgmodel.CommandApply)])
		assert.Equal(t, 1, stats.Commands[string(pkgmodel.CommandDestroy)])

		assert.Equal(t, 2, len(stats.States))
		assert.Equal(t, 1, stats.States[string(forma_command.CommandStateSuccess)])
		assert.Equal(t, 1, stats.States[string(forma_command.CommandStateFailed)])

		assert.Equal(t, 0, stats.Stacks)
		assert.Equal(t, 0, stats.ManagedResources["FakeAWS"])
		assert.Equal(t, 0, stats.UnmanagedResources["FakeAWS"])

		assert.Equal(t, 1, stats.Targets["FakeAWS"]) // plain targets (no $ref) survive destroy

		// The destroy generates no delete for the failed create — it never got a
		// live resource row — and the Delete override succeeds for the rest, so
		// the count observed after the apply is unchanged.
		assert.Equal(t, int64(1), operationFailureCount(reader, failedCreate),
			"destroy must not add a resource-operation failure")
		assert.Equal(t, int64(1), operationFailureCount(reader, map[string]string{}),
			"the failed create is the only counted operation failure")
	})
}

// TestMetastructure_Stats_ReapPendingAndReapedTargets verifies the new
// reap-pending/reaped counts on the stats surface: an unreachable target that
// has crossed its own reap-after threshold is counted under
// ReapPendingTargets before any tombstone, and moves to ReapedTargets once
// PersistTargetReap actually commits.
func TestMetastructure_Stats_ReapPendingAndReapedTargets(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, &plugin.ResourcePluginOverrides{})
		defer def()
		assert.NoError(t, err)

		label := "stats-reap-target"
		reaping, err := pkgmodel.MarshalReaping(&pkgmodel.ReapAfter{Kind: "after", MaxUnreachableSeconds: 0})
		assert.NoError(t, err)
		_, err = m.Datastore.CreateTarget(&pkgmodel.Target{
			Label:     label,
			Namespace: "FakeAWS",
			Reaping:   reaping,
		})
		assert.NoError(t, err)

		stats, err := m.Stats()
		assert.NoError(t, err)
		assert.Equal(t, 0, stats.ReapPendingTargets, "a reachable/unknown target must not count as reap-pending")
		assert.Equal(t, 0, stats.ReapedTargets)

		seenAt := time.Now().UTC()
		observedAt := time.Now().UTC()
		applied, err := m.Datastore.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel: label,
			State:       pkgmodel.TargetHealthStateUnreachable,
			ObservedAt:  observedAt,
			LastSeenAt:  &seenAt,
		})
		assert.NoError(t, err)
		assert.True(t, applied)

		target, err := m.Datastore.LoadTarget(label)
		assert.NoError(t, err)
		assert.NotNil(t, target.Health)

		sampleAt := time.Now().UTC()
		applied, err = m.Datastore.AdvanceTargetAccrual(label, target.Health.IncarnationID, sampleAt, 0)
		assert.NoError(t, err)
		assert.True(t, applied)

		stats, err = m.Stats()
		assert.NoError(t, err)
		assert.Equal(t, 1, stats.ReapPendingTargets, "an over-threshold unreachable target must surface as reap-pending before any tombstone")
		assert.Equal(t, 0, stats.ReapedTargets)

		cutoff := time.Now().UTC()
		reaped, _, err := m.Datastore.PersistTargetReap(datastore.PersistTargetReapRequest{
			Label:            label,
			IncarnationID:    target.Health.IncarnationID,
			LastSeenBefore:   cutoff,
			LastSampleBefore: cutoff,
			ReapedAt:         cutoff,
		})
		assert.NoError(t, err)
		assert.True(t, reaped)

		stats, err = m.Stats()
		assert.NoError(t, err)
		assert.Equal(t, 0, stats.ReapPendingTargets, "a reaped target must no longer count as reap-pending")
		assert.Equal(t, 1, stats.ReapedTargets)
	})
}
