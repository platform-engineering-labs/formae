// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package target_update

import (
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// pluginReadCallTimeout is the per-read call budget the ResolveCache's plugin
// reads actually run under — resource_update.PluginOperationCallTimeout, which
// bounds resource_update.ReadResourceViaPlugin. It is restated here because
// target_update cannot import resource_update (that package imports this one),
// and the resolve envelope is only sound while the two agree.
const pluginReadCallTimeout = 70 * time.Second

// timeoutEnvProcess is a gen.Process double that serves a RetryConfig from its
// environment, so resolvingTimeout can be exercised in isolation.
type timeoutEnvProcess struct {
	gen.Process
	env map[gen.Env]any
}

func (p *timeoutEnvProcess) Env(name gen.Env) (any, bool) {
	v, ok := p.env[name]
	return v, ok
}

// TestResolveTimeoutTimeout_CoversExponentialBackoff asserts the TargetUpdater
// resolve timeout is derived from RetryStrategy.MaxTotalDelay and, for a large
// MaxRetries, strictly exceeds the old flat (MaxRetries-1)*RetryDelay estimate,
// so exponential-backoff resolve retries cannot trip the timeout.
func TestResolveTimeoutTimeout_CoversExponentialBackoff(t *testing.T) {
	cfg := pkgmodel.RetryConfig{MaxRetries: 8, RetryDelay: 1 * time.Second}
	proc := &timeoutEnvProcess{env: map[gen.Env]any{"RetryConfig": cfg}}

	const perAttempt = pluginReadCallTimeout
	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	// The retry loop performs MaxRetries+1 reads (the initial read plus MaxRetries
	// retries), so the envelope must budget one read per attempt, not per retry.
	want := time.Duration(cfg.MaxRetries+1)*perAttempt + strategy.MaxTotalDelay() + 30*time.Second
	assert.Equal(t, want, resolvingTimeout(proc))

	// Invariant: the envelope must cover every attempt plus the full backoff
	// budget, or it can fire mid-retry.
	assert.GreaterOrEqual(t, resolvingTimeout(proc),
		time.Duration(cfg.MaxRetries+1)*perAttempt+strategy.MaxTotalDelay(),
		"timeout must cover MaxRetries+1 attempts plus the backoff budget")

	flatEstimate := time.Duration(cfg.MaxRetries)*perAttempt +
		time.Duration(cfg.MaxRetries-1)*cfg.RetryDelay + 30*time.Second
	assert.Greater(t, resolvingTimeout(proc), flatEstimate,
		"budget-aware timeout must exceed the old flat estimate")
}

// TestResolveTimeoutTimeout_CoversEveryReadAtTheCallTimeout asserts the envelope
// budgets one full plugin-read call budget per attempt at the timeout those reads
// actually run under, plus the whole backoff budget. A per-attempt budget below
// that ceiling fires ResolveTimedOut mid-retry on a resolve that legitimately
// exhausts its retries, failing the target update spuriously.
func TestResolveTimeoutTimeout_CoversEveryReadAtTheCallTimeout(t *testing.T) {
	// The shipped RetryConfig defaults, as defined in Config.pkl.
	cfg := pkgmodel.RetryConfig{MaxRetries: 9, RetryDelay: 10 * time.Second}
	proc := &timeoutEnvProcess{env: map[gen.Env]any{"RetryConfig": cfg}}

	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	assert.GreaterOrEqual(t, resolvingTimeout(proc),
		time.Duration(cfg.MaxRetries+1)*pluginReadCallTimeout+strategy.MaxTotalDelay(),
		"the envelope must budget every read at the call timeout the reads run under")
}

// TestResolveTimeoutTimeout_NoEnvFallback asserts that without a RetryConfig in
// the environment (a bare unit harness) the timeout falls back to a fixed,
// generous default rather than a zero-length timeout.
func TestResolveTimeoutTimeout_NoEnvFallback(t *testing.T) {
	proc := &timeoutEnvProcess{env: map[gen.Env]any{}}
	assert.Equal(t, pluginReadCallTimeout+30*time.Second, resolvingTimeout(proc))
}
