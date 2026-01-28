// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/registrar"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	otelresource "go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.22.0"
)

// PluginCapabilities contains all plugin capability data.
// This struct is gzip-compressed for network transfer to work around
// Ergo's hardcoded 64KB message buffer limit.
type PluginCapabilities struct {
	SupportedResources []ResourceDescriptor
	ResourceSchemas    map[string]model.Schema // key = resource type
	MatchFilters       []MatchFilter
	LabelConfig        LabelConfig
}

// PluginAnnouncement is sent by plugins to PluginCoordinator on startup.
// It contains all information needed for the agent to interact with the plugin.
type PluginAnnouncement struct {
	Namespace            string
	Version              string
	NodeName             string
	MaxRequestsPerSecond int

	// Capabilities contains gzip-compressed JSON of PluginCapabilities.
	// Use CompressCapabilities() and DecompressCapabilities() helpers.
	// This compression is required because Ergo has a hardcoded 64KB buffer
	// limit that cannot be configured via MaxMessageSize.
	Capabilities []byte
}

// CompressCapabilities compresses PluginCapabilities to gzip-compressed JSON.
func CompressCapabilities(caps PluginCapabilities) ([]byte, error) {
	jsonData, err := json.Marshal(caps)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal capabilities: %w", err)
	}

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip writer: %w", err)
	}

	if _, err := gz.Write(jsonData); err != nil {
		return nil, fmt.Errorf("failed to write compressed data: %w", err)
	}

	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	return buf.Bytes(), nil
}

// DecompressCapabilities decompresses gzip-compressed JSON to PluginCapabilities.
func DecompressCapabilities(data []byte) (PluginCapabilities, error) {
	var caps PluginCapabilities

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return caps, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gz.Close()

	if err := json.NewDecoder(gz).Decode(&caps); err != nil {
		return caps, fmt.Errorf("failed to decode capabilities: %w", err)
	}

	return caps, nil
}

// Run starts the plugin process and announces it to the agent's PluginRegistry.
// This is the main entry point for resource plugins.
//
// For external plugins, use RunWithManifest which reads the manifest and wraps
// a simplified ResourcePlugin. For built-in plugins that implement FullResourcePlugin
// directly, use this function.
func Run(fp FullResourcePlugin) {
	// Register message types for network serialization
	registerEDFTypes()

	// Read configuration from environment
	agentNode := os.Getenv("FORMAE_AGENT_NODE")
	if agentNode == "" {
		log.Fatal("FORMAE_AGENT_NODE environment variable required")
	}

	pluginNode := os.Getenv("FORMAE_PLUGIN_NODE")
	if pluginNode == "" {
		log.Fatal("FORMAE_PLUGIN_NODE environment variable required")
	}

	cookie := os.Getenv("FORMAE_NETWORK_COOKIE")
	if cookie == "" {
		log.Fatal("FORMAE_NETWORK_COOKIE environment variable required")
	}

	// Read OTel config from environment variables for initial setup
	otelConfigForSetup := readOTelConfigFromEnv()

	// Setup OTel metrics if enabled
	shutdown := setupPluginOTel(fp.Namespace(), otelConfigForSetup)
	defer shutdown()

	// Setup Ergo node options
	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	options.Network.Cookie = cookie
	options.Security.ExposeEnvRemoteSpawn = true
	options.Log.Level = gen.LogLevelDebug

	// Configure Ergo port if specified (enables parallel test execution)
	if portStr := os.Getenv("FORMAE_ERGO_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil && port != 0 {
			options.Network.Acceptors = []gen.AcceptorOptions{
				{
					Host:      "localhost",
					Port:      uint16(port),
					Registrar: registrar.Create(registrar.Options{Port: uint16(port)}),
				},
			}
		}
	}

	// Read OTel config from environment variables (for standalone plugin process startup)
	// This will be overridden by agent when spawning PluginOperator actors remotely
	otelConfig := readOTelConfigFromEnv()

	// Set environment for PluginActor and remotely spawned PluginOperators
	options.Env = map[gen.Env]any{
		gen.Env("Context"):    context.Background(),
		gen.Env("Plugin"):     fp,
		gen.Env("Namespace"):  fp.Namespace(),
		gen.Env("AgentNode"):  gen.Atom(agentNode),
		gen.Env("OTelConfig"): otelConfig,
		// Default retry config for plugin operations
		gen.Env("RetryConfig"): model.RetryConfig{
			StatusCheckInterval: 5 * time.Second,
			MaxRetries:          3,
			RetryDelay:          2 * time.Second,
		},
	}

	// Register and load the plugin application
	options.Applications = []gen.ApplicationBehavior{
		createPluginApplication(),
	}

	// Start Ergo node
	node, err := ergo.StartNode(gen.Atom(pluginNode), options)
	if err != nil {
		log.Fatalf("Failed to start node: %v", err)
	}

	// Start Ergo metrics collection (if OTel is enabled)
	if err := StartErgoMetrics(node); err != nil {
		log.Printf("Warning: failed to start Ergo metrics: %v", err)
	}

	// Enable remote spawn for PluginOperator so agent can spawn operators on this node
	if err := node.Network().EnableSpawn(gen.Atom(PluginOperatorFactoryName), NewPluginOperator); err != nil {
		log.Fatalf("Failed to enable spawn for PluginOperator: %v", err)
	}

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Printf("Shutting down %s plugin...\n", fp.Namespace())
	node.Stop()
}

