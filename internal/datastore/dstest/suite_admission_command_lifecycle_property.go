// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// RunAdmissionCommandLifecycleProperty generates orderings and literal values
// across the lifecycle fields read by planning. Each generated case owns and
// closes its datastore so PostgreSQL pools and databases cannot accumulate.
func RunAdmissionCommandLifecycleProperty(t *testing.T, newFixture func(AdmissionLifecycleTestingT) AdmissionCommandLifecycleFixture) {
	rapid.Check(t, func(rt *rapid.T) {
		before := rapid.StringMatching(`[a-z]{1,8}`).Draw(rt, "before")
		after := rapid.StringMatching(`[a-z]{1,8}`).Filter(func(v string) bool { return v != before }).Draw(rt, "after")
		order := rapid.Permutation([]string{
			"modified_ts_only", "state_and_terminal_restore", "planning_metadata", "metadata_removal",
			"rollback", "unrelated_stack", "read_membership_and_observation",
		}).Draw(rt, "operation_order")

		f := newFixture(rt)
		rt.Cleanup(func() { require.NoError(rt, f.CloseForTest()) })
		_, err := f.CreateTarget(&pkgmodel.Target{Label: "default-target", Namespace: "AWS", Config: json.RawMessage(`{"region":"baseline"}`)})
		require.NoError(rt, err)
		for _, operation := range order {
			switch operation {
			case "modified_ts_only":
				propertyModifiedTimestampOnly(rt, f, before)
			case "state_and_terminal_restore":
				propertyStateAndTerminalRestore(rt, f, before, after)
			case "planning_metadata":
				propertyPlanningMetadata(rt, f, before)
			case "metadata_removal":
				propertyMetadataRemoval(rt, f, before)
			case "rollback":
				propertyRollback(rt, f, before)
			case "unrelated_stack":
				propertyUnrelatedStack(rt, f, before)
			case "read_membership_and_observation":
				propertyReadMembershipAndObservation(rt, f, before)
			default:
				rt.Fatalf("unknown lifecycle operation %q", operation)
			}
		}
	})
}

func propertyModifiedTimestampOnly(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, value string) {
	stack, command := lifecyclePropertyFixture(t, f, "modified", value, forma_command.CommandStateInProgress)
	guards := lifecycleGuards(t, f, stack)
	require.NoError(t, f.UpdateFormaCommandProgress(command.ID, command.State, command.ModifiedTs.Add(time.Second)))
	assertExtractedLifecycleValue(t, f.Datastore, stack.Label, fmt.Sprintf(`{"value":%q}`, value))
	assertLifecycleAdmission(t, f, guards, lifecycleCandidate(), lifecycleMayConservativelyStale)
}

func propertyStateAndTerminalRestore(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, before, after string) {
	stack := lifecyclePropertyStack(t, f, "terminal")
	resourceID := mksuid.New().String()
	seed := lifecyclePropertyCommand(stack, resourceID, before, forma_command.CommandStateSuccess)
	require.NoError(t, f.StoreFormaCommand(seed, seed.ID))
	finishing := lifecyclePropertyCommand(stack, resourceID, after, forma_command.CommandStatePending)
	require.NoError(t, f.StoreFormaCommand(finishing, finishing.ID))
	guards := lifecycleGuards(t, f, stack)

	require.NoError(t, f.UpdateFormaCommandProgress(finishing.ID, forma_command.CommandStateInProgress, finishing.ModifiedTs.Add(time.Second)))
	assertExtractedLifecycleValue(t, f.Datastore, stack.Label, fmt.Sprintf(`{"value":%q}`, before))
	assertLifecycleAdmission(t, f, guards, lifecycleCandidate(), lifecycleMustStale)

	guards = lifecycleGuards(t, f, stack)
	finishing.State = forma_command.CommandStateSuccess
	finishing.ModifiedTs = finishing.ModifiedTs.Add(2 * time.Second)
	require.NoError(t, f.StoreFormaCommand(finishing, finishing.ID))
	assertExtractedLifecycleValue(t, f.Datastore, stack.Label, fmt.Sprintf(`{"value":%q}`, after))
	assertLifecycleAdmission(t, f, guards, lifecycleCandidate(), lifecycleMustStale)
}

