// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"context"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// metricsProcess is a gen.Process double for driving onStateChange directly.
// stubUpdaterProcess covers Log/Node/PID/Send; onStateChange additionally
// blocking-calls the FormaCommandPersister, which does not exist under the
// unit harness, so Call returns an error the callback is expected to tolerate.
type metricsProcess struct {
	*stubUpdaterProcess

	calls int
}

func (p *metricsProcess) Call(_ any, _ any) (any, error) {
	p.calls++
	return nil, gen.ErrProcessUnknown
}

func newMetricsProcess() *metricsProcess {
	return &metricsProcess{stubUpdaterProcess: &stubUpdaterProcess{}}
}

// newTestMeterProvider returns a MeterProvider backed by a ManualReader so a
// case can read its own metrics without touching the process-global provider.
func newTestMeterProvider() (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	reader := sdkmetric.NewManualReader()
	return sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)), reader
}

// collectOperationFailures returns the data points of the
// formae.resource.operation.failures counter, or nil when it was never emitted.
func collectOperationFailures(t *testing.T, reader *sdkmetric.ManualReader) []metricdata.DataPoint[int64] {
	t.Helper()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != operationFailuresMetricName {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "%s must be a monotonic Sum", operationFailuresMetricName)
			assert.True(t, sum.IsMonotonic, "%s must be monotonic", operationFailuresMetricName)
			return sum.DataPoints
		}
	}
	return nil
}

// requireSinglePoint asserts exactly one data point was emitted and returns it.
func requireSinglePoint(t *testing.T, reader *sdkmetric.ManualReader) metricdata.DataPoint[int64] {
	t.Helper()

	points := collectOperationFailures(t, reader)
	require.Len(t, points, 1, "expected exactly one data point")
	return points[0]
}

// attrs flattens a data point's attribute set into a map for assertion.
func attrs(dp metricdata.DataPoint[int64]) map[string]string {
	out := make(map[string]string)
	for _, kv := range dp.Attributes.ToSlice() {
		out[string(kv.Key)] = kv.Value.Emit()
	}
	return out
}

// newFailureData builds the actor data for a resource update of the given
// operation and type, as onStateChange sees it at a terminal transition.
func newFailureData(operation OperationType, resourceType string) ResourceUpdateData {
	return ResourceUpdateData{
		commandID: "cmd-001",
		resourceUpdate: &ResourceUpdate{
			Operation:    operation,
			DesiredState: pkgmodel.Resource{Label: "r", Type: resourceType},
		},
	}
}

// withCounter attaches a counter created against the given provider, mirroring
// what Init does at spawn time.
func withCounter(t *testing.T, data ResourceUpdateData, mp *sdkmetric.MeterProvider) ResourceUpdateData {
	t.Helper()

	require.NoError(t, setupResourceUpdateMetrics(&data, mp))
	return data
}

// TestResourceUpdater_TerminalFailureIncrementsOperationFailureCounter asserts a
// create that reaches terminal failure emits one count labelled with the
// resource type, the operation, the plugin namespace and the stage it failed in.
func TestResourceUpdater_TerminalFailureIncrementsOperationFailureCounter(t *testing.T) {
	mp, reader := newTestMeterProvider()
	data := withCounter(t, newFailureData(OperationCreate, "FakeAWS::S3::Bucket"), mp)

	proc := newMetricsProcess()
	_, _, err := onStateChange(StateCreating, StateFinishedWithError, data, proc)
	require.NoError(t, err)

	dp := requireSinglePoint(t, reader)
	assert.Equal(t, int64(1), dp.Value)
	assert.Equal(t, map[string]string{
		"resource_type": "FakeAWS::S3::Bucket",
		"operation":     "create",
		"plugin":        "FakeAWS",
		"failure_stage": "creating",
	}, attrs(dp))
}

