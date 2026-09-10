//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// Physical emptiness must not erase a failed declaration which never reached inventory.
func TestEmptyTTLRetirementPreservesDesiredIntent(t *testing.T) {
	for _, scenario := range []string{"failed-create", "generator", "settled", "stale-incarnation"} {
		t.Run(scenario, func(t *testing.T) {
			desired := scenario != "settled"
			ds := newSQLiteTestDatastore(t)
			_, err := ds.CreateStack(&pkgmodel.Stack{Label: "retirement"}, "setup")
			require.NoError(t, err)
			stack, err := ds.GetStackByLabel("retirement")
			require.NoError(t, err)
			if scenario == "failed-create" {
				storeDesired(t, ds, pkgmodel.Resource{Ksuid: "never-created", Stack: stack.Label, Label: "pending", Type: "Test::Resource", Target: "t", Managed: true, Properties: []byte(`{"name":"pending"}`)}, resource_update.OperationCreate, forma_command.CommandStateFailed)
			}
			if scenario == "generator" {
				_, err = ds.CreateGenerator(&pkgmodel.PasswordGenerator{Label: "password", StackID: stack.ID}, "setup")
				require.NoError(t, err)
			}
			if scenario == "stale-incarnation" {
				_, err = ds.DeleteStack(stack.Label, "explicit")
				require.NoError(t, err)
				_, err = ds.CreateStack(&pkgmodel.Stack{Label: stack.Label}, "recreated")
				require.NoError(t, err)
			}
			result, err := prepareDestroyExpiredStack(ds, datastore.ExpiredStackInfo{StackID: stack.ID, StackLabel: stack.Label}, "expiry", "cleanup")
			require.NoError(t, err)
			require.Nil(t, result)
			current, err := ds.GetStackByLabel(stack.Label)
			require.NoError(t, err)
			if desired {
				require.NotNil(t, current, "failed-create ownership must survive empty TTL")
				baseline, err := ds.GetResourcesAtLastReconcile(stack.Label)
				require.NoError(t, err)
				if scenario == "failed-create" {
					require.Len(t, baseline, 1)
				}
			} else {
				require.Nil(t, current)
			}
		})
	}
}

func TestAtomicStackRetirement(t *testing.T) {
	for _, scenario := range []string{"empty", "pending-command", "failed-create", "generator", "stale-incarnation", "nonterminal-cleanup", "wrong-cleanup-membership", "terminal-cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			ds := newSQLiteTestDatastore(t)
			_, err := ds.CreateStack(&pkgmodel.Stack{Label: "retirement"}, "setup")
			require.NoError(t, err)
			stack, err := ds.GetStackByLabel("retirement")
			require.NoError(t, err)
			expected := stack.ID
			cleanup := ""
			switch scenario {
			case "pending-command", "failed-create":
				state := forma_command.CommandStateInProgress
				if scenario == "failed-create" {
					state = forma_command.CommandStateFailed
				}
				storeDesired(t, ds, pkgmodel.Resource{Ksuid: "never-created", Stack: stack.Label, Label: "pending", Type: "Test::Resource", Target: "t", Managed: true, Properties: []byte(`{}`)}, resource_update.OperationCreate, state)
			case "generator":
				_, err = ds.CreateGenerator(&pkgmodel.PasswordGenerator{Label: "password", StackID: stack.ID}, "setup")
				require.NoError(t, err)
			case "stale-incarnation":
				_, err = ds.DeleteStack(stack.Label, "explicit")
				require.NoError(t, err)
				_, err = ds.CreateStack(&pkgmodel.Stack{Label: stack.Label}, "recreate")
				require.NoError(t, err)
			case "nonterminal-cleanup", "wrong-cleanup-membership", "terminal-cleanup":
				state := forma_command.CommandStateSuccess
				if scenario == "nonterminal-cleanup" {
					state = forma_command.CommandStateInProgress
				}
				id := stack.ID
				if scenario == "wrong-cleanup-membership" {
					id = "old-incarnation"
				}
				cleanup = "cleanup"
				c := &forma_command.FormaCommand{ID: cleanup, Command: pkgmodel.CommandDestroy, Source: forma_command.SourceUser, State: state, Stacks: []forma_command.CommandStack{{ID: id, Label: stack.Label}}}
				require.NoError(t, ds.StoreFormaCommand(c, c.ID))
			}
			retire, ok := ds.(interface {
				TryRetireEmptyStack(string, string, string) (bool, error)
			})
			require.True(t, ok, "datastore must atomically retire an expected incarnation")
			retired, err := retire.TryRetireEmptyStack(expected, stack.Label, cleanup)
			require.NoError(t, err)
			require.Equal(t, scenario == "empty" || scenario == "terminal-cleanup", retired)
			current, err := ds.GetStackByLabel(stack.Label)
			require.NoError(t, err)
			if retired {
				require.Nil(t, current)
			} else {
				require.NotNil(t, current)
			}
		})
	}
}
