// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"context"
	"fmt"

	"ergo.services/ergo/gen"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

const (
	// operationFailuresMeterScope is the meter scope the resource-update
	// instruments are registered on.
	operationFailuresMeterScope = "formae/resource_update"

	// operationFailuresMetricName is the counter operators query. The
	// Prometheus exporter renders it as
	// formae_resource_operation_failures_total.
	operationFailuresMetricName = "formae.resource.operation.failures"
)

// setupResourceUpdateMetrics initializes OTel metrics for a resource update.
// The MeterProvider is passed in rather than read from the global so each
// caller — production and tests alike — controls which pipeline the
// instruments land on.
func setupResourceUpdateMetrics(data *ResourceUpdateData, mp otelmetric.MeterProvider) error {
	meter := mp.Meter(operationFailuresMeterScope)

	var err error
	data.operationFailures, err = meter.Int64Counter(
		operationFailuresMetricName,
		otelmetric.WithDescription("Resource-update operations that reached a terminal failure, by resource type, operation and failure stage"),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource operation failures counter: %w", err)
	}

	return nil
}

// recordOperationFailure counts one resource update reaching terminal failure.
//
// The failure stage distinguishes a provider rejecting the operation
// (creating, updating, deleting) from a failure that never reached the plugin
// at all (resolving, synchronizing). It is the deepest stage the update
// recorded, not the state machine's oldState: a resource update runs its whole
// chain inside a single message handler, so the intermediate states are never
// committed and oldState would report 'initializing' for every synchronous
// failure — including a provider-rejected create. oldState is the fallback for
// a terminal failure raised before any stage ran.
//
// The counter is nil when instrument creation failed; a metrics problem must
// never fail a resource update, so the emission is skipped instead.
func recordOperationFailure(oldState gen.Atom, data ResourceUpdateData, proc gen.Process) {
	if data.operationFailures == nil || data.resourceUpdate == nil {
		return
	}

	failureStage := data.stage
	if failureStage == "" {
		failureStage = oldState
	}

	resourceType := data.resourceUpdate.DesiredState.Type

	// A type carrying no "::" separator yields the whole string as its
	// namespace, and an empty type an empty one. Both are emitted rather than
	// dropped: the event is the failure this counter exists to surface.
	plugin := data.resourceUpdate.DesiredState.Namespace()

	proc.Log().Debug("ResourceUpdater: counting terminal failure type=%s operation=%s stage=%s commandID=%s",
		resourceType, data.resourceUpdate.Operation, failureStage, data.commandID)

	data.operationFailures.Add(context.Background(), 1, otelmetric.WithAttributes(
		attribute.String("resource_type", resourceType),
		attribute.String("operation", string(data.resourceUpdate.Operation)),
		attribute.String("plugin", plugin),
		attribute.String("failure_stage", string(failureStage)),
	))
}
