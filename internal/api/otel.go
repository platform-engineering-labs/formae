// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shirou/gopsutil/v4/disk"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/host"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	otellog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	otelresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.22.0"

	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

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
//   - http.Handler for Prometheus /metrics endpoint (can be nil if disabled)
//   - shutdown function that must be called on app exit
func SetupOTelProviders(otelConfig *pkgmodel.OTelConfig) (slog.Handler, http.Handler, func()) {
	if otelConfig == nil || !otelConfig.Enabled {
		return nil, nil, func() {}
	}

	tracerProvider := setupTracerProvider(otelConfig)
	if tracerProvider != nil {
		otel.SetTracerProvider(tracerProvider)
	}

	meterProvider, metricsHandler := setupMeterProvider(otelConfig)
	if meterProvider != nil {
		otel.SetMeterProvider(meterProvider)
	}

	loggerProvider, logHandler := setupLoggerProvider(otelConfig)

	slog.Info("OpenTelemetry enabled", "endpoint", otelConfig.OTLP.Endpoint)

	return logHandler, metricsHandler, func() {
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

	// Skip OTLP exporter if not enabled
	if !otlpConfig.Enabled {
		return nil
	}

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

	return tracerProvider
}

func setupMeterProvider(otelConfig *pkgmodel.OTelConfig) (*metric.MeterProvider, http.Handler) {
	otlpConfig := otelConfig.OTLP

	res, err := newOTelResource(otelConfig.ServiceName)
	if err != nil {
		slog.Error("failed to create resource for OTel metrics", "error", err)
		return nil, nil
	}

	// Collect metric readers
	var readers []metric.Option

	// Create OTLP exporter for pushing metrics (if enabled)
	// Default is delta temporality (OTel-native).
	// For Prometheus/Mimir backends without deltatocumulative processor, set temporality to "cumulative"
	if otlpConfig.Enabled {
		var otlpExporter metric.Exporter
		switch otlpConfig.Protocol {
		case "grpc":
			opts := []otlpmetricgrpc.Option{
				otlpmetricgrpc.WithEndpoint(otlpConfig.Endpoint),
			}
			if otlpConfig.Insecure {
				opts = append(opts, otlpmetricgrpc.WithInsecure())
			}
			if otlpConfig.Temporality == "cumulative" {
				opts = append(opts, otlpmetricgrpc.WithTemporalitySelector(metric.CumulativeTemporalitySelector))
			}
			otlpExporter, err = otlpmetricgrpc.New(context.Background(), opts...)
		case "http":
			opts := []otlpmetrichttp.Option{
				otlpmetrichttp.WithEndpoint(otlpConfig.Endpoint),
			}
			if otlpConfig.Insecure {
				opts = append(opts, otlpmetrichttp.WithInsecure())
			}
			if otlpConfig.Temporality == "cumulative" {
				opts = append(opts, otlpmetrichttp.WithTemporalitySelector(metric.CumulativeTemporalitySelector))
			}
			otlpExporter, err = otlpmetrichttp.New(context.Background(), opts...)
		default:
			slog.Error("unknown OTLP protocol for metrics, continuing with Prometheus if enabled", "protocol", otlpConfig.Protocol)
			// Don't return - allow Prometheus to be set up if enabled
		}

		if err != nil {
			slog.Error("failed to create OTLP metric exporter, continuing with Prometheus if enabled", "error", err)
			// Don't return - allow Prometheus to be set up if enabled
		} else if otlpExporter != nil {
			// Only add OTLP reader if exporter was successfully created
			readers = append(readers, metric.WithReader(metric.NewPeriodicReader(otlpExporter, metric.WithInterval(10*time.Second))))
		}
	}

	// Create Prometheus exporter for /metrics scraping (if enabled)
	var metricsHandler http.Handler
	if otelConfig.Prometheus.Enabled {
		// Create a new prometheus registry for OTel metrics
		promRegistry := promclient.NewRegistry()
		promExporter, err := prometheus.New(prometheus.WithRegisterer(promRegistry))
		if err != nil {
			slog.Error("failed to create Prometheus exporter", "error", err)
		} else {
			readers = append(readers, metric.WithReader(promExporter))
			metricsHandler = promhttp.HandlerFor(promRegistry, promhttp.HandlerOpts{})
			slog.Info("Prometheus /metrics endpoint enabled")
		}
	}

	// If no readers are configured (both OTLP and Prometheus disabled), return nil
	if len(readers) == 0 {
		return nil, nil
	}

	// Build meter provider with all readers
	opts := append(readers, metric.WithResource(res))
	meterProvider := metric.NewMeterProvider(opts...)

	// Start Go runtime metrics collection (goroutines, memory, GC, etc.)
	// Must pass meterProvider explicitly since global provider isn't set yet
	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second), runtime.WithMeterProvider(meterProvider)); err != nil {
		slog.Error("failed to start Go runtime metrics", "error", err)
	}

	// Start host/process metrics collection (CPU, memory, network I/O)
	// Must pass meterProvider explicitly since global provider isn't set yet
	if err := host.Start(host.WithMeterProvider(meterProvider)); err != nil {
		slog.Error("failed to start host metrics", "error", err)
	}

	// Start disk I/O metrics collection (not included in standard host package)
	if err := startDiskMetrics(meterProvider); err != nil {
		slog.Error("failed to start disk I/O metrics", "error", err)
	}

	return meterProvider, metricsHandler
}

