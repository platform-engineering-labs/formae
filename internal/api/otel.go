// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/labstack/echo/v4"
	"github.com/platform-engineering-labs/formae"
	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	promcli "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	otelresource "go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.22.0"
)

type OTel struct {
	otelConfig    *pkgmodel.OTelConfig
	meterProvider *metric.MeterProvider
}

func (s *Server) isOTelEnabled() bool {
	return s.otel != nil && s.otel.otelConfig != nil && s.otel.otelConfig.Enabled
}

func (s *Server) setupOTelMetrics() {
	exporter, err := prometheus.New()
	if err != nil {
		slog.Error("failed to create Prometheus exporter", "error", err)
		return
	}

	res, err := otelresource.New(context.Background(),
		otelresource.WithAttributes(
			semconv.ServiceNameKey.String("formae-agent"),
			semconv.ServiceInstanceIDKey.String("formae"),
			semconv.ServiceVersionKey.String(formae.Version),
		),
	)

	if err != nil {
		slog.Error("failed to create resource for OTel", "error", err)
		return
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithReader(exporter),
		metric.WithResource(res),
	)

	promcli.MustRegister(promcli.CollectorFunc(func(ch chan<- promcli.Metric) {
		stats, err := s.metastructure.Stats()
		if err != nil {
			slog.Error("failed to get stats from metastructure", "error", err)
			return
		}

		for _, metric := range statsToPrometheusMetrics(stats) {
			ch <- metric
		}
	}))

	s.otel.meterProvider = meterProvider
}

func (s *Server) shutdownOTel() {
	if s.isOTelEnabled() && s.otel.meterProvider != nil {
		if err := s.otel.meterProvider.Shutdown(context.Background()); err != nil {
			slog.Error("failed to shut down MeterProvider", "error", err)
		}
	}
}

func setupOTelMetricsHandler() echo.HandlerFunc {
	return echo.WrapHandler(promhttp.Handler())
}

func statsToPrometheusMetrics(stats *apimodel.Stats) []promcli.Metric {
	var metrics []promcli.Metric

	processNumber := func(fieldValue reflect.Value, fieldName string, labels map[string]string) float64 {
		numericValue := fieldValue.Convert(reflect.TypeOf(float64(0))).Float()

		desc := promcli.NewDesc(
			"formae_stats_"+fieldName,
			"formae stats",
			nil, labels,
		)
		metric, err := promcli.NewConstMetric(desc, promcli.GaugeValue, numericValue)
		if err != nil {
			slog.Error("failed to create Prometheus metric", "fieldName", fieldName, "error", err)
			return 0
		}

		metrics = append(metrics, metric)
		return numericValue
	}

	var processFields func(statsValue reflect.Value, prefix string, labels map[string]string)
	processFields = func(statsValue reflect.Value, prefix string, labels map[string]string) {
		statsType := statsValue.Type()
		for i := 0; i < statsType.NumField(); i++ {
			field := statsType.Field(i)
			fieldValue := statsValue.Field(i)
			fieldName := prefix + field.Name
			switch fieldValue.Kind() {
			case reflect.Struct:
				processFields(fieldValue, fieldName+"_", labels)

			case reflect.Ptr:
				if !fieldValue.IsNil() && fieldValue.Elem().Kind() == reflect.Struct {
					processFields(fieldValue.Elem(), fieldName+"_", labels)
				}

			case reflect.Slice, reflect.Array:
				for j := 0; j < fieldValue.Len(); j++ {
					element := fieldValue.Index(j)
					elementName := fieldName + fmt.Sprintf("[%d]", j)
					switch element.Kind() {
					case reflect.String:
						labels[elementName] = element.String()

					case reflect.Int, reflect.Int64, reflect.Float64, reflect.Float32:
						processNumber(element, elementName, labels)

					default:
						slog.Debug("skipping unsupported slice/array element type", "elementName", elementName, "elementType", element.Kind())
					}
				}

			case reflect.Map:
				for _, key := range fieldValue.MapKeys() {
					value := fieldValue.MapIndex(key)
					keyStr := fmt.Sprintf("%v", key.Interface())
					valueName := fieldName + "[" + keyStr + "]"
					switch value.Kind() {
					case reflect.String:
						labels[valueName] = value.String()

					case reflect.Int, reflect.Int64, reflect.Float64, reflect.Float32:
						processNumber(value, valueName, labels)

					default:
						slog.Warn("skipping unsupported map value type", "valueName", valueName, "valueType", value.Kind())
					}
				}
			case reflect.String:
				labels[fieldName] = fieldValue.String()

			case reflect.Int, reflect.Int64, reflect.Float64, reflect.Float32:
				processNumber(fieldValue, fieldName, labels)

			default:
				slog.Warn("skipping unsupported field type", "fieldName", fieldName, "fieldType", fieldValue.Kind())
			}
		}
	}

	statsValue := reflect.ValueOf(stats)
	if statsValue.Kind() == reflect.Ptr {
		statsValue = statsValue.Elem()
	}

	processFields(statsValue, "", map[string]string{})

	return metrics
}