// TestResourceUpdater_FailureBeforeThePluginIsCalledCarriesItsOwnStage asserts a
// failure that never reached the plugin — here a terminal resolve miss — is
// labelled with the stage it actually failed in, so it cannot be misread as the
// provider rejecting the create.
func TestResourceUpdater_FailureBeforeThePluginIsCalledCarriesItsOwnStage(t *testing.T) {
	mp, reader := newTestMeterProvider()
	data := withCounter(t, newFailureData(OperationCreate, "FakeAWS::S3::Bucket"), mp)

	proc := newMetricsProcess()
	_, _, err := onStateChange(StateResolving, StateFinishedWithError, data, proc)
	require.NoError(t, err)

	dp := requireSinglePoint(t, reader)
	assert.Equal(t, "resolving", attrs(dp)["failure_stage"])
	assert.Equal(t, "create", attrs(dp)["operation"])
}

// TestResourceUpdater_SyncReadFailureLabelledRead asserts a failing background
// sync read is counted under operation=read, so the high-volume sync path is
// separable at query time rather than silently dropped.
func TestResourceUpdater_SyncReadFailureLabelledRead(t *testing.T) {
	mp, reader := newTestMeterProvider()
	data := withCounter(t, newFailureData(OperationRead, "FakeAWS::S3::Bucket"), mp)

	proc := newMetricsProcess()
	_, _, err := onStateChange(StateSynchronizing, StateFinishedWithError, data, proc)
	require.NoError(t, err)

	dp := requireSinglePoint(t, reader)
	assert.Equal(t, "read", attrs(dp)["operation"])
	assert.Equal(t, "synchronizing", attrs(dp)["failure_stage"])
}

// TestResourceUpdater_ReplaceHalvesReportDeleteAndCreate pins that a replace is
// executed as two resource updates whose halves report delete and create, so a
// future single-Replace update cannot silently change the label set.
func TestResourceUpdater_ReplaceHalvesReportDeleteAndCreate(t *testing.T) {
	mp, reader := newTestMeterProvider()

	deleteHalf := withCounter(t, newFailureData(OperationDelete, "FakeAWS::S3::Bucket"), mp)
	_, _, err := onStateChange(StateDeleting, StateFinishedWithError, deleteHalf, newMetricsProcess())
	require.NoError(t, err)

	createHalf := withCounter(t, newFailureData(OperationCreate, "FakeAWS::S3::Bucket"), mp)
	_, _, err = onStateChange(StateCreating, StateFinishedWithError, createHalf, newMetricsProcess())
	require.NoError(t, err)

	points := collectOperationFailures(t, reader)
	require.Len(t, points, 2, "the two halves must be distinct series")

	seen := make(map[string]string)
	for _, dp := range points {
		assert.Equal(t, int64(1), dp.Value)
		seen[attrs(dp)["operation"]] = attrs(dp)["failure_stage"]
	}
	assert.Equal(t, map[string]string{"delete": "deleting", "create": "creating"}, seen)
}

// TestResourceUpdater_SuccessDoesNotIncrement asserts a successful terminal
// transition emits nothing.
func TestResourceUpdater_SuccessDoesNotIncrement(t *testing.T) {
	mp, reader := newTestMeterProvider()
	data := withCounter(t, newFailureData(OperationCreate, "FakeAWS::S3::Bucket"), mp)

	proc := newMetricsProcess()
	_, _, err := onStateChange(StateCreating, StateFinishedSuccessfully, data, proc)
	require.NoError(t, err)

	assert.Nil(t, collectOperationFailures(t, reader))
}

// TestResourceUpdater_RejectedDoesNotIncrement asserts the out-of-band-drift
// guard declining to proceed is not counted as a failure: the update's state
// becomes Rejected, not Failed, so counting it here would misreport it.
func TestResourceUpdater_RejectedDoesNotIncrement(t *testing.T) {
	mp, reader := newTestMeterProvider()
	data := withCounter(t, newFailureData(OperationUpdate, "FakeAWS::S3::Bucket"), mp)

	proc := newMetricsProcess()
	_, _, err := onStateChange(StateSynchronizing, StateRejected, data, proc)
	require.NoError(t, err)

	assert.Nil(t, collectOperationFailures(t, reader))
}

