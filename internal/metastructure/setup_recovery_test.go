//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestRecoveryBlocksUnknownAndIncompleteSetupBeforeActors(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "incomplete"}[known], func(t *testing.T) {
			ds := newSQLiteTestDatastore(t)
			c := &forma_command.FormaCommand{ID: util.NewID(), Command: pkgmodel.CommandApply, State: forma_command.CommandStateNotStarted, StartTs: util.TimeNow(), ModifiedTs: util.TimeNow()}
			if known {
				c.Setup = &forma_command.SetupBoundary{Version: 1}
				c.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: "old-incarnation", Label: "reused"}, Operation: stack_update.StackOperationUpdate, State: stack_update.StackUpdateStateNotStarted}}
			}
			require.NoError(t, ds.StoreFormaCommand(c, c.ID))
			_, err := ds.CreateStack(&pkgmodel.Stack{ID: "new-incarnation", Label: "reused"}, "new")
			require.NoError(t, err)
			m := &Metastructure{Datastore: ds} // No actor/provider runtime: unsafe recovery must stop before using it.
			require.NotPanics(t, func() { require.ErrorContains(t, m.ReRunIncompleteCommands(), c.ID) })
			stored, err := ds.GetFormaCommandByCommandID(c.ID)
			require.NoError(t, err)
			require.Equal(t, forma_command.CommandStateNotStarted, stored.State)
			current, err := ds.GetStackByLabel("reused")
			require.NoError(t, err)
			require.Equal(t, "new-incarnation", current.ID)
		})
	}
}

func TestPostcommitPolicyRefreshReadsCurrentIncarnation(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	stack := &pkgmodel.Stack{ID: util.NewID(), Label: "reused"}
	_, err := ds.CreateStack(stack, "old")
	require.NoError(t, err)
	_, err = ds.CreatePolicy(&pkgmodel.AutoReconcilePolicy{Type: "auto-reconcile", Label: "automatic", StackID: stack.ID, IntervalSeconds: 60}, "old")
	require.NoError(t, err)
	data := AutoReconcilerData{datastore: ds, scheduled: map[string]bool{}, activeReconciles: map[string]string{}}
	delays := []time.Duration{}
	schedule := func(label string, delay time.Duration) error {
		require.Equal(t, "reused", label)
		delays = append(delays, delay)
		return nil
	}
	require.NoError(t, refreshEffectivePolicies(&data, schedule))
	require.Equal(t, []time.Duration{time.Minute}, delays)
	require.NoError(t, refreshEffectivePolicies(&data, schedule))
	require.Len(t, delays, 1)
	_, err = ds.DeleteStack("reused", "delete")
	require.NoError(t, err)
	_, err = ds.CreateStack(&pkgmodel.Stack{ID: util.NewID(), Label: "reused"}, "new")
	require.NoError(t, err)
	require.NoError(t, refreshEffectivePolicies(&data, schedule))
	require.False(t, data.scheduled["reused"])
	require.Len(t, delays, 1, "duplicate old hints cannot resurrect an old policy on the new stack")
}
