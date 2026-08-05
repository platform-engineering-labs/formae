// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"testing"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestResolveWatchdogTimeout_CoversExponentialBackoff asserts the resolve
// watchdog envelope is derived from RetryStrategy.MaxTotalDelay and, for a large
// MaxRetries, strictly exceeds the old flat (MaxRetries-1)*RetryDelay estimate,
// so ResolveCache's exponential-backoff retries cannot trip the watchdog.
func TestResolveWatchdogTimeout_CoversExponentialBackoff(t *testing.T) {
	cfg := pkgmodel.RetryConfig{MaxRetries: 8, RetryDelay: 1 * time.Second}

	perAttempt := time.Duration(PluginOperationCallTimeout) * time.Second
	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	want := time.Duration(cfg.MaxRetries)*perAttempt + strategy.MaxTotalDelay() + 30*time.Second
	assert.Equal(t, want, resolveWatchdogTimeout(cfg))

	flatEstimate := time.Duration(cfg.MaxRetries)*perAttempt +
		time.Duration(cfg.MaxRetries-1)*cfg.RetryDelay + 30*time.Second
	assert.Greater(t, resolveWatchdogTimeout(cfg), flatEstimate,
		"budget-aware watchdog must exceed the old flat estimate")
}

// TestResolve_SchedulesBudgetAwareWatchdog drives the resolve state handler in
// isolation with a stub process (the ergo-unit direct-handler pattern) and
// asserts it schedules a ResolveCacheMissingInAction StateTimeout whose duration
// is the budget-aware envelope for the actor's RetryConfig.
func TestResolve_SchedulesBudgetAwareWatchdog(t *testing.T) {
	cfg := pkgmodel.RetryConfig{MaxRetries: 8, RetryDelay: 1 * time.Second}
	data := ResourceUpdateData{
		resourceUpdate: &ResourceUpdate{
			Operation:            OperationUpdate,
			DesiredState:         pkgmodel.Resource{Label: "r", Ksuid: "3E3wKW8YqVCQEyfKjsGpbsoE8bl"},
			RemainingResolvables: []pkgmodel.FormaeURI{"formae://src#/SecretString"},
		},
		commandID:   "cmd",
		retryConfig: cfg,
	}

	_, _, actions, err := resolve(gen.Atom("resolving"), data, &stubUpdaterProcess{})
	require.NoError(t, err)

	var found bool
	var dur time.Duration
	for _, a := range actions {
		if st, ok := a.(statemachine.StateTimeout); ok {
			if _, isWatchdog := st.Message.(ResolveCacheMissingInAction); isWatchdog {
				found, dur = true, st.Duration
			}
		}
	}
	require.True(t, found, "resolve must schedule a ResolveCacheMissingInAction watchdog")
	assert.Equal(t, resolveWatchdogTimeout(cfg), dur,
		"the scheduled watchdog must be the budget-aware envelope")
}