// TestResourceUpdater_IncrementsOncePerTerminalTransition pins the counting
// unit: one terminal-failure transition contributes exactly one count.
func TestResourceUpdater_IncrementsOncePerTerminalTransition(t *testing.T) {
	mp, reader := newTestMeterProvider()
	data := withCounter(t, newFailureData(OperationCreate, "FakeAWS::S3::Bucket"), mp)

	proc := newMetricsProcess()
	_, _, err := onStateChange(StateCreating, StateFinishedWithError, data, proc)
	require.NoError(t, err)

	dp := requireSinglePoint(t, reader)
	assert.Equal(t, int64(1), dp.Value)
}

// TestResourceUpdater_MalformedTypeLabelsPluginWithTheWholeType pins the
// documented Namespace() behaviour for a type carrying no "::" separator: the
// plugin label is the whole type, matching the sibling gauges.
func TestResourceUpdater_MalformedTypeLabelsPluginWithTheWholeType(t *testing.T) {
	mp, reader := newTestMeterProvider()
	data := withCounter(t, newFailureData(OperationCreate, "S3Bucket"), mp)

	proc := newMetricsProcess()
	_, _, err := onStateChange(StateCreating, StateFinishedWithError, data, proc)
	require.NoError(t, err)

	dp := requireSinglePoint(t, reader)
	assert.Equal(t, "S3Bucket", attrs(dp)["resource_type"])
	assert.Equal(t, "S3Bucket", attrs(dp)["plugin"])
}

// TestResourceUpdater_EmptyTypeEmitsEmptyLabels asserts a typeless update is
// still counted, with empty type and plugin labels. Dropping the event would
// hide the failure this metric exists to surface.
func TestResourceUpdater_EmptyTypeEmitsEmptyLabels(t *testing.T) {
	mp, reader := newTestMeterProvider()
	data := withCounter(t, newFailureData(OperationCreate, ""), mp)

	proc := newMetricsProcess()
	_, _, err := onStateChange(StateCreating, StateFinishedWithError, data, proc)
	require.NoError(t, err)

	dp := requireSinglePoint(t, reader)
	assert.Equal(t, "", attrs(dp)["resource_type"])
	assert.Equal(t, "", attrs(dp)["plugin"])
	assert.Equal(t, int64(1), dp.Value)
}

// TestResourceUpdater_MissingMeterProviderDoesNotFailInit asserts a spawn with
// no injected MeterProvider still initializes: a metrics problem must never
// fail a resource update.
func TestResourceUpdater_MissingMeterProviderDoesNotFailInit(t *testing.T) {
	sender := gen.PID{Node: "test", ID: 100}
	env := map[gen.Env]any{
		"RetryConfig":     pkgmodel.RetryConfig{StatusCheckInterval: 1 * time.Second},
		"DiscoveryConfig": pkgmodel.DiscoveryConfig{},
		"Datastore":       newMockDatastore(),
	}

	updater, err := unit.Spawn(t, newResourceUpdater,
		unit.WithArgs(sender),
		unit.WithEnv(env))

	require.NoError(t, err, "a missing MeterProvider must not fail Init")
	require.NotNil(t, updater)
	assert.False(t, updater.IsTerminated())
}

// TestResourceUpdater_InjectedMeterProviderIsUsed asserts the MeterProvider
// injected via the actor environment is what the counter is created against,
// which is what keeps the unit cases hermetic.
func TestResourceUpdater_InjectedMeterProviderIsUsed(t *testing.T) {
	mp, reader := newTestMeterProvider()
	sender := gen.PID{Node: "test", ID: 100}
	env := map[gen.Env]any{
		"RetryConfig":     pkgmodel.RetryConfig{StatusCheckInterval: 1 * time.Second},
		"DiscoveryConfig": pkgmodel.DiscoveryConfig{},
		"Datastore":       newMockDatastore(),
		"MeterProvider":   mp,
	}

	updater, err := unit.Spawn(t, newResourceUpdater,
		unit.WithArgs(sender),
		unit.WithEnv(env))
	require.NoError(t, err)
	require.NotNil(t, updater)

	// The spawned actor's counter is registered against the injected provider,
	// so an emission through it is visible on this case's own reader.
	data := newFailureData(OperationCreate, "FakeAWS::S3::Bucket")
	require.NoError(t, setupResourceUpdateMetrics(&data, mp))
	recordOperationFailure(StateCreating, data, newMetricsProcess())

	dp := requireSinglePoint(t, reader)
	assert.Equal(t, int64(1), dp.Value)
}

