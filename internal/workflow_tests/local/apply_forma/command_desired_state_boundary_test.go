// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// A command's desired state is the input the command executes from, and while
// the command is live the row holds it as the author wrote it, opaque values in
// the clear. Only once the command is final does the same row hold a digest.
//
// The window is what makes a command resumable. An agent returning to an
// unfinished command re-reads its desired state from the row and dispatches
// what it still owes; a row hashed at write time would leave it holding a
// digest where a credential belongs, with no way to recover the value, so
// every unfinished write would have to be failed instead. The window closes
// with the command, and every other sink on the path (read-back properties,
// progress snapshots, the resource row) is hashed at its own write, so this
// column is where a live command's plaintext is at rest.
//
// A generator-bound property is not part of this window. A drawn value is
// delivered into the in-memory update and never written back to the row, which
// is why a resumed command draws again for the destinations it still owes
// rather than replaying a value it could read back.
func TestApplyForma_InFlightCommand_HoldsDesiredSecretUntilItIsFinal(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const secret = "in-flight-desired-secret"
		var released atomic.Bool

		// The secret's create reports in progress and is polled until the test
		// releases it, which parks the command in a live, unfinished state for
		// as long as the observation needs.
		parking := &plugin.ResourcePluginOverrides{
			Create: func(_ *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusInProgress,
				}}, nil
			},
			Status: func(_ *resource.StatusRequest) (*resource.StatusResult, error) {
				if !released.Load() {
					return &resource.StatusResult{ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusInProgress,
					}}, nil
				}
				// The completion reports no properties of its own, so nothing
				// merges over the desired document: what the row ends up
				// holding is what the command's own finalization leaves there.
				return &resource.StatusResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					NativeID:        "native-parked",
				}}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, parking, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Targets:   []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Resources: []pkgmodel.Resource{secretResource(stack, "parked", secret)},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)

		var commandID string
		require.Eventually(t, func() bool {
			incomplete, loadErr := m.Datastore.LoadIncompleteFormaCommands()
			if loadErr != nil || len(incomplete) != 1 {
				return false
			}
			commandID = incomplete[0].ID
			updates, loadErr := m.Datastore.LoadResourceUpdates(commandID)
			return loadErr == nil && len(updates) == 1
		}, 10*time.Second, 25*time.Millisecond,
			"the command should be live with its resource update stored")

		inFlight, err := m.Datastore.LoadResourceUpdates(commandID)
		require.NoError(t, err)
		require.Len(t, inFlight, 1)
		assert.Equal(t, secret,
			gjson.GetBytes(inFlight[0].DesiredState.Properties, "SecretString").String(),
			"a live command must keep its desired secret executable, not a digest of it")

		released.Store(true)
		waitForApplyComplete(t, m)

		final, err := m.Datastore.GetFormaCommandByCommandID(commandID)
		require.NoError(t, err)
		require.NotNil(t, final)
		require.Equal(t, forma_command.CommandStateSuccess, final.State)

		settled, err := m.Datastore.LoadResourceUpdates(commandID)
		require.NoError(t, err)
		require.Len(t, settled, 1)
		stored := gjson.GetBytes(settled[0].DesiredState.Properties, "SecretString")
		assert.True(t, stored.Get("$hashed").Bool(),
			"a final command must hold the desired secret as a digest, got %s", stored.Raw)
		assert.Equal(t, pkgmodel.ComputeValueHash(secret), stored.Get("$value").String(),
			"the digest must be the digest of the value the command executed with")
		assertNoPlaintextInResourceUpdates(t, m, commandID, secret)
	})
}
