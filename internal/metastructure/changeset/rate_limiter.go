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
	mgr, ok := l.Env("PluginManager")
	if !ok {
		l.Log().Error("RateLimiter: missing 'PluginManager' environment variable")
		return fmt.Errorf("rateLimiter: missing 'PluginManager' environment variable")
	}
	l.pluginManager = mgr.(*plugin.Manager)

	l.buckets = make(map[string]*TokenBucket)
	for _, plugin := range l.pluginManager.ListResourcePlugins() {
		ns := (*plugin).Namespace()
		rateLimit := (*plugin).MaxRequestsPerSecond()
		l.buckets[ns] = &TokenBucket{
			Tokens:     rateLimit,
			Capacity:   rateLimit,
			LastRefill: time.Now(),
		}
	}

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
