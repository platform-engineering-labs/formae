// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestDatastoreMetrics_RecordDuration(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background())

	// Override the package-level meter for testing
	origMeter := meter
	meter = provider.Meter("formae/datastore")
	defer func() { meter = origMeter }()

	m, err := newDatastoreMetrics()
	require.NoError(t, err)

	start := time.Now().Add(-50 * time.Millisecond)
	m.recordDuration(context.Background(), "store_resource", start)

	var rm metricdata.ResourceMetrics
	err = reader.Collect(context.Background(), &rm)
	require.NoError(t, err)

	require.Len(t, rm.ScopeMetrics, 1)
	require.Len(t, rm.ScopeMetrics[0].Metrics, 1)

	metric := rm.ScopeMetrics[0].Metrics[0]
	assert.Equal(t, "formae.datastore.query.duration_ms", metric.Name)

	histogram := metric.Data.(metricdata.Histogram[float64])
	require.Len(t, histogram.DataPoints, 1)
	assert.Greater(t, histogram.DataPoints[0].Sum, float64(0))
}
