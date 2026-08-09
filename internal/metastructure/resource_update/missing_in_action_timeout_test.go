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

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// shippedRetryConfig holds the default RetryConfig values as defined in Config.pkl.
func shippedRetryConfig() pkgmodel.RetryConfig {
	return pkgmodel.RetryConfig{
		StatusCheckInterval: 20 * time.Second,
		MaxRetries:          9,
		RetryDelay:          10 * time.Second,
	}
}

// TestMissingInActionTimeout_CoversLongestOperatorSleep asserts the watchdog
// window is the longest sleep the PluginOperator can schedule between two
// progress reports plus one plugin call plus the margin, and that on the
// shipped defaults it strictly exceeds the flat twice-the-interval window that
// killed healthy operations under provider throttling.
func TestMissingInActionTimeout_CoversLongestOperatorSleep(t *testing.T) {
	cfg := shippedRetryConfig()
	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	longestSleep := max(cfg.StatusCheckInterval, cfg.RetryDelay, strategy.Backoff(cfg.MaxRetries+1))

	assert.Equal(t, longestSleep+PluginCallTimeout+missingInActionMargin, missingInActionTimeout(cfg))
	assert.Equal(t, 100*time.Second, missingInActionTimeout(cfg),
		"the shipped defaults must yield a 100s window")
	assert.Greater(t, missingInActionTimeout(cfg), 2*cfg.StatusCheckInterval,
		"the derived window must exceed the flat twice-the-interval window")
}

// TestMissingInActionTimeout_DrivenByFlatRetryDelay covers the non-throttling
// recoverable path, which sleeps a flat RetryDelay. Backoff returns BaseDelay
// verbatim for the first attempt, so a RetryDelay above DefaultMaxBackoff is
// never clamped and must drive the window on its own.
func TestMissingInActionTimeout_DrivenByFlatRetryDelay(t *testing.T) {
	retryDelay := resource.DefaultMaxBackoff + 15*time.Second
	cfg := pkgmodel.RetryConfig{
		StatusCheckInterval: 20 * time.Second,
		MaxRetries:          5,
		RetryDelay:          retryDelay,
	}
	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	require.Greater(t, retryDelay, strategy.Backoff(cfg.MaxRetries+1),
		"this case only bites when the flat delay outlasts the capped backoff")

	assert.Equal(t, retryDelay+PluginCallTimeout+missingInActionMargin, missingInActionTimeout(cfg))
}

// TestMissingInActionTimeout_DrivenByLastScheduledBackoff covers the throttling
// path. Attempts start at 1 and a retry is scheduled while attempts is at most
// MaxRetries+1, so the largest backoff the operator can schedule is
// Backoff(MaxRetries+1).
func TestMissingInActionTimeout_DrivenByLastScheduledBackoff(t *testing.T) {
	cfg := pkgmodel.RetryConfig{
		StatusCheckInterval: 5 * time.Second,
		MaxRetries:          4,
		RetryDelay:          1 * time.Second,
	}
	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	lastBackoff := strategy.Backoff(cfg.MaxRetries + 1)
	require.Greater(t, lastBackoff, strategy.Backoff(cfg.MaxRetries),
		"the last scheduled backoff must outlast the one before it")

	assert.Equal(t, lastBackoff+PluginCallTimeout+missingInActionMargin, missingInActionTimeout(cfg))
	assert.Greater(t, missingInActionTimeout(cfg),
		strategy.Backoff(cfg.MaxRetries)+PluginCallTimeout+missingInActionMargin,
		"a window built on Backoff(MaxRetries) would fire one backoff short")
}

// TestMissingInActionTimeout_SmallAndDegenerateConfigs pins the window for the
// retry counts where the backoff ladder is one or two rungs long, and keeps it
// positive for configurations that schedule no sleep at all.
func TestMissingInActionTimeout_SmallAndDegenerateConfigs(t *testing.T) {
	t.Run("MaxRetriesZero", func(t *testing.T) {
		cfg := pkgmodel.RetryConfig{StatusCheckInterval: 2 * time.Second, MaxRetries: 0, RetryDelay: 7 * time.Second}
		// The single scheduled backoff is Backoff(1), which is the base delay.
		assert.Equal(t, 7*time.Second+PluginCallTimeout+missingInActionMargin, missingInActionTimeout(cfg))
	})

	t.Run("MaxRetriesOne", func(t *testing.T) {
		cfg := pkgmodel.RetryConfig{StatusCheckInterval: 2 * time.Second, MaxRetries: 1, RetryDelay: 7 * time.Second}
		// Backoff(2) doubles the base delay, still under DefaultMaxBackoff.
		assert.Equal(t, 14*time.Second+PluginCallTimeout+missingInActionMargin, missingInActionTimeout(cfg))
	})

	t.Run("ZeroConfig", func(t *testing.T) {
		assert.Equal(t, PluginCallTimeout+missingInActionMargin, missingInActionTimeout(pkgmodel.RetryConfig{}))
	})

	t.Run("NegativeDurations", func(t *testing.T) {
		cfg := pkgmodel.RetryConfig{StatusCheckInterval: -5 * time.Second, MaxRetries: 0, RetryDelay: -5 * time.Second}
		assert.Equal(t, PluginCallTimeout+missingInActionMargin, missingInActionTimeout(cfg),
			"a negative duration in config must not shrink the window")
	})
}

