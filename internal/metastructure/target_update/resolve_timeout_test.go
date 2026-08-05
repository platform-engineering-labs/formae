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

	const perAttempt = 60 * time.Second
	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	want := time.Duration(cfg.MaxRetries)*perAttempt + strategy.MaxTotalDelay() + 30*time.Second
	assert.Equal(t, want, resolvingTimeout(proc))

	flatEstimate := time.Duration(cfg.MaxRetries)*perAttempt +
		time.Duration(cfg.MaxRetries-1)*cfg.RetryDelay + 30*time.Second
	assert.Greater(t, resolvingTimeout(proc), flatEstimate,
		"budget-aware timeout must exceed the old flat estimate")
}

// TestResolveTimeoutTimeout_NoEnvFallback asserts that without a RetryConfig in
// the environment (a bare unit harness) the timeout falls back to a fixed,
// generous default rather than a zero-length timeout.
func TestResolveTimeoutTimeout_NoEnvFallback(t *testing.T) {
	proc := &timeoutEnvProcess{env: map[gen.Env]any{}}
	assert.Equal(t, 60*time.Second+30*time.Second, resolvingTimeout(proc))
}
