// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
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
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

func main() {
	cs := NewCloudState()
	ol := NewOperationLog()
	inj := NewInjectionState()
	rq := NewResponseQueue()

	p := &TestPlugin{
		cloudState:    cs,
		injections:    inj,
		responseQueue: rq,
		opLog:         ol,
	}

	// Register message types for network serialization
	if err := plugin.RegisterSharedEDFTypes(); err != nil {
		log.Printf("Warning: failed to register shared EDF types: %v", err)
	}
	if err := testcontrol.RegisterEDFTypes(); err != nil {
		log.Printf("Warning: failed to register testcontrol EDF types: %v", err)
	}

	// Read configuration from environment (set by the agent when spawning the plugin)
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

	// Setup Ergo node options (mirrors pkg/plugin.Run)
	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	options.Network.Cookie = cookie
	options.Security.ExposeEnvRemoteSpawn = true
	options.Log.Level = gen.LogLevelDebug

	options.Network.Acceptors = []gen.AcceptorOptions{
		{
			Host: "localhost",
			Port: 0, // auto-select free port
		},
	}

	// Configure registrar if specified (enables parallel test execution)
	if registrarPortStr := os.Getenv("FORMAE_REGISTRAR_PORT"); registrarPortStr != "" {
		if registrarPort, err := strconv.Atoi(registrarPortStr); err == nil && registrarPort != 0 {
			options.Network.Registrar = registrar.Create(registrar.Options{Port: uint16(registrarPort)})
		}
	}

	// Set environment for PluginActor, TestController, and remotely spawned PluginOperators
	options.Env = map[gen.Env]any{
		gen.Env("Context"):        context.Background(),
		gen.Env("Plugin"):         plugin.FullResourcePlugin(p),
		gen.Env("Namespace"):      p.Namespace(),
		gen.Env("AgentNode"):      gen.Atom(agentNode),
		gen.Env("CloudState"):     cs,
		gen.Env("InjectionState"): inj,
		gen.Env("OperationLog"):   ol,
		gen.Env("ResponseQueue"):  rq,
		gen.Env("RetryConfig"): model.RetryConfig{
			StatusCheckInterval: 5 * time.Second,
			MaxRetries:          3,
			RetryDelay:          2 * time.Second,
		},
	}

	// Use the standard plugin application (spawns PluginActor for announcement/monitoring)
	options.Applications = []gen.ApplicationBehavior{
		&plugin.PluginApplication{},
	}

	// Start Ergo node
	node, err := ergo.StartNode(gen.Atom(pluginNode), options)
	if err != nil {
		log.Fatalf("Failed to start node: %v", err)
	}

	// Spawn TestController actor on this node
	if _, err := node.SpawnRegister(testcontrol.TestControllerName, NewTestController, gen.ProcessOptions{}); err != nil {
		log.Fatalf("Failed to spawn TestController: %v", err)
	}

	// Enable remote spawn for PluginOperator so agent can spawn operators on this node
	if err := node.Network().EnableSpawn(gen.Atom(plugin.PluginOperatorFactoryName), plugin.NewPluginOperator); err != nil {
		log.Fatalf("Failed to enable spawn for PluginOperator: %v", err)
	}

	fmt.Printf("Test plugin started (node: %s, agent: %s)\n", pluginNode, agentNode)

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("Shutting down test plugin...")
	node.Stop()
}