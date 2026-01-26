// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

type mockMetrics struct {
	counterCalls       int
	upDownCounterCalls int
	gaugeCalls         int
	histogramCalls     int
}

func (m *mockMetrics) Counter(name string, value int64, attrs ...attribute.KeyValue) {
	m.counterCalls++
}

func (m *mockMetrics) UpDownCounter(name string, value int64, attrs ...attribute.KeyValue) {
	m.upDownCounterCalls++
}

func (m *mockMetrics) Gauge(name string, value float64, attrs ...attribute.KeyValue) {
	m.gaugeCalls++
}

func (m *mockMetrics) Histogram(name string, value float64, attrs ...attribute.KeyValue) {
	m.histogramCalls++
}

func TestMetricsFromContext_WithMetrics(t *testing.T) {
	metrics := &mockMetrics{}
	ctx := WithMetrics(context.Background(), metrics)

	got := MetricsFromContext(ctx)
	if got != metrics {
		t.Error("expected same metrics instance")
	}
}

func TestMetricsFromContext_WithoutMetrics(t *testing.T) {
	ctx := context.Background()
	got := MetricsFromContext(ctx)
	if _, ok := got.(noopMetrics); !ok {
		t.Errorf("expected noopMetrics, got %T", got)
	}
}

func TestNoopMetrics_DoesNotPanic(t *testing.T) {
	metrics := noopMetrics{}
	metrics.Counter("test.counter", 1)
	metrics.Counter("test.counter", 5, attribute.String("key", "value"))
	metrics.UpDownCounter("test.updown", 1)
	metrics.UpDownCounter("test.updown", -1, attribute.String("key", "value"))
	metrics.Gauge("test.gauge", 42.0)
	metrics.Gauge("test.gauge", 100.0, attribute.String("key", "value"))
	metrics.Histogram("test.histogram", 1.5)
	metrics.Histogram("test.histogram", 2.5, attribute.String("key", "value"))
}
