// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetryStrategy_Backoff(t *testing.T) {
	s := RetryStrategy{MaxRetries: 10, BaseDelay: time.Second, MaxBackoff: 30 * time.Second}
	// attempt<=1 => BaseDelay; then 2^(attempt-1)*BaseDelay, capped at MaxBackoff.
	assert.Equal(t, time.Second, s.Backoff(1))
	assert.Equal(t, 2*time.Second, s.Backoff(2))
	assert.Equal(t, 4*time.Second, s.Backoff(3))
	assert.Equal(t, 8*time.Second, s.Backoff(4))
	assert.Equal(t, 16*time.Second, s.Backoff(5))
	assert.Equal(t, 30*time.Second, s.Backoff(6), "32s exceeds cap -> 30s")
	assert.Equal(t, 30*time.Second, s.Backoff(50), "large attempt saturates at cap")
}

func TestRetryStrategy_Backoff_ZeroMaxBackoffUsesDefault(t *testing.T) {
	s := RetryStrategy{MaxRetries: 10, BaseDelay: time.Second}
	assert.Equal(t, DefaultMaxBackoff, s.Backoff(10))
}

func TestRetryStrategy_Decide_NonRecoverable(t *testing.T) {
	s := RetryStrategy{MaxRetries: 5, BaseDelay: time.Second}
	d := s.Decide(1, OperationErrorCodeInvalidRequest)
	assert.False(t, d.Retry)
}

func TestRetryStrategy_Decide_ExhaustedAttempts(t *testing.T) {
	s := RetryStrategy{MaxRetries: 3, BaseDelay: time.Second}
	assert.True(t, s.Decide(3, OperationErrorCodeThrottling).Retry)
	assert.False(t, s.Decide(4, OperationErrorCodeThrottling).Retry, "attempt beyond MaxRetries gives up")
}

func TestRetryStrategy_Decide_ThrottlingIsExponential(t *testing.T) {
	s := RetryStrategy{MaxRetries: 5, BaseDelay: time.Second}
	assert.Equal(t, time.Second, s.Decide(1, OperationErrorCodeThrottling).After)
	assert.Equal(t, 2*time.Second, s.Decide(2, OperationErrorCodeThrottling).After)
	assert.Equal(t, 4*time.Second, s.Decide(3, OperationErrorCodeThrottling).After)
}

func TestRetryStrategy_Decide_OtherRecoverableIsFlat(t *testing.T) {
	s := RetryStrategy{MaxRetries: 5, BaseDelay: time.Second}
	assert.Equal(t, time.Second, s.Decide(1, OperationErrorCodeThrottling).After)
	// NetworkFailure is recoverable but not throttling -> flat BaseDelay each time.
	assert.Equal(t, time.Second, s.Decide(3, OperationErrorCodeNetworkFailure).After)
}

func TestRetryStrategy_MaxTotalDelay(t *testing.T) {
	// MaxRetries=4, base 1s: 1 + 2 + 4 + 8 = 15s.
	s := RetryStrategy{MaxRetries: 4, BaseDelay: time.Second, MaxBackoff: 30 * time.Second}
	assert.Equal(t, 15*time.Second, s.MaxTotalDelay())

	// With saturation: MaxRetries=8: 1+2+4+8+16+30+30+30 = 121s.
	s8 := RetryStrategy{MaxRetries: 8, BaseDelay: time.Second, MaxBackoff: 30 * time.Second}
	assert.Equal(t, 121*time.Second, s8.MaxTotalDelay())

	assert.Equal(t, time.Duration(0), RetryStrategy{MaxRetries: 0, BaseDelay: time.Second}.MaxTotalDelay())
}

func TestRetryStrategy_MaxTotalDelay_ExceedsFlatEstimate(t *testing.T) {
	// A resolve watchdog must be sized off MaxTotalDelay, not a flat
	// (MaxRetries-1)*BaseDelay estimate: under exponential-for-throttling backoff
	// the real budget is far larger, so a flat-sized watchdog would fire
	// mid-retry and crash the waiting actor. This guards that MaxTotalDelay
	// captures the (larger) exponential budget the watchdog derives from.
	s := RetryStrategy{MaxRetries: 8, BaseDelay: time.Second, MaxBackoff: 30 * time.Second}
	flatEstimate := time.Duration(s.MaxRetries-1) * s.BaseDelay // the old assumption: 7s
	assert.Greater(t, s.MaxTotalDelay(), flatEstimate,
		"exponential budget must exceed the flat estimate the old watchdog used")
	assert.Equal(t, 121*time.Second, s.MaxTotalDelay())
}
