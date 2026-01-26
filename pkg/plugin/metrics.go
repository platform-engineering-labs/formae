// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
)

// MetricRegistry allows plugins to record custom metrics.
// All metrics are automatically tagged with plugin.namespace.
type MetricRegistry interface {
	// Counter records a monotonically increasing value (e.g., total requests, errors).
	Counter(name string, value int64, attrs ...attribute.KeyValue)
	// UpDownCounter records a value that can increase or decrease (e.g., in-flight operations).
	UpDownCounter(name string, value int64, attrs ...attribute.KeyValue)
	// Gauge records a point-in-time value (e.g., current connections, queue size).
	Gauge(name string, value float64, attrs ...attribute.KeyValue)
	// Histogram records a distribution of values (e.g., latency, file sizes).
	Histogram(name string, value float64, attrs ...attribute.KeyValue)
}

type metricsKey struct{}

// MetricsFromContext extracts the MetricRegistry from context.
// Returns a no-op registry if none is present.
func MetricsFromContext(ctx context.Context) MetricRegistry {
	if m, ok := ctx.Value(metricsKey{}).(MetricRegistry); ok {
		return m
	}
	return noopMetrics{}
}

// WithMetrics returns a new context with the given MetricRegistry.
func WithMetrics(ctx context.Context, m MetricRegistry) context.Context {
	return context.WithValue(ctx, metricsKey{}, m)
}

// noopMetrics does nothing - used when no registry in context.
type noopMetrics struct{}

func (noopMetrics) Counter(name string, value int64, attrs ...attribute.KeyValue)       {}
func (noopMetrics) UpDownCounter(name string, value int64, attrs ...attribute.KeyValue) {}
func (noopMetrics) Gauge(name string, value float64, attrs ...attribute.KeyValue)       {}
func (noopMetrics) Histogram(name string, value float64, attrs ...attribute.KeyValue)   {}