func propertyPlanningMetadata(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, value string) {
	stack, command := lifecyclePropertyFixture(t, f, "metadata", value, forma_command.CommandStateInProgress)
	for _, mutation := range []struct {
		column string
		value  any
	}{
		{column: "source", value: string(forma_command.SourceAutoReconciler)},
		{column: "config_mode", value: string(pkgmodel.FormaApplyModePatch)},
		{column: "timestamp", value: command.StartTs.Add(time.Second)},
		{column: "setup_metadata", value: `{"Version":2}`},
	} {
		guards := lifecycleGuards(t, f, stack)
		tx, err := f.AdmissionStore.Begin()
		require.NoError(t, err)
		require.NoError(t, tx.Exec("UPDATE forma_commands SET "+mutation.column+"=? WHERE command_id=?", mutation.value, command.ID))
		require.NoError(t, tx.Commit())
		assertExtractedLifecycleValue(t, f.Datastore, stack.Label, fmt.Sprintf(`{"value":%q}`, value))
		assertLifecycleAdmission(t, f, guards, lifecycleCandidate(), lifecycleMustStale)
	}
}

func propertyMetadataRemoval(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, value string) {
	stack, command := lifecyclePropertyFixture(t, f, "removal", value, forma_command.CommandStateInProgress)
	updates, err := json.Marshal([]target_update.TargetUpdate{{
		Target:    pkgmodel.Target{Label: "metadata-target", Namespace: "AWS", Config: json.RawMessage(`{"region":"literal"}`)},
		Operation: target_update.TargetOperationUpdate, State: target_update.TargetUpdateStateSuccess,
	}})
	require.NoError(t, err)
	require.NoError(t, f.UpdateFormaCommandTargetUpdates(command.ID, updates, command.State, command.ModifiedTs.Add(time.Second)))
	for _, payload := range []string{"[]", "null"} {
		guards := lifecycleGuards(t, f, stack)
		tx, beginErr := f.AdmissionStore.Begin()
		require.NoError(t, beginErr)
		require.NoError(t, tx.Exec("UPDATE forma_commands SET target_updates=? WHERE command_id=?", payload, command.ID))
		require.NoError(t, tx.Commit())
		assertLifecycleAdmission(t, f, guards, lifecycleCandidate(), lifecycleMustStale)
	}
}

func propertyRollback(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, value string) {
	stack, command := lifecyclePropertyFixture(t, f, "rollback", value, forma_command.CommandStateInProgress)
	guards := lifecycleGuards(t, f, stack)
	tx, err := f.AdmissionStore.Begin()
	require.NoError(t, err)
	require.NoError(t, tx.Exec("UPDATE forma_commands SET source=? WHERE command_id=?", string(forma_command.SourceDiscovery), command.ID))
	require.NoError(t, tx.Rollback())
	assertLifecycleAdmission(t, f, guards, lifecycleCandidate(), lifecycleMustAccept)
}

func propertyUnrelatedStack(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, value string) {
	stack, _ := lifecyclePropertyFixture(t, f, "related", value, forma_command.CommandStateInProgress)
	guards := lifecycleStackGuard(t, f, stack)
	other, otherCommand := lifecyclePropertyFixture(t, f, "unrelated", value+"-other", forma_command.CommandStateInProgress)
	require.NotEqual(t, stack.ID, other.ID)
	require.NoError(t, f.UpdateFormaCommandProgress(otherCommand.ID, forma_command.CommandStatePending, otherCommand.ModifiedTs.Add(time.Second)))
	assertLifecycleAdmission(t, f, guards, lifecycleCandidate(), lifecycleMustAccept)
}

func lifecycleStackGuard(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, stack *pkgmodel.Stack) []datastore.RevisionGuard {
	guards, err := f.Datastore.(datastore.CommandAdmitter).ReadAdmissionRevisions([]string{datastore.AdmissionStackGuardKey(stack.ID)})
	require.NoError(t, err)
	return guards
}

func propertyReadMembershipAndObservation(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, value string) {
	stack := lifecyclePropertyStack(t, f, "read")
	guards := lifecycleGuards(t, f, stack)
	read := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
	read.Command = pkgmodel.CommandSync
	read.Source = forma_command.SourceSynchronizer
	read.Stacks = []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}}
	require.NoError(t, f.StoreFormaCommand(read, read.ID))
	require.NoError(t, f.UpdateFormaCommandProgress(read.ID, forma_command.CommandStateSuccess, read.ModifiedTs.Add(time.Second)))
	assertLifecycleAdmission(t, f, guards, lifecycleCandidate(), lifecycleMustAccept)

	guards = lifecycleGuards(t, f, stack)
	resource := pkgmodel.Resource{Ksuid: mksuid.New().String(), Stack: stack.Label, Label: "observed", Type: "AWS::S3::Bucket", Target: "default-target", Properties: json.RawMessage(fmt.Sprintf(`{"value":%q}`, value)), Managed: true}
	_, err := f.StoreResource(&resource, read.ID)
	require.NoError(t, err)
	assertLifecycleAdmission(t, f, guards, lifecycleCandidate(), lifecycleMustStale)
}

