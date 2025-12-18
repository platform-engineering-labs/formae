// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	otellog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	otelresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.22.0"

	"github.com/platform-engineering-labs/formae"
	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	promcli "github.com/prometheus/client_golang/prometheus"
)

type OTel struct {
	otelConfig *pkgmodel.OTelConfig
}

func (s *Server) isOTelEnabled() bool {
	return s.otel != nil && s.otel.otelConfig != nil && s.otel.otelConfig.Enabled
}

// newOTelResource creates a standard OTel resource with service metadata
func newOTelResource(serviceName string) (*otelresource.Resource, error) {
	return otelresource.New(context.Background(),
		otelresource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceInstanceIDKey.String("formae"),
			semconv.ServiceVersionKey.String(formae.Version),
		),
	)
}

// SetupOTelProviders initializes all OTel providers (traces, metrics, logs)
// Must be called BEFORE db connections are created, as otelsql requires the
// TracerProvider at driver registration time.
// Returns:
//   - slog.Handler for logging integration (can be nil if disabled)
//   - shutdown function that must be called on app exit
func SetupOTelProviders(otelConfig *pkgmodel.OTelConfig) (slog.Handler, func()) {
	if otelConfig == nil || !otelConfig.Enabled {
		return nil, func() {}
	}

	tracerProvider := setupTracerProvider(otelConfig)
	if tracerProvider != nil {
		otel.SetTracerProvider(tracerProvider)
	}

	meterProvider := setupMeterProvider(otelConfig)
	if meterProvider != nil {
		otel.SetMeterProvider(meterProvider)
	}

	loggerProvider, logHandler := setupLoggerProvider(otelConfig)

	return logHandler, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if loggerProvider != nil {
			if err := loggerProvider.Shutdown(ctx); err != nil {
				slog.Error("failed to shut down LoggerProvider", "error", err)
			}
		}
		if meterProvider != nil {
			if err := meterProvider.Shutdown(ctx); err != nil {
				slog.Error("failed to shut down MeterProvider", "error", err)
			}
		}
		if tracerProvider != nil {
			if err := tracerProvider.Shutdown(ctx); err != nil {
				slog.Error("failed to shut down TracerProvider", "error", err)
			}
		}
	}
}

func setupTracerProvider(otelConfig *pkgmodel.OTelConfig) *sdktrace.TracerProvider {
	otlpConfig := otelConfig.OTLP

	res, err := newOTelResource(otelConfig.ServiceName)
	if err != nil {
		slog.Error("failed to create resource for OTel tracing", "error", err)
		return nil
	}

	var exporter sdktrace.SpanExporter
	switch otlpConfig.Protocol {
	case "grpc":
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(otlpConfig.Endpoint),
		}
		if otlpConfig.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		exporter, err = otlptracegrpc.New(context.Background(), opts...)
	case "http":
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(otlpConfig.Endpoint),
		}
		if otlpConfig.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		exporter, err = otlptracehttp.New(context.Background(), opts...)
	default:
		slog.Error("unknown OTLP protocol for tracing", "protocol", otlpConfig.Protocol)
		return nil
	}

	if err != nil {
		slog.Error("failed to create OTLP trace exporter", "error", err)
		return nil
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	slog.Info("OTel tracing enabled", "endpoint", otlpConfig.Endpoint, "protocol", otlpConfig.Protocol)
	return tracerProvider
}

func setupMeterProvider(otelConfig *pkgmodel.OTelConfig) *metric.MeterProvider {
	exporter, err := prometheus.New()
	if err != nil {
		slog.Error("failed to create Prometheus exporter", "error", err)
		return nil
	}

	res, err := newOTelResource(otelConfig.ServiceName)
	if err != nil {
		slog.Error("failed to create resource for OTel metrics", "error", err)
		return nil
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithReader(exporter),
		metric.WithResource(res),
	)

	slog.Info("OTel metrics enabled")
	return meterProvider
}

func setupLoggerProvider(otelConfig *pkgmodel.OTelConfig) (*otellog.LoggerProvider, slog.Handler) {
	otlpConfig := otelConfig.OTLP

	res, err := newOTelResource(otelConfig.ServiceName)
	if err != nil {
		slog.Error("could not set up OTel resource for logging", "error", err)
		return nil, nil
	}

	var exporter otellog.Exporter
	switch otlpConfig.Protocol {
	case "grpc":
		opts := []otlploggrpc.Option{
			otlploggrpc.WithEndpoint(otlpConfig.Endpoint),
		}
		if otlpConfig.Insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
		exporter, err = otlploggrpc.New(context.Background(), opts...)
	case "http":
		opts := []otlploghttp.Option{
			otlploghttp.WithEndpoint(otlpConfig.Endpoint),
		}
		if otlpConfig.Insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
		exporter, err = otlploghttp.New(context.Background(), opts...)
	default:
		slog.Error("unknown OTLP protocol for logging", "protocol", otlpConfig.Protocol)
		return nil, nil
	}

	if err != nil {
		slog.Error("could not set up OTLP exporter for logging", "error", err, "protocol", otlpConfig.Protocol)
		return nil, nil
	}

	loggerProvider := otellog.NewLoggerProvider(
		otellog.WithResource(res),
		otellog.WithProcessor(
			otellog.NewBatchProcessor(exporter),
		),
	)

	handler := otelslog.NewHandler(otelConfig.ServiceName, otelslog.WithLoggerProvider(loggerProvider))
	slog.Info("OTel logging enabled", "endpoint", otlpConfig.Endpoint, "protocol", otlpConfig.Protocol)
	return loggerProvider, handler
}

// RegisterPrometheusStatsCollector registers a Prometheus collector for formae stats
// This should be called once during server initialization
func RegisterPrometheusStatsCollector(metastructure interface {
	Stats() (*apimodel.Stats, error)
}) {
	promcli.MustRegister(promcli.CollectorFunc(func(ch chan<- promcli.Metric) {
		stats, err := metastructure.Stats()
		if err != nil {
			slog.Error("failed to get stats from metastructure", "error", err)
			return
		}

		for _, metric := range statsToPrometheusMetrics(stats) {
			ch <- metric
		}
	}))
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
