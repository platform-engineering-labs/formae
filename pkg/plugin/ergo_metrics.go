// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"fmt"
	"log/slog"

	"ergo.services/ergo/gen"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// StartErgoMetrics registers Ergo actor framework metrics with OTel.
// This can be called by both the formae agent and plugins to expose
// their Ergo node metrics. The metrics will be differentiated by the
// service.name attribute set during OTel provider setup.
//
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
