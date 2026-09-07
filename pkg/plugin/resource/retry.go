// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource

import "time"

// DefaultMaxBackoff caps a single exponential-backoff delay. It matches the
// historical cap in the plugin operator's throttling backoff.
const DefaultMaxBackoff = 30 * time.Second

// RetryStrategy is a pure, actor-agnostic description of how a recoverable
// plugin operation should be retried. It performs no I/O, no sleeping, and holds
// no actor state: callers ask it for a decision and then schedule their own
// (non-blocking) reschedule. It is the single source of truth for both the
// per-attempt backoff and the total retry budget, so a waiting actor's watchdog
// (derived from MaxTotalDelay) can never drift from the actual backoff.
type RetryStrategy struct {
	// MaxRetries is the number of retries allowed after the first attempt.
	MaxRetries int
	// BaseDelay is the delay before the first retry and the flat delay used for
	// recoverable non-throttling errors.
	BaseDelay time.Duration
	// MaxBackoff caps a single exponential backoff delay. Zero means
	// DefaultMaxBackoff.
	MaxBackoff time.Duration
}

// RetryDecision is the result of RetryStrategy.Decide.
type RetryDecision struct {
	Retry bool
	After time.Duration
}

func (s RetryStrategy) maxBackoff() time.Duration {
	if s.MaxBackoff <= 0 {
		return DefaultMaxBackoff
	}
	return s.MaxBackoff
}

// Backoff returns the delay before the retry that follows a given attempt.
// attempt is 1-based (attempt 1 = the first try just failed). It reproduces the
// historical calculateExponentialBackoff exactly: attempt <= 1 returns BaseDelay;
// otherwise BaseDelay * 2^(attempt-1), capped at MaxBackoff.
func (s RetryStrategy) Backoff(attempt int) time.Duration {
	if attempt <= 1 {
		return s.BaseDelay
	}
	shift := attempt - 1
	// Guard against overflow of the shift and of the multiplication.
	if shift >= 62 {
		return s.maxBackoff()
	}
	backoff := s.BaseDelay * time.Duration(int64(1)<<uint(shift))
	if backoff <= 0 || backoff > s.maxBackoff() {
		return s.maxBackoff()
	}
	return backoff
}

// Decide reports whether an operation that has just failed on its `attempt`-th
// try (1-based) with error code `code` should retry, and after how long.
// Throttling uses exponential Backoff; other recoverable codes use the flat
// BaseDelay. It gives up on a non-recoverable code or once attempt reaches
// MaxRetries.
func (s RetryStrategy) Decide(attempt int, code OperationErrorCode) RetryDecision {
	if !IsRecoverable(code) || attempt > s.MaxRetries {
		return RetryDecision{Retry: false}
	}
	if code == OperationErrorCodeThrottling {
		return RetryDecision{Retry: true, After: s.Backoff(attempt)}
	}
	return RetryDecision{Retry: true, After: s.BaseDelay}
}

// MaxTotalDelay is the worst-case total time spent backing off across all
// retries (the exponential/throttling path, which dominates the flat path). A
// watchdog that waits on a retrying operation must cover at least this budget on
// top of the per-attempt operation timeouts, or it will fire mid-retry.
func (s RetryStrategy) MaxTotalDelay() time.Duration {
	var total time.Duration
	for attempt := 1; attempt <= s.MaxRetries; attempt++ {
		total += s.Backoff(attempt)
	}
	return total
}
