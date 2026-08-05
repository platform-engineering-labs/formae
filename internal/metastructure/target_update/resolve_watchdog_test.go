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

// watchdogEnvProcess is a gen.Process double that serves a RetryConfig from its
// environment, so resolveWatchdogTimeout can be exercised in isolation.
type watchdogEnvProcess struct {
	gen.Process
	env map[gen.Env]any
}

func (p *watchdogEnvProcess) Env(name gen.Env) (any, bool) {
	v, ok := p.env[name]
	return v, ok
}

// TestResolveWatchdogTimeout_CoversExponentialBackoff asserts the TargetUpdater
// resolve watchdog is derived from RetryStrategy.MaxTotalDelay and, for a large
// MaxRetries, strictly exceeds the old flat (MaxRetries-1)*RetryDelay estimate,
// so exponential-backoff resolve retries cannot trip the watchdog.
func TestResolveWatchdogTimeout_CoversExponentialBackoff(t *testing.T) {
	cfg := pkgmodel.RetryConfig{MaxRetries: 8, RetryDelay: 1 * time.Second}
	proc := &watchdogEnvProcess{env: map[gen.Env]any{"RetryConfig": cfg}}

	const perAttempt = 60 * time.Second
	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	want := time.Duration(cfg.MaxRetries)*perAttempt + strategy.MaxTotalDelay() + 30*time.Second
	assert.Equal(t, want, resolveWatchdogTimeout(proc))

	flatEstimate := time.Duration(cfg.MaxRetries)*perAttempt +
		time.Duration(cfg.MaxRetries-1)*cfg.RetryDelay + 30*time.Second
	assert.Greater(t, resolveWatchdogTimeout(proc), flatEstimate,
		"budget-aware watchdog must exceed the old flat estimate")
}

// TestResolveWatchdogTimeout_NoEnvFallback asserts that without a RetryConfig in
// the environment (a bare unit harness) the watchdog falls back to a fixed,
// generous default rather than a zero-length timeout.
func TestResolveWatchdogTimeout_NoEnvFallback(t *testing.T) {
	proc := &watchdogEnvProcess{env: map[gen.Env]any{}}
	assert.Equal(t, 60*time.Second+30*time.Second, resolveWatchdogTimeout(proc))
}
