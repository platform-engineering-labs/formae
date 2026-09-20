// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

const commandUpdateAdmissionTrigger = "admission_forma_commands_update"

// AdmissionCommandLifecycleFixture exposes only test instrumentation around a
// real datastore. Trigger changes and event logging live in its disposable DB.
type AdmissionCommandLifecycleFixture struct {
	datastore.Datastore
	Backend                         string
	AdmissionTriggerExistsForTest   func(string) (bool, error)
	DropCommandUpdateTriggerForTest func() error
	ResetWriterEventsForTest        func() error
	WriterEventsForTest             func() ([]string, error)
}

// RunAdmissionCommandLifecycle characterizes the final resource/command gap
// through every public command writer. The stale-admission assertion is the
// safety oracle; revision deltas and event logs only identify which trigger
// paths supplied that protection.
func RunAdmissionCommandLifecycle(t *testing.T, newFixture func(*testing.T) AdmissionCommandLifecycleFixture) {
	paths := []struct {
		name  string
		write func(*testing.T, AdmissionCommandLifecycleFixture, *forma_command.FormaCommand)
	}{
		{
			name: "progress_update",
			write: func(t *testing.T, f AdmissionCommandLifecycleFixture, command *forma_command.FormaCommand) {
				require.NoError(t, f.UpdateFormaCommandProgress(command.ID, forma_command.CommandStateSuccess, command.ModifiedTs.Add(time.Second)))
			},
		},
		{
			name: "target_metadata_update",
			write: func(t *testing.T, f AdmissionCommandLifecycleFixture, command *forma_command.FormaCommand) {
				updates, err := json.Marshal([]target_update.TargetUpdate{{
					Target:    pkgmodel.Target{Label: "metadata-target", Namespace: "AWS", Config: json.RawMessage(`{"region":"literal"}`)},
					Operation: target_update.TargetOperationUpdate,
					State:     target_update.TargetUpdateStateSuccess,
				}})
				require.NoError(t, err)
				require.NoError(t, f.UpdateFormaCommandTargetUpdates(command.ID, updates, forma_command.CommandStateSuccess, command.ModifiedTs.Add(time.Second)))
			},
		},
		{
			name: "full_command_restore",
			write: func(t *testing.T, f AdmissionCommandLifecycleFixture, command *forma_command.FormaCommand) {
				command.State = forma_command.CommandStateSuccess
				command.ModifiedTs = command.ModifiedTs.Add(time.Second)
				require.NoError(t, f.StoreFormaCommand(command, command.ID))
			},
		},
	}

	for _, path := range paths {
		for _, dropUpdateTrigger := range []bool{false, true} {
			triggerCase := "with_command_update_trigger"
			if dropUpdateTrigger {
				triggerCase = "without_command_update_trigger"
			}
			t.Run(path.name+"/"+triggerCase, func(t *testing.T) {
				f := newFixture(t)

				for _, name := range []string{
					"admission_forma_commands_insert",
					commandUpdateAdmissionTrigger,
					"admission_resource_updates_insert",
					"admission_resource_updates_update",
				} {
					exists, err := f.AdmissionTriggerExistsForTest(name)
					require.NoError(t, err)
					require.True(t, exists, "required fixture trigger %s must exist before intervention", name)
				}
				if dropUpdateTrigger {
					require.NoError(t, f.DropCommandUpdateTriggerForTest())
					exists, err := f.AdmissionTriggerExistsForTest(commandUpdateAdmissionTrigger)
					require.NoError(t, err)
					require.False(t, exists, "counterfactual must remove exactly the command UPDATE trigger")
					for _, name := range []string{"admission_forma_commands_insert", "admission_resource_updates_insert", "admission_resource_updates_update"} {
						exists, err = f.AdmissionTriggerExistsForTest(name)
						require.NoError(t, err)
						require.True(t, exists, "counterfactual must preserve %s", name)
					}
				}

				stack := &pkgmodel.Stack{Label: "lifecycle-" + mksuid.New().String()}
				_, err := f.CreateStack(stack, "fixture")
				require.NoError(t, err)
				_, err = f.CreateTarget(&pkgmodel.Target{Label: "default-target", Namespace: "AWS", Config: json.RawMessage(`{"region":"baseline"}`)})
				require.NoError(t, err)

				resourceID := mksuid.New().String()
				seed := lifecycleCommand(stack.Label, resourceID, `{"value":"before"}`, forma_command.CommandStateSuccess, -2*time.Minute)
				require.NoError(t, f.StoreFormaCommand(seed, seed.ID))
				finishing := lifecycleCommand(stack.Label, resourceID, `{"value":"after"}`, forma_command.CommandStateInProgress, -time.Minute)
				require.NoError(t, f.StoreFormaCommand(finishing, finishing.ID))

				assertExtractedLifecycleValue(t, f.Datastore, stack.Label, `{"value":"before"}`)
				guardKey := datastore.AdmissionStackGuardKey(stack.ID)
				admitter := f.Datastore.(datastore.CommandAdmitter)
				guards, err := admitter.ReadAdmissionRevisions([]string{guardKey})
				require.NoError(t, err)
				require.Len(t, guards, 1)
				require.NoError(t, f.ResetWriterEventsForTest())

				path.write(t, f, finishing)

				stored, err := f.GetFormaCommandByCommandID(finishing.ID)
				require.NoError(t, err)
				require.Equal(t, forma_command.CommandStateSuccess, stored.State)
				require.Len(t, stored.ResourceUpdates, 1)
				require.JSONEq(t, `{"value":"after"}`, string(stored.ResourceUpdates[0].DesiredState.Properties))
				if path.name == "target_metadata_update" {
					require.Len(t, stored.TargetUpdates, 1)
					require.JSONEq(t, `{"region":"literal"}`, string(stored.TargetUpdates[0].Target.Config))
				}
				assertExtractedLifecycleValue(t, f.Datastore, stack.Label, `{"value":"after"}`)

				events, err := f.WriterEventsForTest()
				require.NoError(t, err)
				assertLifecycleWriterEvents(t, f.Backend, path.name, events)
				after, err := admitter.ReadAdmissionRevisions([]string{guardKey})
				require.NoError(t, err)
				wantContributions := int64(len(events))
				if dropUpdateTrigger && slices.Contains(events, "forma_commands:update") {
					wantContributions--
				}
				require.Equal(t, wantContributions, after[0].Revision-guards[0].Revision,
					"each preserved INSERT/resource-update trigger and the optional command UPDATE trigger contributes once to this stack identity")

				candidate := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, time.Minute, nil)
				request := datastore.CommandAdmission{
					Guards: guards, PrincipalScope: "lifecycle", IdempotencyKey: mksuid.New().String(),
					RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{"lifecycle":true}`),
				}
				_, err = admitter.AdmitFormaCommand(candidate, request)
				wantStale := !dropUpdateTrigger || path.name == "full_command_restore"
				if wantStale {
					require.ErrorIs(t, err, datastore.ErrStaleAdmission)
					receipt, lookupErr := admitter.LookupCommandAdmission(request.PrincipalScope, request.IdempotencyKey)
					require.NoError(t, lookupErr)
					require.Nil(t, receipt, "stale admission must leave no accepted receipt")
					_, loadErr := f.GetFormaCommandByCommandID(candidate.ID)
					require.Error(t, loadErr, "stale admission must leave no candidate command")
				} else {
					require.NoError(t, err, "removing the only contributing command UPDATE trigger is the counterfactual stale acceptance witness")
					receipt, lookupErr := admitter.LookupCommandAdmission(request.PrincipalScope, request.IdempotencyKey)
					require.NoError(t, lookupErr)
					require.NotNil(t, receipt)
					require.Equal(t, candidate.ID, receipt.CommandID)
					accepted, loadErr := f.GetFormaCommandByCommandID(candidate.ID)
					require.NoError(t, loadErr)
					require.Equal(t, candidate.ID, accepted.ID)
				}
			})
		}
	}
}

func lifecycleCommand(stack, resourceID, properties string, state forma_command.CommandState, offset time.Duration) *forma_command.FormaCommand {
	command := reconcileBuilder(state, pkgmodel.FormaApplyModeReconcile, offset, []resource_update.ResourceUpdate{
		resourceUpdate(stack, resourceID, "literal-resource", properties, types.OperationUpdate, resource_update.FormaCommandSourceUser),
	})
	return command
}

func assertExtractedLifecycleValue(t *testing.T, ds datastore.Datastore, stack, properties string) {
	t.Helper()
	desired, err := (&metastructure.Metastructure{Datastore: ds}).ExtractDesiredStacks("stack:" + stack)
	require.NoError(t, err)
	require.Len(t, desired.Resources, 1)
	require.Equal(t, "literal-resource", desired.Resources[0].Label)
	require.JSONEq(t, properties, string(desired.Resources[0].Properties))
}

func assertLifecycleWriterEvents(t *testing.T, backend, path string, events []string) {
	t.Helper()
	switch path {
	case "progress_update", "target_metadata_update":
		require.Equal(t, []string{"forma_commands:update"}, events)
	case "full_command_restore":
		switch backend {
		case "sqlite":
			require.Equal(t, []string{"forma_commands:insert", "resource_updates:insert"}, events,
				"SQLite INSERT OR REPLACE uses INSERT triggers; recursive DELETE triggers are disabled")
		case "postgres":
			require.Equal(t, []string{"forma_commands:insert", "forma_commands:update", "resource_updates:insert", "resource_updates:update"}, events,
				"PostgreSQL UPSERT executes BEFORE INSERT and BEFORE UPDATE triggers")
		default:
			t.Fatalf("unsupported lifecycle fixture backend %q", backend)
		}
	}
}