// startDiskMetrics registers disk I/O metrics using gopsutil
func startDiskMetrics(provider *metric.MeterProvider) error {
	meter := provider.Meter("formae/disk")

	// Disk read bytes counter
	diskReadBytes, err := meter.Int64ObservableCounter(
		"system.disk.io.read_bytes",
		otelmetric.WithDescription("Total bytes read from disk"),
		otelmetric.WithUnit("By"),
	)
	if err != nil {
		return fmt.Errorf("failed to create disk read bytes counter: %w", err)
	}

	// Disk write bytes counter
	diskWriteBytes, err := meter.Int64ObservableCounter(
		"system.disk.io.write_bytes",
		otelmetric.WithDescription("Total bytes written to disk"),
		otelmetric.WithUnit("By"),
	)
	if err != nil {
		return fmt.Errorf("failed to create disk write bytes counter: %w", err)
	}

	// Disk read operations counter
	diskReadOps, err := meter.Int64ObservableCounter(
		"system.disk.io.read_ops",
		otelmetric.WithDescription("Total disk read operations"),
		otelmetric.WithUnit("{operation}"),
	)
	if err != nil {
		return fmt.Errorf("failed to create disk read ops counter: %w", err)
	}

	// Disk write operations counter
	diskWriteOps, err := meter.Int64ObservableCounter(
		"system.disk.io.write_ops",
		otelmetric.WithDescription("Total disk write operations"),
		otelmetric.WithUnit("{operation}"),
	)
	if err != nil {
		return fmt.Errorf("failed to create disk write ops counter: %w", err)
	}

	// Register callback for all disk metrics
	_, err = meter.RegisterCallback(
		func(ctx context.Context, observer otelmetric.Observer) error {
			counters, err := disk.IOCounters()
			if err != nil {
				slog.Debug("failed to get disk I/O counters", "error", err)
				return nil // Don't fail the callback
			}

			for device, counter := range counters {
				attrs := attribute.String("device", device)
				observer.ObserveInt64(diskReadBytes, int64(counter.ReadBytes), otelmetric.WithAttributes(attrs))
				observer.ObserveInt64(diskWriteBytes, int64(counter.WriteBytes), otelmetric.WithAttributes(attrs))
				observer.ObserveInt64(diskReadOps, int64(counter.ReadCount), otelmetric.WithAttributes(attrs))
				observer.ObserveInt64(diskWriteOps, int64(counter.WriteCount), otelmetric.WithAttributes(attrs))
			}
			return nil
		},
		diskReadBytes, diskWriteBytes, diskReadOps, diskWriteOps,
	)
	if err != nil {
		return fmt.Errorf("failed to register disk metrics callback: %w", err)
	}

	return nil
}

func setupLoggerProvider(otelConfig *pkgmodel.OTelConfig) (*otellog.LoggerProvider, slog.Handler) {
	otlpConfig := otelConfig.OTLP

	// Skip OTLP exporter if not enabled
	if !otlpConfig.Enabled {
		return nil, nil
	}

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
	return loggerProvider, handler
}

