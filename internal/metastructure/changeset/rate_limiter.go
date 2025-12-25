// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

type RequestTokens struct {
	Namespace string
	N         int
}

type TokensGranted struct {
	N int
}

// RegisterNamespace registers a new namespace with its rate limit.
// Sent by PluginCoordinator when a plugin announces itself.
type RegisterNamespace struct {
	Namespace            string
	MaxRequestsPerSecond int
}

type TokenBucket struct {
	Tokens     int
	Capacity   int
	LastRefill time.Time
}

func (b *TokenBucket) consume(n int) {
	b.Tokens -= n
}

func (b *TokenBucket) replenish() {
	if time.Since(b.LastRefill) > 1*time.Second {
		b.Tokens = b.Capacity
		b.LastRefill = time.Now()
	}
}

type RateLimiter struct {
	act.Actor

	pluginManager *plugin.Manager
	// buckets maps namespace to buckets. Each plugin can define its own rate limit.
	buckets map[string]*TokenBucket

	// Metrics
	tokensRequested otelmetric.Int64Counter
	tokensGranted   otelmetric.Int64Counter
}

func NewRateLimiter() gen.ProcessBehavior {
	return &RateLimiter{}
}

func (l *RateLimiter) Init(args ...any) error {
	l.buckets = make(map[string]*TokenBucket)

	// PluginManager is optional - in distributed mode, namespaces are registered
	// dynamically via RegisterNamespace messages from PluginCoordinator
	mgr, ok := l.Env("PluginManager")
	if ok {
		l.pluginManager = mgr.(*plugin.Manager)
		// Register in-process plugins
		for _, plugin := range l.pluginManager.ListResourcePlugins() {
			ns := (*plugin).Namespace()
			rateLimit := (*plugin).MaxRequestsPerSecond()
			l.buckets[ns] = &TokenBucket{
				Tokens:     rateLimit,
				Capacity:   rateLimit,
				LastRefill: time.Now(),
			}
			l.Log().Debug("Registered in-process plugin", "namespace", ns, "rateLimit", rateLimit)
		}
	}

	// Initialize OTel metrics
	if err := l.setupMetrics(); err != nil {
		l.Log().Error("Failed to setup rate limiter metrics: %v", err)
		// Don't fail initialization if metrics setup fails
	}

	l.Log().Debug("RateLimiter started with %d buckets", len(l.buckets))
	return nil
}

func (l *RateLimiter) HandleCall(from gen.PID, ref gen.Ref, message any) (any, error) {
	switch msg := message.(type) {
	case RequestTokens:
		bucket, exists := l.buckets[msg.Namespace]
		if !exists {
			l.Log().Error(fmt.Sprintf("RateLimiter: unknown namespace requested: %s", msg.Namespace))
			return TokensGranted{N: 0}, fmt.Errorf("unknown namespace: %s", msg.Namespace)
		}

		// Extract requester name for metrics
		requester := l.getRequesterName(from)
		attrs := otelmetric.WithAttributes(
			attribute.String("namespace", msg.Namespace),
			attribute.String("requester", requester),
		)

		// Record tokens requested
		if l.tokensRequested != nil {
			l.tokensRequested.Add(context.Background(), int64(msg.N), attrs)
		}

		bucket.replenish()
		n := util.Min(msg.N, bucket.Tokens)
		bucket.consume(n)

		// Record tokens granted
		if l.tokensGranted != nil {
			l.tokensGranted.Add(context.Background(), int64(n), attrs)
		}

		return TokensGranted{N: n}, nil
	default:
		return TokensGranted{N: 0}, fmt.Errorf("unhandled message type: %T", msg)
	}
}

func (l *RateLimiter) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case RegisterNamespace:
		// Register or update a namespace with its rate limit
		l.buckets[msg.Namespace] = &TokenBucket{
			Tokens:     msg.MaxRequestsPerSecond,
			Capacity:   msg.MaxRequestsPerSecond,
			LastRefill: time.Now(),
		}
		l.Log().Debug("Registered namespace %s with rate limit %d", msg.Namespace, msg.MaxRequestsPerSecond)
	default:
		l.Log().Debug("Received unknown message", "type", fmt.Sprintf("%T", message))
	}
	return nil
}

