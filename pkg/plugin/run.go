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
	"syscall"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
	"github.com/platform-engineering-labs/formae/pkg/model"
)

// PluginCapabilities contains all plugin capability data.
// This struct is gzip-compressed for network transfer to work around
// Ergo's hardcoded 64KB message buffer limit.
type PluginCapabilities struct {
	SupportedResources []ResourceDescriptor
	ResourceSchemas    map[string]model.Schema // key = resource type
	MatchFilters       []MatchFilter
}

// PluginAnnouncement is sent by plugins to PluginCoordinator on startup.
// It contains all information needed for the agent to interact with the plugin.
type PluginAnnouncement struct {
	Namespace            string // e.g., "FakeAWS", "AWS", "Azure"
	NodeName             string // Ergo node name where plugin runs, e.g., "fakeaws-plugin@localhost"
	MaxRequestsPerSecond int    // Rate limit for this plugin

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
func Run(rp ResourcePlugin) {
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

	fmt.Printf("Network cookie: %s\n", cookie)
	// Setup Ergo node options
	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	options.Network.Cookie = cookie
	options.Security.ExposeEnvRemoteSpawn = true
	options.Log.Level = gen.LogLevelDebug

	// Set environment for PluginActor and remotely spawned PluginOperators
	options.Env = map[gen.Env]any{
		gen.Env("Context"):   context.Background(),
		gen.Env("Plugin"):    rp,
		gen.Env("Namespace"): rp.Namespace(),
		gen.Env("AgentNode"): gen.Atom(agentNode),
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

	// Enable remote spawn for PluginOperator so agent can spawn operators on this node
	if err := node.Network().EnableSpawn(gen.Atom(PluginOperatorFactoryName), NewPluginOperator); err != nil {
		log.Fatalf("Failed to enable spawn for PluginOperator: %v", err)
	}

	fmt.Printf("%s plugin started\n", rp.Namespace())
	fmt.Printf("Plugin node: %s\n", pluginNode)
	fmt.Printf("Agent node: %s\n", agentNode)

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Printf("Shutting down %s plugin...\n", rp.Namespace())
	node.Stop()
}

// registerEDFTypes registers all message types needed for network serialization
// between the agent and plugin processes.
func registerEDFTypes() {
	// Register shared types (used by both agent and plugins)
	if err := RegisterSharedEDFTypes(); err != nil {
		log.Printf("Warning: failed to register shared EDF types: %v", err)
	}

	// Register plugin-specific types (not needed by agent)
	if err := edf.RegisterTypeOf(GetFilters{}); err != nil {
		log.Printf("Warning: failed to register GetFilters type: %v", err)
	}
}
