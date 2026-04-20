// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package postgres

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var meter = otel.Meter("formae/datastore")

type datastoreMetrics struct {
	queryDuration metric.Float64Histogram
}

func newDatastoreMetrics() (*datastoreMetrics, error) {
	queryDuration, err := meter.Float64Histogram(
		"formae.datastore.query.duration_ms",
		metric.WithDescription("Duration of datastore queries in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}
	return &datastoreMetrics{queryDuration: queryDuration}, nil
}

func (m *datastoreMetrics) recordDuration(ctx context.Context, queryType string, start time.Time) {
	if m == nil {
		return
	}
	duration := float64(time.Since(start).Milliseconds())
	m.queryDuration.Record(ctx, duration,
		metric.WithAttributes(attribute.String("query_type", queryType)),
	)
}