// TestPluginCallTimeouts_OperatorDeadlineExpiresFirst pins both deadlines: the
// one the agent hands the operator for a single plugin call, and the one the
// agent puts on its own call to the operator. The operator's must expire first
// so its attributable failure progress wins the race, and it must equal the
// operator's compiled defaultPluginCallTimeout fallback (pkg/plugin), which it
// stands in for whenever the deadline is not supplied.
func TestPluginCallTimeouts_OperatorDeadlineExpiresFirst(t *testing.T) {
	assert.Equal(t, 60*time.Second, PluginCallTimeout,
		"must track the plugin operator's compiled defaultPluginCallTimeout")
	assert.Equal(t, 70, PluginOperationCallTimeout,
		"the agent's call timeout is the operator's deadline plus a margin, in seconds")
}

// armingProcess is a gen.Process double for the two watchdog-arming handlers.
// It answers the synchronous calls they make: spawning a plugin operator, the
// operation call to that operator (answered with operatorProgress), and the
// progress persist.
type armingProcess struct {
	*stubUpdaterProcess

	operatorProgress plugin.TrackedProgress

	// spawnRetryConfig is the retry config the PluginCoordinator double reports
	// the spawned operator was given. Nil means it supplied none.
	spawnRetryConfig *pkgmodel.RetryConfig
}

func (p *armingProcess) Call(_ any, message any) (any, error) {
	if _, ok := message.(messages.SpawnPluginOperator); ok {
		return messages.SpawnPluginOperatorResult{
			PID:         gen.PID{Node: "test-node", ID: 2},
			RetryConfig: p.spawnRetryConfig,
		}, nil
	}
	return nil, nil
}

func (p *armingProcess) CallWithTimeout(_ any, _ any, _ int) (any, error) {
	return p.operatorProgress, nil
}

// armedMissingInActionTimeout returns the duration of the
// PluginOperatorMissingInAction timeout among the returned actions.
func armedMissingInActionTimeout(t *testing.T, actions []statemachine.Action) time.Duration {
	t.Helper()
	for _, a := range actions {
		if st, ok := a.(statemachine.StateTimeout); ok {
			if _, isMIA := st.Message.(PluginOperatorMissingInAction); isMIA {
				return st.Duration
			}
		}
	}
	require.FailNow(t, "no PluginOperatorMissingInAction timeout was armed")
	return 0
}

func inProgressCreate(cfg pkgmodel.RetryConfig) plugin.TrackedProgress {
	return plugin.TrackedProgress{
		ProgressResult: resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusInProgress,
			RequestID:       "request-1",
			NativeID:        "native-1",
		},
		Attempts:    1,
		MaxAttempts: cfg.MaxRetries + 1,
	}
}

func armingTestData(cfg pkgmodel.RetryConfig) ResourceUpdateData {
	return ResourceUpdateData{
		resourceUpdate: &ResourceUpdate{
			Operation: OperationCreate,
			DesiredState: pkgmodel.Resource{
				Label: "r",
				Type:  "FakeAWS::EC2::Subnet",
				Ksuid: "3E3wKW8YqVCQEyfKjsGpbsoE8bl",
			},
		},
		commandID:   "cmd",
		retryConfig: cfg,
	}
}

// TestHandleProgressUpdate_ArmsCadenceDerivedWatchdog drives the progress
// handler in isolation with a stub process (the ergo-unit direct-handler
// pattern) and asserts that an in-progress report arms a
// PluginOperatorMissingInAction timeout derived from the operator's cadence.
func TestHandleProgressUpdate_ArmsCadenceDerivedWatchdog(t *testing.T) {
	cfg := shippedRetryConfig()
	proc := &armingProcess{stubUpdaterProcess: &stubUpdaterProcess{}}

	state, _, actions, err := handleProgressUpdate(gen.PID{}, StateCreating, armingTestData(cfg), inProgressCreate(cfg), proc)

	require.NoError(t, err)
	assert.Equal(t, StateCreating, state, "an in-progress report keeps the state machine waiting")
	assert.Equal(t, missingInActionTimeout(cfg), armedMissingInActionTimeout(t, actions))
}