func lifecyclePropertyFixture(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, prefix, value string, state forma_command.CommandState) (*pkgmodel.Stack, *forma_command.FormaCommand) {
	stack := lifecyclePropertyStack(t, f, prefix)
	resourceID := mksuid.New().String()
	if state != forma_command.CommandStateSuccess && state != forma_command.CommandStateFailed {
		seed := lifecyclePropertyCommand(stack, resourceID, value, forma_command.CommandStateSuccess)
		seed.StartTs = seed.StartTs.Add(-time.Minute)
		seed.ModifiedTs = seed.ModifiedTs.Add(-time.Minute)
		require.NoError(t, f.StoreFormaCommand(seed, seed.ID))
	}
	command := lifecyclePropertyCommand(stack, resourceID, value, state)
	require.NoError(t, f.StoreFormaCommand(command, command.ID))
	return stack, command
}

func lifecyclePropertyStack(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, prefix string) *pkgmodel.Stack {
	stack := &pkgmodel.Stack{Label: prefix + "-" + mksuid.New().String()}
	_, err := f.CreateStack(stack, "lifecycle-property")
	require.NoError(t, err)
	return stack
}

func lifecyclePropertyCommand(stack *pkgmodel.Stack, resourceID, value string, state forma_command.CommandState) *forma_command.FormaCommand {
	command := lifecycleCommand(stack.Label, resourceID, fmt.Sprintf(`{"value":%q}`, value), state, -time.Minute)
	command.Stacks = []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}}
	return command
}

func lifecycleGuards(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, stack *pkgmodel.Stack) []datastore.RevisionGuard {
	keys := []string{datastore.AdmissionStackGuardKey(stack.ID), datastore.AdmissionTargetGuard, datastore.AdmissionTopologyGuard, datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard}
	guards, err := f.Datastore.(datastore.CommandAdmitter).ReadAdmissionRevisions(keys)
	require.NoError(t, err)
	return guards
}

type lifecycleAdmissionExpectation int

const (
	lifecycleMustAccept lifecycleAdmissionExpectation = iota
	lifecycleMustStale
	lifecycleMayConservativelyStale
)

func assertLifecycleAdmission(t AdmissionLifecycleTestingT, f AdmissionCommandLifecycleFixture, guards []datastore.RevisionGuard, candidate *forma_command.FormaCommand, expectation lifecycleAdmissionExpectation) {
	admitter := f.Datastore.(datastore.CommandAdmitter)
	request := datastore.CommandAdmission{Guards: guards, PrincipalScope: "lifecycle-property", IdempotencyKey: candidate.ID, RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{"property":true}`)}
	_, err := admitter.AdmitFormaCommand(candidate, request)
	switch expectation {
	case lifecycleMustAccept:
		require.NoError(t, err)
	case lifecycleMustStale:
		require.ErrorIs(t, err, datastore.ErrStaleAdmission)
	case lifecycleMayConservativelyStale:
		if err != nil {
			require.ErrorIs(t, err, datastore.ErrStaleAdmission)
		}
	default:
		t.Errorf("unknown admission expectation %d", expectation)
	}
	receipt, lookupErr := admitter.LookupCommandAdmission(request.PrincipalScope, request.IdempotencyKey)
	require.NoError(t, lookupErr)
	if err == nil {
		require.NotNil(t, receipt)
	} else {
		require.Nil(t, receipt)
		_, loadErr := f.GetFormaCommandByCommandID(candidate.ID)
		require.Error(t, loadErr)
	}
	// Every generated case is non-vacuous: it reaches a durable admission or a
	// concrete stale rejection, and uses at least one real revision guard.
	require.NotEmpty(t, guards)
}

func lifecycleCandidate() *forma_command.FormaCommand {
	return reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, time.Minute, []resource_update.ResourceUpdate{
		resourceUpdate("candidate-"+mksuid.New().String(), mksuid.New().String(), "candidate", `{"candidate":true}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
	})
}