// StartErgoMetrics registers Ergo actor framework metrics with OTel.
// This should be called after the Ergo node is started.
func StartErgoMetrics(node gen.Node) error {
	meter := otel.Meter("formae/ergo")

	// Node uptime
	uptimeSeconds, err := meter.Int64ObservableGauge(
		"ergo.node.uptime_seconds",
		otelmetric.WithDescription("Node uptime in seconds"),
		otelmetric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("failed to create uptime gauge: %w", err)
	}

	// Process metrics
	processesTotal, err := meter.Int64ObservableGauge(
		"ergo.processes.total",
		otelmetric.WithDescription("Total number of processes"),
	)
	if err != nil {
		return fmt.Errorf("failed to create processes total gauge: %w", err)
	}

	processesRunning, err := meter.Int64ObservableGauge(
		"ergo.processes.running",
		otelmetric.WithDescription("Number of running processes"),
	)
	if err != nil {
		return fmt.Errorf("failed to create processes running gauge: %w", err)
	}

	processesZombie, err := meter.Int64ObservableGauge(
		"ergo.processes.zombie",
		otelmetric.WithDescription("Number of zombie processes"),
	)
	if err != nil {
		return fmt.Errorf("failed to create processes zombie gauge: %w", err)
	}

	// Memory metrics
	memoryUsed, err := meter.Int64ObservableGauge(
		"ergo.memory.used_bytes",
		otelmetric.WithDescription("Memory used by the node"),
		otelmetric.WithUnit("By"),
	)
	if err != nil {
		return fmt.Errorf("failed to create memory used gauge: %w", err)
	}

	memoryAlloc, err := meter.Int64ObservableGauge(
		"ergo.memory.alloc_bytes",
		otelmetric.WithDescription("Memory allocated by the node"),
		otelmetric.WithUnit("By"),
	)
	if err != nil {
		return fmt.Errorf("failed to create memory alloc gauge: %w", err)
	}

	// CPU metrics
	cpuUser, err := meter.Int64ObservableCounter(
		"ergo.cpu.user_seconds",
		otelmetric.WithDescription("User CPU time in seconds"),
		otelmetric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("failed to create cpu user counter: %w", err)
	}

	cpuSystem, err := meter.Int64ObservableCounter(
		"ergo.cpu.system_seconds",
		otelmetric.WithDescription("System CPU time in seconds"),
		otelmetric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("failed to create cpu system counter: %w", err)
	}

	// Application metrics
	applicationsTotal, err := meter.Int64ObservableGauge(
		"ergo.applications.total",
		otelmetric.WithDescription("Total number of applications"),
	)
	if err != nil {
		return fmt.Errorf("failed to create applications total gauge: %w", err)
	}

	applicationsRunning, err := meter.Int64ObservableGauge(
		"ergo.applications.running",
		otelmetric.WithDescription("Number of running applications"),
	)
	if err != nil {
		return fmt.Errorf("failed to create applications running gauge: %w", err)
	}

	// Registry metrics
	registeredNames, err := meter.Int64ObservableGauge(
		"ergo.registered.names",
		otelmetric.WithDescription("Number of registered process names"),
	)
	if err != nil {
		return fmt.Errorf("failed to create registered names gauge: %w", err)
	}

	registeredAliases, err := meter.Int64ObservableGauge(
		"ergo.registered.aliases",
		otelmetric.WithDescription("Number of registered aliases"),
	)
	if err != nil {
		return fmt.Errorf("failed to create registered aliases gauge: %w", err)
	}

	registeredEvents, err := meter.Int64ObservableGauge(
		"ergo.registered.events",
		otelmetric.WithDescription("Number of registered events"),
	)
	if err != nil {
		return fmt.Errorf("failed to create registered events gauge: %w", err)
	}

	// Network metrics
	connectedNodes, err := meter.Int64ObservableGauge(
		"ergo.connected.nodes_total",
		otelmetric.WithDescription("Total connected nodes"),
	)
	if err != nil {
		return fmt.Errorf("failed to create connected nodes gauge: %w", err)
	}

	remoteNodeUptime, err := meter.Int64ObservableGauge(
		"ergo.remote.node_uptime_seconds",
		otelmetric.WithDescription("Remote node uptime"),
		otelmetric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("failed to create remote node uptime gauge: %w", err)
	}

	remoteMessagesIn, err := meter.Int64ObservableCounter(
		"ergo.remote.messages_in",
		otelmetric.WithDescription("Messages received from node"),
	)
	if err != nil {
		return fmt.Errorf("failed to create remote messages in counter: %w", err)
	}

	remoteMessagesOut, err := meter.Int64ObservableCounter(
		"ergo.remote.messages_out",
		otelmetric.WithDescription("Messages sent to node"),
	)
	if err != nil {
		return fmt.Errorf("failed to create remote messages out counter: %w", err)
	}

	remoteBytesIn, err := meter.Int64ObservableCounter(
		"ergo.remote.bytes_in",
		otelmetric.WithDescription("Bytes received from node"),
		otelmetric.WithUnit("By"),
	)
	if err != nil {
		return fmt.Errorf("failed to create remote bytes in counter: %w", err)
	}

	remoteBytesOut, err := meter.Int64ObservableCounter(
		"ergo.remote.bytes_out",
		otelmetric.WithDescription("Bytes sent to node"),
		otelmetric.WithUnit("By"),
	)
	if err != nil {
		return fmt.Errorf("failed to create remote bytes out counter: %w", err)
	}

	// Register callback to collect all metrics
	_, err = meter.RegisterCallback(
		func(ctx context.Context, observer otelmetric.Observer) error {
			info, err := node.Info()
			if err != nil {
				slog.Debug("failed to get Ergo node info", "error", err)
				return nil // Don't fail the callback
			}

			observer.ObserveInt64(uptimeSeconds, info.Uptime)
			observer.ObserveInt64(processesTotal, info.ProcessesTotal)
			observer.ObserveInt64(processesRunning, info.ProcessesRunning)
			observer.ObserveInt64(processesZombie, info.ProcessesZombee)
			observer.ObserveInt64(memoryUsed, int64(info.MemoryUsed))
			observer.ObserveInt64(memoryAlloc, int64(info.MemoryAlloc))
			observer.ObserveInt64(cpuUser, info.UserTime)
			observer.ObserveInt64(cpuSystem, info.SystemTime)
			observer.ObserveInt64(applicationsTotal, info.ApplicationsTotal)
			observer.ObserveInt64(applicationsRunning, info.ApplicationsRunning)
			observer.ObserveInt64(registeredNames, info.RegisteredNames)
			observer.ObserveInt64(registeredAliases, info.RegisteredAliases)
			observer.ObserveInt64(registeredEvents, info.RegisteredEvents)

			// Network metrics
			network := node.Network()
			if network != nil {
				connectedNodesList := network.Nodes()
				observer.ObserveInt64(connectedNodes, int64(len(connectedNodesList)))

				for _, nodeName := range connectedNodesList {
					remoteNode, err := network.Node(nodeName)
					if err != nil {
						continue
					}
					remoteInfo := remoteNode.Info()
					nodeAttr := otelmetric.WithAttributes(attribute.String("node", string(nodeName)))

					observer.ObserveInt64(remoteNodeUptime, remoteInfo.ConnectionUptime, nodeAttr)
					observer.ObserveInt64(remoteMessagesIn, int64(remoteInfo.MessagesIn), nodeAttr)
					observer.ObserveInt64(remoteMessagesOut, int64(remoteInfo.MessagesOut), nodeAttr)
					observer.ObserveInt64(remoteBytesIn, int64(remoteInfo.BytesIn), nodeAttr)
					observer.ObserveInt64(remoteBytesOut, int64(remoteInfo.BytesOut), nodeAttr)
				}
			}

			return nil
		},
		uptimeSeconds,
		processesTotal,
		processesRunning,
		processesZombie,
		memoryUsed,
		memoryAlloc,
		cpuUser,
		cpuSystem,
		applicationsTotal,
		applicationsRunning,
		registeredNames,
		registeredAliases,
		registeredEvents,
		connectedNodes,
		remoteNodeUptime,
		remoteMessagesIn,
		remoteMessagesOut,
		remoteBytesIn,
		remoteBytesOut,
	)
	if err != nil {
		return fmt.Errorf("failed to register Ergo metrics callback: %w", err)
	}

	slog.Debug("Ergo actor metrics collection started")
	return nil
}

