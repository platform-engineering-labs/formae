// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logging

import (
	"context"
	"log/slog"

	"github.com/platform-engineering-labs/formae"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.22.0"
)

func setupOTelHandler(serviceName string, otlpEndpoint string, protocol string, insecure bool) slog.Handler {
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceInstanceIDKey.String("formae"),
			semconv.ServiceVersionKey.String(formae.Version),
		),
	)

	if err != nil {
		slog.Error("could not set up OTel resource", "error", err)
		return nil
	}

	var loggerProvider *otellog.LoggerProvider
	switch protocol {
	case "grpc":
		exporterOptions := []otlploggrpc.Option{
			otlploggrpc.WithEndpoint(otlpEndpoint),
		}
		if insecure {
			exporterOptions = append(exporterOptions, otlploggrpc.WithInsecure())
		}
		otlpExporter, err := otlploggrpc.New(context.Background(), exporterOptions...)
		if err != nil {
			slog.Error("could not set up OTLP gRPC exporter", "error", err)
			return nil
		}
		loggerProvider = otellog.NewLoggerProvider(
			otellog.WithResource(res),
			otellog.WithProcessor(
				otellog.NewBatchProcessor(otlpExporter),
			),
		)
	case "http":
		exporterOptions := []otlploghttp.Option{
			otlploghttp.WithEndpoint(otlpEndpoint),
		}
		if insecure {
			exporterOptions = append(exporterOptions, otlploghttp.WithInsecure())
		}
		otlpExporter, err := otlploghttp.New(context.Background(), exporterOptions...)
		if err != nil {
			slog.Error("could not set up OTLP HTTP exporter", "error", err)
			return nil
		}
		loggerProvider = otellog.NewLoggerProvider(
			otellog.WithResource(res),
			otellog.WithProcessor(
				otellog.NewBatchProcessor(otlpExporter),
			),
		)
	default:
		slog.Error("unknown OTLP protocol", "protocol", protocol)
		return nil
	}

	return otelslog.NewHandler(serviceName, otelslog.WithLoggerProvider(loggerProvider))
}
