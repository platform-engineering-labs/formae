// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
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
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// PluginAnnouncement is sent by plugins to PluginRegistry on startup.
// It contains all information needed for the agent to interact with the plugin.
type PluginAnnouncement struct {
	Namespace            string // e.g., "FakeAWS", "AWS", "Azure"
	NodeName             string // Ergo node name where plugin runs, e.g., "fakeaws-plugin@localhost"
	MaxRequestsPerSecond int    // Rate limit for this plugin
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

	// Setup Ergo node options
	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	options.Network.Cookie = cookie
	options.Security.ExposeEnvRemoteSpawn = true

	// Set environment for PluginActor and remotely spawned PluginOperators
	options.Env = map[gen.Env]any{
		gen.Env("Plugin"):    rp,
		gen.Env("Namespace"): rp.Namespace(),
		gen.Env("AgentNode"): gen.Atom(agentNode),
		// Context for plugin operations (used by remotely spawned PluginOperators)
		gen.Env("Context"): context.Background(),
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
	// Plugin announcement
	if err := edf.RegisterTypeOf(PluginAnnouncement{}); err != nil {
		log.Printf("Warning: failed to register PluginAnnouncement type: %v", err)
	}

	// Operator message types (for remote spawn and message passing)
	if err := edf.RegisterTypeOf(ReadResource{}); err != nil {
		log.Printf("Warning: failed to register ReadResource type: %v", err)
	}
	if err := edf.RegisterTypeOf(CreateResource{}); err != nil {
		log.Printf("Warning: failed to register CreateResource type: %v", err)
	}
	if err := edf.RegisterTypeOf(UpdateResource{}); err != nil {
		log.Printf("Warning: failed to register UpdateResource type: %v", err)
	}
	if err := edf.RegisterTypeOf(DeleteResource{}); err != nil {
		log.Printf("Warning: failed to register DeleteResource type: %v", err)
	}
	if err := edf.RegisterTypeOf(ListResources{}); err != nil {
		log.Printf("Warning: failed to register ListResources type: %v", err)
	}
	if err := edf.RegisterTypeOf(PluginOperatorCheckStatus{}); err != nil {
		log.Printf("Warning: failed to register PluginOperatorCheckStatus type: %v", err)
	}
	if err := edf.RegisterTypeOf(Listing{}); err != nil {
		log.Printf("Warning: failed to register Listing type: %v", err)
	}
	if err := edf.RegisterTypeOf(PluginOperatorShutdown{}); err != nil {
		log.Printf("Warning: failed to register PluginOperatorShutdown type: %v", err)
	}
	if err := edf.RegisterTypeOf(PluginOperatorRetry{}); err != nil {
		log.Printf("Warning: failed to register PluginOperatorRetry type: %v", err)
	}
	if err := edf.RegisterTypeOf(ResumeWaitingForResource{}); err != nil {
		log.Printf("Warning: failed to register ResumeWaitingForResource type: %v", err)
	}

	// Resource types (progress results sent back to agent)
	if err := edf.RegisterTypeOf(resource.ProgressResult{}); err != nil {
		log.Printf("Warning: failed to register ProgressResult type: %v", err)
	}
}