// setupMetrics initializes OTel metrics for the rate limiter
func (l *RateLimiter) setupMetrics() error {
	meter := otel.Meter("formae/rate_limiter")

	// Create counter for tokens requested
	var err error
	l.tokensRequested, err = meter.Int64Counter(
		"formae.rate_limiter.tokens.requested",
		otelmetric.WithDescription("Total tokens requested from rate limiter"),
		otelmetric.WithUnit("{token}"),
	)
	if err != nil {
		return fmt.Errorf("failed to create tokens requested counter: %w", err)
	}

	// Create counter for tokens granted
	l.tokensGranted, err = meter.Int64Counter(
		"formae.rate_limiter.tokens.granted",
		otelmetric.WithDescription("Total tokens granted by rate limiter"),
		otelmetric.WithUnit("{token}"),
	)
	if err != nil {
		return fmt.Errorf("failed to create tokens granted counter: %w", err)
	}

	// Create observable gauges for bucket state
	bucketsAvailable, err := meter.Int64ObservableGauge(
		"formae.rate_limiter.bucket.tokens_available",
		otelmetric.WithDescription("Current tokens available in bucket"),
		otelmetric.WithUnit("{token}"),
	)
	if err != nil {
		return fmt.Errorf("failed to create bucket tokens available gauge: %w", err)
	}

	bucketsCapacity, err := meter.Int64ObservableGauge(
		"formae.rate_limiter.bucket.capacity",
		otelmetric.WithDescription("Maximum bucket capacity"),
		otelmetric.WithUnit("{token}"),
	)
	if err != nil {
		return fmt.Errorf("failed to create bucket capacity gauge: %w", err)
	}

	bucketsUtilization, err := meter.Float64ObservableGauge(
		"formae.rate_limiter.bucket.utilization",
		otelmetric.WithDescription("Bucket utilization ratio (0.0 to 1.0)"),
		otelmetric.WithUnit("1"),
	)
	if err != nil {
		return fmt.Errorf("failed to create bucket utilization gauge: %w", err)
	}

	namespacesRegistered, err := meter.Int64ObservableGauge(
		"formae.rate_limiter.namespaces.registered",
		otelmetric.WithDescription("Number of registered namespaces"),
		otelmetric.WithUnit("{namespace}"),
	)
	if err != nil {
		return fmt.Errorf("failed to create namespaces registered gauge: %w", err)
	}

	// Register callback to collect bucket state metrics
	_, err = meter.RegisterCallback(
		func(ctx context.Context, observer otelmetric.Observer) error {
			for namespace, bucket := range l.buckets {
				attrs := otelmetric.WithAttributes(
					attribute.String("namespace", namespace),
				)

				observer.ObserveInt64(bucketsAvailable, int64(bucket.Tokens), attrs)
				observer.ObserveInt64(bucketsCapacity, int64(bucket.Capacity), attrs)

				if bucket.Capacity > 0 {
					utilization := float64(bucket.Capacity-bucket.Tokens) / float64(bucket.Capacity)
					observer.ObserveFloat64(bucketsUtilization, utilization, attrs)
				}
			}

			observer.ObserveInt64(namespacesRegistered, int64(len(l.buckets)))
			return nil
		},
		bucketsAvailable,
		bucketsCapacity,
		bucketsUtilization,
		namespacesRegistered,
	)
	if err != nil {
		return fmt.Errorf("failed to register callback: %w", err)
	}

	return nil
}

// getRequesterName gets the process name from PID and extracts the actor type.
// This avoids high cardinality by stripping unique IDs.
func (l *RateLimiter) getRequesterName(from gen.PID) string {
	// Try to get process info first
	processInfo, err := l.Node().ProcessInfo(from)
	if err != nil {
		// ProcessInfo can fail for remote PIDs or terminated processes
		// This is rare but can happen during process lifecycle transitions
		// Log at trace level to avoid noise, since this might be normal during cleanup
		l.Log().Trace("Failed to get process info for PID %v: %v", from, err)
		return "Unknown"
	}

	return extractRequesterName(string(processInfo.Name))
}

// extractRequesterName extracts just the actor type from process name for metrics.
// This avoids high cardinality by stripping unique IDs and URLs.
func extractRequesterName(processName string) string {
	// Examples from actornames package:
	// "Discovery" -> "Discovery"
	// "Synchronizer" -> "Synchronizer"
	// "formae://changeset/executor/{commandID}" -> "ChangesetExecutor"
	// "formae://changeset/resolve-cache/{commandID}" -> "ResolveCache"
	// "formae://{ksuid}#/resource-updater/{operation}/{commandID}" -> "ResourceUpdater"
	// "formae://{ksuid}#{operation}/{operationID}" -> "PluginOperator" (rare, these run remotely)

	// Handle formae:// URLs that contain actor types in the path
	if strings.HasPrefix(processName, "formae://") {
		// ChangesetExecutor: "formae://changeset/executor/{commandID}"
		if strings.Contains(processName, "/executor/") {
			return "ChangesetExecutor"
		}
		// ResolveCache: "formae://changeset/resolve-cache/{commandID}"
		if strings.Contains(processName, "/resolve-cache/") {
			return "ResolveCache"
		}
		// ResourceUpdater: "formae://{ksuid}#/resource-updater/{operation}/{commandID}"
		if strings.Contains(processName, "/resource-updater/") {
			return "ResourceUpdater"
		}
		// PluginOperator: "formae://{ksuid}#{operation}/{operationID}"
		// This is rare as plugin operators run remotely, but handle it
		return "PluginOperator"
	}

	// Simple constant names like "Discovery", "Synchronizer"
	// These don't have any prefix or suffix, just return as-is
	return processName
}
