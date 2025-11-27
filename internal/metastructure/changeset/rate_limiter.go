// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"fmt"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
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

	l.Log().Info("RateLimiter started with %d buckets", len(l.buckets))
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
		bucket.replenish()
		n := util.Min(msg.N, bucket.Tokens)
		bucket.consume(n)
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
		l.Log().Info("Registered namespace %s with rate limit %d", msg.Namespace, msg.MaxRequestsPerSecond)
	default:
		l.Log().Debug("Received unknown message", "type", fmt.Sprintf("%T", message))
	}
	return nil
}