// registerEDFTypes registers all message types needed for network serialization
// between the agent and plugin processes.
func registerEDFTypes() {
	// Register shared types (used by both agent and plugins)
	if err := RegisterSharedEDFTypes(); err != nil {
		log.Printf("Warning: failed to register shared EDF types: %v", err)
	}
}

// readOTelConfigFromEnv reads OTel configuration from environment variables.
// This is used when the plugin process starts standalone.
func readOTelConfigFromEnv() model.OTelConfig {
	enabled, _ := strconv.ParseBool(os.Getenv("FORMAE_OTEL_ENABLED"))
	if !enabled {
		return model.OTelConfig{Enabled: false}
	}

	endpoint := os.Getenv("FORMAE_OTEL_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	protocol := os.Getenv("FORMAE_OTEL_PROTOCOL")
	if protocol == "" {
		protocol = "grpc"
	}

	insecure, _ := strconv.ParseBool(os.Getenv("FORMAE_OTEL_INSECURE"))
	if os.Getenv("FORMAE_OTEL_INSECURE") == "" {
		insecure = true
	}

	return model.OTelConfig{
		Enabled:     true,
		ServiceName: "formae-plugin",
		OTLP: model.OTLPConfig{
			Endpoint:    endpoint,
			Protocol:    protocol,
			Insecure:    insecure,
			Temporality: "delta",
		},
	}
}

// setupPluginOTel initializes OpenTelemetry metrics for the plugin process.
// Returns a shutdown function that should be called on exit.
func setupPluginOTel(namespace string, config model.OTelConfig) func() {
	if !config.Enabled {
		return func() {}
	}

	endpoint := config.OTLP.Endpoint
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	protocol := config.OTLP.Protocol
	if protocol == "" {
		protocol = "grpc"
	}

	insecure := config.OTLP.Insecure

	// Create resource with plugin-specific attributes
	res, err := otelresource.New(context.Background(),
		otelresource.WithAttributes(
			semconv.ServiceNameKey.String("formae-plugin-"+namespace),
			attribute.String("plugin.namespace", namespace),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stdout, "Warning: failed to create OTel resource: %v\n", err)
		return func() {}
	}

	// Create OTLP exporter
	var exporter metric.Exporter
	switch protocol {
	case "grpc":
		opts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(endpoint),
		}
		if insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		exporter, err = otlpmetricgrpc.New(context.Background(), opts...)
	case "http":
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(endpoint),
		}
		if insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		exporter, err = otlpmetrichttp.New(context.Background(), opts...)
	default:
		fmt.Fprintf(os.Stdout, "Warning: unknown OTLP protocol: %s, skipping OTel setup\n", protocol)
		return func() {}
	}

	if err != nil {
		fmt.Fprintf(os.Stdout, "Warning: failed to create OTLP exporter: %v\n", err)
		return func() {}
	}

	// Create meter provider with periodic reader
	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(exporter, metric.WithInterval(10*time.Second))),
	)

	// Set global meter provider
	otel.SetMeterProvider(meterProvider)

	fmt.Fprintf(os.Stdout, "OTel metrics enabled for plugin %s (endpoint: %s, protocol: %s)\n", namespace, endpoint, protocol)

	// Return shutdown function
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := meterProvider.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stdout, "Warning: failed to shutdown meter provider: %v\n", err)
		}
	}
}