// perPluginRetryConfig is a retry config that differs from the shipped defaults
// in every term the watchdog window is derived from, standing in for a
// per-plugin retry override the operator was spawned with.
func perPluginRetryConfig() pkgmodel.RetryConfig {
	return pkgmodel.RetryConfig{
		StatusCheckInterval: 90 * time.Second,
		MaxRetries:          3,
		RetryDelay:          30 * time.Second,
	}
}

// windowSource is one case of the config the watchdog window must be sized
// from: supplied is what the operator was spawned with (nil when the
// coordinator supplied none), want is the config the window must come from.
type windowSource struct {
	name     string
	supplied *pkgmodel.RetryConfig
	want     pkgmodel.RetryConfig
}

// windowSourceCases enumerates which retry config must size the watchdog
// window: the one the spawned operator was given when the coordinator supplied
// it, the node-global one when it did not. A supplied config whose fields are
// all zero is a legitimate config, not an absent one, so it must size the
// window too rather than fall back.
func windowSourceCases(global pkgmodel.RetryConfig) []windowSource {
	perPlugin := perPluginRetryConfig()
	zero := pkgmodel.RetryConfig{}
	return []windowSource{
		{name: "operator config supplied", supplied: &perPlugin, want: perPlugin},
		{name: "no operator config supplied", supplied: nil, want: global},
		{name: "operator config supplied with zero fields", supplied: &zero, want: zero},
	}
}

// TestHandleProgressUpdate_SizesWindowFromOperatorConfig asserts the progress
// handler sizes the watchdog from the retry config the watched operator was
// spawned with, and only falls back to the node-global config when none was
// supplied.
func TestHandleProgressUpdate_SizesWindowFromOperatorConfig(t *testing.T) {
	global := shippedRetryConfig()
	require.NotEqual(t, missingInActionTimeout(global), missingInActionTimeout(perPluginRetryConfig()),
		"the per-plugin config must yield a different window than the node-global one")

	for _, tc := range windowSourceCases(global) {
		t.Run(tc.name, func(t *testing.T) {
			proc := &armingProcess{stubUpdaterProcess: &stubUpdaterProcess{}}
			data := armingTestData(global)
			data.operatorRetryConfig = tc.supplied

			_, _, actions, err := handleProgressUpdate(gen.PID{}, StateCreating, data, inProgressCreate(global), proc)

			require.NoError(t, err)
			assert.Equal(t, missingInActionTimeout(tc.want), armedMissingInActionTimeout(t, actions))
		})
	}
}

// TestRecoverFromPreviousProgress_SizesWindowFromSpawnedOperatorConfig asserts
// the recovery path carries the retry config the spawn result reported all the
// way to the armed watchdog, so the window tracks the cadence the operator
// actually polls on.
func TestRecoverFromPreviousProgress_SizesWindowFromSpawnedOperatorConfig(t *testing.T) {
	global := shippedRetryConfig()
	require.NotEqual(t, missingInActionTimeout(global), missingInActionTimeout(pkgmodel.RetryConfig{}),
		"a zero-field config must yield a different window than the node-global one, or the fallback would be invisible")

	for _, tc := range windowSourceCases(global) {
		t.Run(tc.name, func(t *testing.T) {
			lastKnownProgress := inProgressCreate(global)
			proc := &armingProcess{
				stubUpdaterProcess: &stubUpdaterProcess{},
				operatorProgress:   inProgressCreate(global),
				spawnRetryConfig:   tc.supplied,
			}
			operation := plugin.CreateResource{ResourceType: "FakeAWS::EC2::Subnet", Label: "r"}

			_, _, actions, err := recoverFromPreviousProgress(StateCreating, armingTestData(global), &lastKnownProgress, operation, proc)

			require.NoError(t, err)
			assert.Equal(t, missingInActionTimeout(tc.want), armedMissingInActionTimeout(t, actions))
		})
	}
}

// TestRecoverFromPreviousProgress_ArmsCadenceDerivedWatchdog asserts the
// recovery path arms the same cadence-derived watchdog when the resumed plugin
// operator reports the operation is still in progress.
func TestRecoverFromPreviousProgress_ArmsCadenceDerivedWatchdog(t *testing.T) {
	cfg := shippedRetryConfig()
	lastKnownProgress := inProgressCreate(cfg)
	proc := &armingProcess{
		stubUpdaterProcess: &stubUpdaterProcess{},
		operatorProgress:   inProgressCreate(cfg),
	}
	operation := plugin.CreateResource{ResourceType: "FakeAWS::EC2::Subnet", Label: "r"}

	state, _, actions, err := recoverFromPreviousProgress(StateCreating, armingTestData(cfg), &lastKnownProgress, operation, proc)

	require.NoError(t, err)
	assert.Equal(t, StateCreating, state, "an unfinished operation keeps the state machine waiting")
	assert.Equal(t, missingInActionTimeout(cfg), armedMissingInActionTimeout(t, actions))
}