// StatsProvider is an interface for retrieving formae stats.
// Implemented by the metastructure or datastore.
type StatsProvider interface {
	Stats() (*apimodel.Stats, error)
}

// StartFormaeMetrics registers formae-specific metrics with OTel.
// This should be called after the metastructure is ready.
func StartFormaeMetrics(statsProvider StatsProvider) error {
	meter := otel.Meter("formae/stats")

	// Client connections
	clientsConnected, err := meter.Int64ObservableGauge(
		"formae.clients.connected",
		otelmetric.WithDescription("Number of connected clients"),
	)
	if err != nil {
		return fmt.Errorf("failed to create clients connected gauge: %w", err)
	}

	// Commands by type
	commandsTotal, err := meter.Int64ObservableGauge(
		"formae.commands.total",
		otelmetric.WithDescription("Total commands by type"),
	)
	if err != nil {
		return fmt.Errorf("failed to create commands total gauge: %w", err)
	}

	// Commands by state
	commandsByState, err := meter.Int64ObservableGauge(
		"formae.commands.by_state",
		otelmetric.WithDescription("Commands by state"),
	)
	if err != nil {
		return fmt.Errorf("failed to create commands by state gauge: %w", err)
	}

	// Stacks
	stacksTotal, err := meter.Int64ObservableGauge(
		"formae.stacks.total",
		otelmetric.WithDescription("Total number of stacks"),
	)
	if err != nil {
		return fmt.Errorf("failed to create stacks total gauge: %w", err)
	}

	// Managed resources by namespace
	resourcesManaged, err := meter.Int64ObservableGauge(
		"formae.resources.managed",
		otelmetric.WithDescription("Managed resources by namespace"),
	)
	if err != nil {
		return fmt.Errorf("failed to create resources managed gauge: %w", err)
	}

	// Unmanaged resources by namespace
	resourcesUnmanaged, err := meter.Int64ObservableGauge(
		"formae.resources.unmanaged",
		otelmetric.WithDescription("Unmanaged resources by namespace"),
	)
	if err != nil {
		return fmt.Errorf("failed to create resources unmanaged gauge: %w", err)
	}

	// Targets by namespace
	targetsTotal, err := meter.Int64ObservableGauge(
		"formae.targets.total",
		otelmetric.WithDescription("Targets by namespace"),
	)
	if err != nil {
		return fmt.Errorf("failed to create targets total gauge: %w", err)
	}

	// Resources by type
	resourcesByType, err := meter.Int64ObservableGauge(
		"formae.resources.by_type",
		otelmetric.WithDescription("Resources by type"),
	)
	if err != nil {
		return fmt.Errorf("failed to create resources by type gauge: %w", err)
	}

	// Resource errors by type
	resourceErrors, err := meter.Int64ObservableGauge(
		"formae.resource.errors",
		otelmetric.WithDescription("Resource errors by type"),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource errors gauge: %w", err)
	}

	// Register callback to collect all formae metrics
	_, err = meter.RegisterCallback(
		func(ctx context.Context, observer otelmetric.Observer) error {
			stats, err := statsProvider.Stats()
			if err != nil {
				slog.Debug("failed to get formae stats", "error", err)
				return nil // Don't fail the callback
			}

			// Clients
			observer.ObserveInt64(clientsConnected, int64(stats.Clients))

			// Stacks
			observer.ObserveInt64(stacksTotal, int64(stats.Stacks))

			// Commands by type
			for commandType, count := range stats.Commands {
				observer.ObserveInt64(commandsTotal, int64(count),
					otelmetric.WithAttributes(attribute.String("command_type", commandType)))
			}

			// Commands by state
			for state, count := range stats.States {
				observer.ObserveInt64(commandsByState, int64(count),
					otelmetric.WithAttributes(attribute.String("state", state)))
			}

			// Managed resources by plugin
			for plugin, count := range stats.ManagedResources {
				observer.ObserveInt64(resourcesManaged, int64(count),
					otelmetric.WithAttributes(attribute.String("plugin", plugin)))
			}

			// Unmanaged resources by plugin
			for plugin, count := range stats.UnmanagedResources {
				observer.ObserveInt64(resourcesUnmanaged, int64(count),
					otelmetric.WithAttributes(attribute.String("plugin", plugin)))
			}

			// Targets by plugin
			for plugin, count := range stats.Targets {
				observer.ObserveInt64(targetsTotal, int64(count),
					otelmetric.WithAttributes(attribute.String("plugin", plugin)))
			}

			// Resources by type (extract plugin from type)
			for resourceType, count := range stats.ResourceTypes {
				plugin := extractNamespace(resourceType)
				observer.ObserveInt64(resourcesByType, int64(count),
					otelmetric.WithAttributes(
						attribute.String("plugin", plugin),
						attribute.String("resource_type", resourceType),
					))
			}

			// Resource errors by type
			for resourceType, count := range stats.ResourceErrors {
				plugin := extractNamespace(resourceType)
				observer.ObserveInt64(resourceErrors, int64(count),
					otelmetric.WithAttributes(
						attribute.String("plugin", plugin),
						attribute.String("resource_type", resourceType),
					))
			}

			return nil
		},
		clientsConnected,
		commandsTotal,
		commandsByState,
		stacksTotal,
		resourcesManaged,
		resourcesUnmanaged,
		targetsTotal,
		resourcesByType,
		resourceErrors,
	)
	if err != nil {
		return fmt.Errorf("failed to register formae metrics callback: %w", err)
	}

	slog.Debug("Formae stats metrics collection started")
	return nil
}

// extractNamespace extracts the namespace (e.g., "AWS") from a resource type (e.g., "AWS::S3::Bucket")
func extractNamespace(resourceType string) string {
	for i, c := range resourceType {
		if c == ':' {
			return resourceType[:i]
		}
	}
	return resourceType
}
