// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
)

// PluginAnnouncement is sent by plugins to PluginRegistry on startup.
type PluginAnnouncement struct {
	Namespace string
}

// Run starts the plugin process and announces it to the agent's PluginRegistry.
// This is the main entry point for resource plugins.
func Run(rp ResourcePlugin) {
	// Register message types for network serialization
	if err := edf.RegisterTypeOf(PluginAnnouncement{}); err != nil {
		log.Printf("Warning: failed to register PluginAnnouncement type: %v", err)
	}

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

	// Set environment for PluginActor
	options.Env = map[gen.Env]any{
		gen.Env("Plugin"):    rp,
		gen.Env("Namespace"): rp.Namespace(),
		gen.Env("AgentNode"): gen.Atom(agentNode),
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