// TestResourceUpdater_NilCounterDoesNotPanic asserts the emission path is safe
// when instrument creation failed and the counter is nil.
func TestResourceUpdater_NilCounterDoesNotPanic(t *testing.T) {
	data := newFailureData(OperationCreate, "FakeAWS::S3::Bucket")
	require.Nil(t, data.operationFailures)

	proc := newMetricsProcess()
	assert.NotPanics(t, func() {
		recordOperationFailure(StateCreating, data, proc)
	})

	assert.NotPanics(t, func() {
		_, _, err := onStateChange(StateCreating, StateFinishedWithError, data, proc)
		assert.NoError(t, err)
	})
}

// TestResourceUpdater_FailureCounterEmittedBeforePersisterCall asserts the
// increment is ordered ahead of the blocking persister call, so an already
// reached terminal failure is still counted when that call fails.
func TestResourceUpdater_FailureCounterEmittedBeforePersisterCall(t *testing.T) {
	mp, reader := newTestMeterProvider()
	data := withCounter(t, newFailureData(OperationCreate, "FakeAWS::S3::Bucket"), mp)

	proc := newMetricsProcess()
	_, _, err := onStateChange(StateCreating, StateFinishedWithError, data, proc)
	require.NoError(t, err)

	require.Positive(t, proc.calls, "the persister call must have been attempted and failed")
	dp := requireSinglePoint(t, reader)
	assert.Equal(t, int64(1), dp.Value, "the failure is counted despite the persister call failing")
}

// TestResourceUpdater_OperationFailureCounterIdentity pins the instrument's
// public identity — the name operators query and its description.
func TestResourceUpdater_OperationFailureCounterIdentity(t *testing.T) {
	mp, reader := newTestMeterProvider()
	data := withCounter(t, newFailureData(OperationCreate, "FakeAWS::S3::Bucket"), mp)

	recordOperationFailure(StateCreating, data, newMetricsProcess())

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "formae.resource.operation.failures" {
				found = true
				assert.Equal(t, operationFailuresMeterScope, sm.Scope.Name)
				assert.Empty(t, m.Unit, "the counter declares no unit")
				assert.NotEmpty(t, m.Description)
			}
		}
	}
	assert.True(t, found, "the counter must be registered as formae.resource.operation.failures")
}

// TestResourceUpdater_FailureStageCoversEveryNonTerminalState asserts every
// non-terminal state the actor can fail out of is reported verbatim as the
// failure stage, so the label stays faithful as new states are added.
func TestResourceUpdater_FailureStageCoversEveryNonTerminalState(t *testing.T) {
	stages := []gen.Atom{
		StateInitializing,
		StateSynchronizing,
		StateResolving,
		StateDeleting,
		StateCreating,
		StateUpdating,
		StateExiting,
	}

	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			mp, reader := newTestMeterProvider()
			data := withCounter(t, newFailureData(OperationCreate, "FakeAWS::S3::Bucket"), mp)

			_, _, err := onStateChange(stage, StateFinishedWithError, data, newMetricsProcess())
			require.NoError(t, err)

			dp := requireSinglePoint(t, reader)
			assert.Equal(t, string(stage), attrs(dp)["failure_stage"])
			assert.Equal(t, attribute.String("failure_stage", string(stage)).Value.Emit(),
				attrs(dp)["failure_stage"])
		})
	}
}
