// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_local

import (
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watchdogMarginFloor is the fixed margin the agent adds on top of the
// operator's own cadence when it sizes the plugin-operator watchdog. It is only
// one term of that window, so the window is always longer than this — which
// makes it a safe lower bound for a test to reason against without restating
// the agent's arithmetic.
const watchdogMarginFloor = 10 * time.Second

// findResourceUpdateByLabel returns the stored resource update for the resource
// labelled label in command commandID, and whether it was found.
func findResourceUpdateByLabel(t *testing.T, ds datastore.Datastore, commandID string, label string) (resource_update.ResourceUpdate, bool) {
	t.Helper()
	commands, err := ds.LoadFormaCommands()
	require.NoError(t, err, "loading the stored forma commands must not fail")
	for _, cmd := range commands {
		if cmd.ID != commandID {
			continue
		}
		for _, ru := range cmd.ResourceUpdates {
			if ru.DesiredState.Label == label {
				return ru, true
			}
		}
	}
	return resource_update.ResourceUpdate{}, false
}

// TestSlowHeartbeatIsNotDeclaredMissingInAction is the regression test for a
// healthy plugin killed by the watchdog. The plugin reports progress on a
// cadence the agent itself set — a status check every StatusCheckInterval — but
// one provider call is slow, so the gap between two progress reports is
// statusCheckInterval + slowStatusCall.
//
// That gap deliberately sits between the two candidate windows: it exceeds
// twice the status-check interval, which is what the watchdog used to allow, and
// it stays well below the window the agent now derives from the operator's
// cadence, whose fixed margin alone outlasts the gap. So under the old rule the
// create is failed mid-flight with a missing-in-action error while the plugin is
// still working; under the derived window it runs to completion.
//
// The slow call finishes well inside the deadline the agent hands the operator,
// so what is under test is a provider call that was slow and then reported, not
// one that outran its deadline.
func TestSlowHeartbeatIsNotDeclaredMissingInAction(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const (
			statusCheckInterval = 1 * time.Second
			pluginCallTimeout   = 5 * time.Second
			slowStatusCall      = 2500 * time.Millisecond
		)

		heartbeatGap := statusCheckInterval + slowStatusCall
		require.Greater(t, heartbeatGap, 2*statusCheckInterval,
			"the gap must outlast the flat twice-the-interval window, or the old rule would not have fired")
		require.Less(t, heartbeatGap, watchdogMarginFloor,
			"the gap must stay inside the derived window, of which the fixed margin is only one term")
		require.LessOrEqual(t, slowStatusCall, pluginCallTimeout/2,
			"the slow call must leave as much headroom again inside the deadline the agent hands the "+
				"operator, or a loaded runner turns this into a call that outran its deadline instead of "+
				"a slow one that reported")

		// Create parks the operator in its status-check loop. The first status
		// check is the slow provider call and still reports in progress, so it is
		// the late heartbeat the watchdog must accept; the next one finishes the
		// operation.
		var statusCalls atomic.Int32
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusInProgress,
						RequestID:       "request-" + req.Label,
						NativeID:        "native-" + req.Label,
					},
				}, nil
			},
			Status: func(req *resource.StatusRequest) (*resource.StatusResult, error) {
				if statusCalls.Add(1) == 1 {
					time.Sleep(slowStatusCall)
					return &resource.StatusResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusInProgress,
							RequestID:       req.RequestID,
							NativeID:        req.NativeID,
						},
					}, nil
				}
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          req.RequestID,
						NativeID:           req.NativeID,
						ResourceProperties: json.RawMessage(`{"BucketName":"slow-heartbeat-bucket"}`),
					},
				}, nil
			},
		}

		origCallTimeout := resource_update.PluginCallTimeout
		resource_update.PluginCallTimeout = pluginCallTimeout
		t.Cleanup(func() { resource_update.PluginCallTimeout = origCallTimeout })

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Retry.StatusCheckInterval = statusCheckInterval
		cfg.Agent.Retry.RetryDelay = 100 * time.Millisecond

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "slow-heartbeat"}},
			Resources: []pkgmodel.Resource{
				{
					Label:      "bucket",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"BucketName":"slow-heartbeat-bucket"}`),
					Stack:      "slow-heartbeat",
					Target:     "test-target",
					Managed:    true,
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target"}},
		}

		resp, err := m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")
		require.NoError(t, err)

		// A failed resource update is terminal, so reaching Success is proof the
		// watchdog never fired on the late heartbeat.
		assert.Eventually(t, func() bool {
			update, found := findResourceUpdateByLabel(t, m.Datastore, resp.CommandID, "bucket")
			return found && update.State == resource_update.ResourceUpdateStateSuccess
		}, 20*time.Second, 100*time.Millisecond,
			"a plugin heartbeating inside the derived watchdog window must run to completion")

		assert.GreaterOrEqual(t, statusCalls.Load(), int32(2),
			"the slow status check must have been followed by another one")
	})
}

// TestFailedStatusCheckReportsThePluginError asserts a status check that fails
// with a plugin error is reported to the watching resource updater before the
// operator terminates: the resource update fails carrying the plugin's message,
// and it does so far sooner than the watchdog could have fired. An operator that
// terminated without reporting would leave the update in progress until the
// watchdog fired, and that failure carries no message at all.
func TestFailedStatusCheckReportsThePluginError(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const (
			pluginErr           = "simulated provider failure on StatusCheck"
			statusCheckInterval = 200 * time.Millisecond
			// Reaching a terminal failure inside this deadline rules out the
			// watchdog, whose window outlasts its fixed margin alone.
			reportDeadline = 5 * time.Second
		)

		require.Less(t, reportDeadline, watchdogMarginFloor,
			"the deadline must be short enough that the watchdog cannot be what failed the update")

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusInProgress,
						RequestID:       "request-" + req.Label,
						NativeID:        "native-" + req.Label,
					},
				}, nil
			},
			Status: func(req *resource.StatusRequest) (*resource.StatusResult, error) {
				return nil, errors.New(pluginErr)
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Retry.StatusCheckInterval = statusCheckInterval
		cfg.Agent.Retry.RetryDelay = 100 * time.Millisecond

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "failing-status"}},
			Resources: []pkgmodel.Resource{
				{
					Label:      "bucket",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"BucketName":"failing-status-bucket"}`),
					Stack:      "failing-status",
					Target:     "test-target",
					Managed:    true,
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target"}},
		}

		resp, err := m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			update, found := findResourceUpdateByLabel(t, m.Datastore, resp.CommandID, "bucket")
			return found && update.State == resource_update.ResourceUpdateStateFailed
		}, reportDeadline, 100*time.Millisecond,
			"a failing status check must fail the resource update before the watchdog window elapses")

		update, found := findResourceUpdateByLabel(t, m.Datastore, resp.CommandID, "bucket")
		require.True(t, found)
		assert.Contains(t, update.MostRecentFailureMessage(), pluginErr,
			"the failing status check's plugin error must reach the resource update")
	})
}
