// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// A sync cycle plans its reads against a snapshot of the records and can
// execute them minutes later. This drives that window through the real
// synchronizer: the resource is replaced while the queued read is in flight, so
// the read probes an identity the record has already moved past and truthfully
// reports NotFound. The record must survive, because the resource behind it is
// live under its new identity.
func TestSynchronizer_StaleReadDoesNotDeleteReplacedResource(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const (
			stackLabel = "stale-read-stack"
			plannedID  = "arn:task-definition/app:4"
			replacedID = "arn:task-definition/app:5"
		)

		var (
			ds datastore.Datastore
			// armed keeps the replacement out of the apply's own reads: it is
			// set only once the apply has settled, so the window is exercised
			// against a read the sync planned.
			armed       atomic.Bool
			replaceOnce sync.Once
			replaced    = make(chan struct{})
		)

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "create-1",
						NativeID:        plannedID,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				if !armed.Load() || request.NativeID != plannedID {
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"memory":"512"}`,
					}, nil
				}

				// The sync planned this read against the record as it stood.
				// Land the replacement now, so the answer below concerns an
				// identity the record has already moved past — the production
				// sequence, made deterministic.
				replaceOnce.Do(func() {
					defer close(replaced)

					stored, err := ds.LoadResourcesByStack(stackLabel)
					if err != nil || len(stored) != 1 {
						return
					}
					replacement := *stored[0]
					replacement.NativeID = replacedID
					replacement.Properties = json.RawMessage(`{"memory":"1024"}`)
					_, _ = ds.StoreResource(&replacement, "cmd-simulated-apply")
				})

				// The old revision has been deregistered.
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					ErrorCode:    resource.OperationErrorCodeNotFound,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)
		ds = m.Datastore

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stackLabel}},
			Resources: []pkgmodel.Resource{
				{
					Label:      "task-def",
					Type:       "FakeAWS::Resource",
					Properties: json.RawMessage(`{"memory":"512"}`),
					Schema:     pkgmodel.Schema{Fields: []string{"memory"}},
					Stack:      stackLabel,
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target"}},
		}

		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test", "", "")
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			stored, err := ds.LoadResourcesByStack(stackLabel)
			return err == nil && len(stored) == 1 && stored[0].NativeID == plannedID
		}, 15*time.Second, 100*time.Millisecond, "the apply should leave one record at the planned identity")

		armed.Store(true)
		require.NoError(t, m.ForceSync())

		select {
		case <-replaced:
		case <-time.After(15 * time.Second):
			t.Fatal("sync never read the planned identity, so the stale-read window was never exercised")
		}

		// The read has answered NotFound, so any delete it produces lands
		// within this window. The record must never disappear.
		require.Never(t, func() bool {
			stored, err := ds.LoadResourcesByStack(stackLabel)
			return err == nil && len(stored) == 0
		}, 5*time.Second, 100*time.Millisecond,
			"the record must survive a stale NotFound read of a superseded identity")

		stored, err := ds.LoadResourcesByStack(stackLabel)
		require.NoError(t, err)
		require.Len(t, stored, 1, "exactly one record must remain")
		require.Equal(t, replacedID, stored[0].NativeID, "the surviving record must be the replacement")
	})
}

// Registration excludes resources from future sync batches. It does not cancel
// a read already executing when the changeset executor registers an update.
func TestSynchronizer_StaleSuccessfulReadDoesNotOverwriteApply(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const stackLabel = "stale-success-stack"
		logCapture := test_helpers.SetupTestLogger()
		var armed atomic.Bool
		var updated atomic.Bool
		started := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "create-1",
					NativeID:        "resource-1",
				}}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				updated.Store(true)
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationUpdate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "update-1",
					NativeID:        "resource-1",
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				if armed.CompareAndSwap(true, false) {
					close(started)
					<-release
					return &resource.ReadResult{ResourceType: request.ResourceType, Properties: `{"memory":"768"}`}, nil
				}
				properties := `{"memory":"512"}`
				if updated.Load() {
					properties = `{"memory":"1024"}`
				}
				return &resource.ReadResult{ResourceType: request.ResourceType, Properties: properties}, nil
			},
		}
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		defer unblock()
		require.NoError(t, err)
		waitCommand := func(id string) {
			t.Helper()
			require.Eventually(t, func() bool {
				commands, err := m.Datastore.LoadFormaCommands()
				if err != nil {
					return false
				}
				for _, cmd := range commands {
					if cmd.ID == id {
						return cmd.State == forma_command.CommandStateSuccess
					}
				}
				return false
			}, 15*time.Second, 100*time.Millisecond, "command %s should succeed", id)
		}
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stackLabel}},
			Resources: []pkgmodel.Resource{{
				Label:      "resource",
				Type:       "FakeAWS::Resource",
				Stack:      stackLabel,
				Target:     "test-target",
				Properties: json.RawMessage(`{"memory":"512"}`),
				Schema:     pkgmodel.Schema{Fields: []string{"memory"}},
			}},
			Targets: []pkgmodel.Target{{Label: "test-target"}},
		}
		initial, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test", "", "")
		require.NoError(t, err)
		waitCommand(initial.CommandID)
		armed.Store(true)
		require.NoError(t, m.ForceSync())
		select {
		case <-started:
		case <-time.After(15 * time.Second):
			t.Fatal("sync read did not start")
		}
		// This is a real update through the changeset executor and its registration.
		f.Resources[0].Properties = json.RawMessage(`{"memory":"1024"}`)
		apply, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test", "", "")
		require.NoError(t, err)
		waitCommand(apply.CommandID)
		require.True(t, updated.Load(), "the plugin update must run while the sync read is blocked")
		unblock()
		// Unchanged sync commands are discarded; wait for the synchronizer's
		// completion signal instead of expecting a command-history entry.
		require.Eventually(t, func() bool {
			return logCapture.ContainsAll("Synchronization finished")
		}, 15*time.Second, 100*time.Millisecond, "sync should finish after its read is released")

		stored, err := m.Datastore.LoadResourcesByStack(stackLabel)
		require.NoError(t, err)
		require.Len(t, stored, 1)
		require.JSONEq(t, `{"memory":"1024"}`, string(stored[0].Properties))
	})
}
