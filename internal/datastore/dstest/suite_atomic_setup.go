//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package dstest

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func runAtomicSetup(t *testing.T, ds datastore.Datastore, fixture datastore.AdmissionStore, other datastore.CommandAdmitter) {
	a := ds.(datastore.CommandAdmitter)
	id, label := mksuid.New().String(), "atomic-"+mksuid.New().String()
	c := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
	c.Stacks = []forma_command.CommandStack{{ID: id, Label: label}}
	c.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: id, Label: label}, Operation: stack_update.StackOperationCreate}}
	c.DrawGeneratorUpdates = []generator_update.GeneratorUpdate{{Generator: &pkgmodel.PasswordGenerator{ID: "draw-id", StackID: id, Stack: label, Label: "draw-intent", Length: 24}, Operation: generator_update.GeneratorOperationDraw}}
	c.GeneratorUpdates = []generator_update.GeneratorUpdate{{Generator: &pkgmodel.PasswordGenerator{ID: mksuid.New().String(), StackID: id, Stack: label, Label: "password", Length: 24}, StackLabel: label, Operation: generator_update.GeneratorOperationCreate}}
	guards, err := a.ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
	require.NoError(t, err)
	req := datastore.CommandAdmission{PrincipalScope: label, IdempotencyKey: "one", RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{"ok":true}`), Guards: guards}
	result, err := a.AdmitFormaCommand(c, req)
	require.NoError(t, err)
	stack, err := ds.GetStackByLabel(label)
	require.NoError(t, err)
	require.NotNil(t, stack, "admission must commit declared setup")
	require.Equal(t, id, stack.ID)
	loaded, err := ds.GetFormaCommandByCommandID(c.ID)
	require.NoError(t, err)
	require.Equal(t, forma_command.CommandStateSuccess, loaded.State)
	require.Equal(t, stack_update.StackUpdateStateSuccess, loaded.StackUpdates[0].State)
	require.Len(t, loaded.GeneratorUpdates, 1)
	require.Equal(t, c.GeneratorUpdates[0].Generator.GetID(), loaded.GeneratorUpdates[0].Generator.GetID())
	require.Equal(t, id, loaded.GeneratorUpdates[0].Generator.GetStackID())
	require.NotEmpty(t, loaded.GeneratorUpdates[0].Version)
	require.Len(t, loaded.DrawGeneratorUpdates, 1)
	require.True(t, loaded.DrawIntentKnown)
	require.Equal(t, "draw-id", loaded.DrawGeneratorUpdates[0].Generator.GetID())
	txCheck, err := fixture.Begin()
	require.NoError(t, err)
	payload, err := txCheck.Query("SELECT setup_metadata FROM forma_commands WHERE command_id=?", c.ID)
	require.NoError(t, err)
	require.Contains(t, payload[0], "draw-intent")
	require.NotContains(t, payload[0], "$value")
	require.NoError(t, txCheck.Rollback())

	commands, err := ds.QueryFormaCommands(&datastore.StatusQuery{CommandID: &datastore.QueryItem[string]{Item: c.ID}})
	require.NoError(t, err)
	require.Len(t, commands, 1)
	require.Len(t, commands[0].GeneratorUpdates, 1)
	require.Equal(t, id, commands[0].GeneratorUpdates[0].Generator.GetStackID())
	all, err := ds.LoadFormaCommands()
	require.NoError(t, err)
	found := false
	for _, saved := range all {
		if saved.ID == c.ID {
			found = true
			require.Len(t, saved.GeneratorUpdates, 1)
			require.Equal(t, c.GeneratorUpdates[0].Generator.GetID(), saved.GeneratorUpdates[0].Generator.GetID())
		}
	}
	require.True(t, found)

	// Stale caller progress cannot erase authoritative successful setup.
	require.NoError(t, ds.StoreFormaCommand(c, c.ID))
	reloaded, err := ds.GetFormaCommandByCommandID(c.ID)
	require.NoError(t, err)
	require.Equal(t, stack_update.StackUpdateStateSuccess, reloaded.StackUpdates[0].State)
	require.Equal(t, loaded.GeneratorUpdates[0].Version, reloaded.GeneratorUpdates[0].Version)
	require.NoError(t, reloaded.CheckSetupRecovery())
	require.Equal(t, forma_command.CommandStateSuccess, reloaded.State)
	replay, err := a.AdmitFormaCommand(c, req)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, result.StoredAdmission, replay.StoredAdmission)

	for _, failure := range []string{"INSERT INTO generators", "snapshot", "UPDATE command_admissions SET"} {
		t.Run("rollback_"+failure, func(t *testing.T) {
			raw, err := json.Marshal(c)
			require.NoError(t, err)
			var candidate forma_command.FormaCommand
			require.NoError(t, json.Unmarshal(raw, &candidate))
			candidate.ID = mksuid.New().String()
			sid := mksuid.New().String()
			sl := "rollback-" + sid
			candidate.Stacks = []forma_command.CommandStack{{ID: sid, Label: sl}}
			candidate.StackUpdates[0].Stack.ID = sid
			candidate.StackUpdates[0].Stack.Label = sl
			candidate.GeneratorUpdates[0].StackLabel = sl
			candidate.GeneratorUpdates[0].Generator.SetStackID(sid)
			candidate.GeneratorUpdates[0].Generator.SetID(mksuid.New().String())
			candidate.PolicyUpdates = []policy_update.PolicyUpdate{{Policy: &pkgmodel.AutoReconcilePolicy{Type: "auto-reconcile", Label: "automatic", IntervalSeconds: 3600}, StackLabel: sl, Operation: policy_update.PolicyOperationCreate}}
			r := req
			r.IdempotencyKey = candidate.ID
			r.Guards, err = a.ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
			require.NoError(t, err)
			failing := fixture
			failing.Begin = func() (datastore.AdmissionTransaction, error) {
				tx, e := fixture.Begin()
				if e != nil {
					return nil, e
				}
				return setupFailureTx{tx, failure}, nil
			}
			_, err = failing.AdmitFormaCommand(&candidate, r)
			require.ErrorContains(t, err, "injected")
			current, err := ds.GetStackByLabel(sl)
			require.NoError(t, err)
			require.Nil(t, current)
			receipt, err := a.LookupCommandAdmission(r.PrincipalScope, r.IdempotencyKey)
			require.NoError(t, err)
			require.Nil(t, receipt)
			_, err = ds.GetFormaCommandByCommandID(candidate.ID)
			require.Error(t, err)
			tx, err := fixture.Begin()
			require.NoError(t, err)
			for _, table := range []string{"stacks", "policies", "generators", "resource_updates"} {
				row, e := tx.Query("SELECT command_id FROM "+table+" WHERE command_id=?", candidate.ID)
				require.NoError(t, e)
				require.Nil(t, row)
			}
			require.NoError(t, tx.Rollback())
			require.Empty(t, candidate.StackUpdates[0].Version, "failed attempt must leave caller intent reusable")
			// Same key retries safely after rollback.
			_, err = a.AdmitFormaCommand(&candidate, r)
			require.NoError(t, err)
		})
	}
	t.Run("rename_preserves_generation_and_identity", func(t *testing.T) {
		genID := loaded.GeneratorUpdates[0].Generator.GetID()
		require.NoError(t, ds.AdvanceGeneration(genID, "generation-one", c.ID, json.RawMessage(`{"Length":24}`)))
		next := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
		next.Stacks = c.Stacks
		original := &pkgmodel.PasswordGenerator{ID: genID, StackID: id, Stack: label, Label: "password", Length: 24}
		renamed := &pkgmodel.PasswordGenerator{ID: genID, StackID: id, Stack: label, Label: "renamed", Alias: "password", Length: 32}
		next.GeneratorUpdates = []generator_update.GeneratorUpdate{{Generator: renamed, ExistingGenerator: original, StackLabel: label, Operation: generator_update.GeneratorOperationUpdate}}
		r := req
		r.IdempotencyKey = next.ID
		r.Guards, err = a.ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
		require.NoError(t, err)
		_, err = a.AdmitFormaCommand(next, r)
		require.NoError(t, err)
		identity, err := ds.GetGeneratorIdentity("renamed", label)
		require.NoError(t, err)
		require.Equal(t, genID, identity.ID)
		require.Equal(t, "generation-one", identity.GenerationID)
		require.JSONEq(t, `{"Length":24}`, string(identity.GenerationSpec))
		absent, err := ds.GetGenerator("password", label)
		require.NoError(t, err)
		require.Nil(t, absent)
		deletion := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
		deletion.Stacks = c.Stacks
		deletion.GeneratorUpdates = []generator_update.GeneratorUpdate{{Generator: renamed, ExistingGenerator: renamed, StackLabel: label, Operation: generator_update.GeneratorOperationDelete}}
		r.IdempotencyKey = deletion.ID
		r.Guards, err = a.ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
		require.NoError(t, err)
		_, err = a.AdmitFormaCommand(deletion, r)
		require.NoError(t, err)
		absent, err = ds.GetGenerator("renamed", label)
		require.NoError(t, err)
		require.Nil(t, absent)
		saved, err := ds.GetFormaCommandByCommandID(deletion.ID)
		require.NoError(t, err)
		require.Equal(t, genID, saved.GeneratorUpdates[0].ExistingGenerator.GetID())
		require.Equal(t, id, saved.GeneratorUpdates[0].ExistingGenerator.GetStackID())
	})
	t.Run("lost_commit_response_retains_complete_setup", func(t *testing.T) {
		next := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
		sid := mksuid.New().String()
		sl := "lost-" + sid
		next.Stacks = []forma_command.CommandStack{{ID: sid, Label: sl}}
		pinnedVersion := mksuid.New().String()
		next.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: sid, Label: sl}, Version: pinnedVersion, Operation: stack_update.StackOperationCreate}}
		r := req
		r.IdempotencyKey = next.ID
		r.Guards, err = a.ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
		require.NoError(t, err)
		losing := fixture
		losing.Begin = func() (datastore.AdmissionTransaction, error) {
			tx, e := fixture.Begin()
			if e != nil {
				return nil, e
			}
			return lostSetupResponseTx{tx}, nil
		}
		_, err = losing.AdmitFormaCommand(next, r)
		require.ErrorContains(t, err, "lost committed response")
		recovered, err := other.AdmitFormaCommand(next, r)
		require.NoError(t, err)
		require.True(t, recovered.Replayed)
		saved, err := ds.GetFormaCommandByCommandID(next.ID)
		require.NoError(t, err)
		require.True(t, saved.Setup.Committed)
		require.Equal(t, pinnedVersion, saved.StackUpdates[0].Version)
		require.NoError(t, saved.CheckSetupRecovery())
		require.Equal(t, forma_command.CommandStateSuccess, saved.State)
		tx, err := fixture.Begin()
		require.NoError(t, err)
		row, err := tx.Query("SELECT CAST(COUNT(*) AS VARCHAR(20)) FROM stacks WHERE id=?", sid)
		require.NoError(t, err)
		require.Equal(t, []string{"1"}, row)
		require.NoError(t, tx.Rollback())
	})
	t.Run("stale_before_setup_and_legacy_unknown", func(t *testing.T) {
		next := *c
		next.ID = mksuid.New().String()
		r := req
		r.IdempotencyKey = next.ID
		_, err := a.AdmitFormaCommand(&next, r)
		require.ErrorIs(t, err, datastore.ErrStaleAdmission)
		saved, err := a.LookupCommandAdmission(r.PrincipalScope, r.IdempotencyKey)
		require.NoError(t, err)
		require.Nil(t, saved)
		legacy := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
		require.NoError(t, ds.StoreFormaCommand(legacy, legacy.ID))
		loaded, err := ds.GetFormaCommandByCommandID(legacy.ID)
		require.NoError(t, err)
		require.Nil(t, loaded.Setup)
		require.Error(t, loaded.CheckSetupRecovery())
		legacy.Setup = &forma_command.SetupBoundary{Version: 1}
		require.NoError(t, ds.StoreFormaCommand(legacy, legacy.ID))
		loaded, err = ds.GetFormaCommandByCommandID(legacy.ID)
		require.NoError(t, err)
		require.NoError(t, loaded.CheckSetupRecovery())
		require.False(t, loaded.Setup.Committed)
		legacy.Setup.Committed = true // Generic storage must not claim atomic setup.
		require.NoError(t, ds.StoreFormaCommand(legacy, legacy.ID))
		loaded, err = ds.GetFormaCommandByCommandID(legacy.ID)
		require.NoError(t, err)
		require.False(t, loaded.Setup.Committed)
	})
	t.Run("independent_connections_same_key_setup", func(t *testing.T) {
		raw, _ := json.Marshal(c)
		var next forma_command.FormaCommand
		require.NoError(t, json.Unmarshal(raw, &next))
		next.ID = mksuid.New().String()
		sid := mksuid.New().String()
		sl := "raced-" + sid
		next.Stacks = []forma_command.CommandStack{{ID: sid, Label: sl}}
		next.StackUpdates[0].Stack.ID = sid
		next.StackUpdates[0].Stack.Label = sl
		next.GeneratorUpdates = nil
		r := req
		r.IdempotencyKey = next.ID
		r.Guards, err = a.ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
		require.NoError(t, err)
		var wg sync.WaitGroup
		wg.Add(2)
		errs := make([]error, 2)
		results := make([]datastore.AdmissionResult, 2)
		go func() { defer wg.Done(); results[0], errs[0] = a.AdmitFormaCommand(&next, r) }()
		go func() { defer wg.Done(); results[1], errs[1] = other.AdmitFormaCommand(&next, r) }()
		wg.Wait()
		for i, e := range errs {
			if e != nil {
				results[i], errs[i] = other.AdmitFormaCommand(&next, r)
			}
			require.NoError(t, errs[i])
		}
		require.Equal(t, results[0].StoredAdmission, results[1].StoredAdmission)
		tx, err := fixture.Begin()
		require.NoError(t, err)
		row, err := tx.Query("SELECT CAST(COUNT(*) AS VARCHAR(20)) FROM stacks WHERE id=?", sid)
		require.NoError(t, err)
		require.Equal(t, []string{"1"}, row)
		require.NoError(t, tx.Rollback())
	})
}

type setupFailureTx struct {
	datastore.AdmissionTransaction
	fail string
}

func (t setupFailureTx) Exec(q string, args ...any) error {
	if strings.HasPrefix(q, t.fail) {
		return fmt.Errorf("injected setup failure")
	}
	return t.AdmissionTransaction.Exec(q, args...)
}
func (t setupFailureTx) Store(c *forma_command.FormaCommand, id string) error {
	if t.fail == "snapshot" {
		return fmt.Errorf("injected snapshot failure")
	}
	return t.AdmissionTransaction.Store(c, id)
}

func runAtomicPolicyLifecycle(t *testing.T, ds datastore.Datastore, fixture datastore.AdmissionStore) {
	a := ds.(datastore.CommandAdmitter)
	sid := mksuid.New().String()
	label := "policy-life-" + sid
	pid := mksuid.New().String()
	inlineID := mksuid.New().String()
	makeCommand := func() *forma_command.FormaCommand {
		c := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
		c.Stacks = []forma_command.CommandStack{{ID: sid, Label: label}}
		return c
	}
	request := func(c *forma_command.FormaCommand) datastore.CommandAdmission {
		guards, err := a.ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
		require.NoError(t, err)
		return datastore.CommandAdmission{PrincipalScope: label, IdempotencyKey: c.ID, RequestDigest: strings.Repeat("f", 64), Receipt: []byte(`{"ok":true}`), Guards: guards}
	}
	policy := func(interval int64) *pkgmodel.AutoReconcilePolicy {
		return &pkgmodel.AutoReconcilePolicy{Type: "auto-reconcile", Label: label, IntervalSeconds: interval}
	}
	create := makeCommand()
	create.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: sid, Label: label}, Operation: stack_update.StackOperationCreate}}
	create.PolicyUpdates = []policy_update.PolicyUpdate{
		{PolicyID: pid, Policy: policy(60), Operation: policy_update.PolicyOperationCreate},
		{PolicyID: pid, StackID: sid, StackLabel: label, PolicyRef: label, Operation: policy_update.PolicyOperationAttach},
		{PolicyID: inlineID, StackID: sid, StackLabel: label, Policy: &pkgmodel.TTLPolicy{Label: label, TTLSeconds: 3600, OnDependents: "abort"}, Operation: policy_update.PolicyOperationCreate},
	}
	_, err := a.AdmitFormaCommand(create, request(create))
	require.NoError(t, err)
	reader, ok := ds.(interface {
		ReadPolicyIdentity(string, string) (*datastore.PolicyIdentity, error)
	})
	require.True(t, ok, "guarded planning must be able to bind policy identities")
	bound, err := reader.ReadPolicyIdentity(label, "")
	require.NoError(t, err)
	require.NotNil(t, bound)
	require.Equal(t, pid, bound.ID)
	require.NotEmpty(t, bound.Version)
	inline, err := reader.ReadPolicyIdentity(label, sid)
	require.NoError(t, err)
	require.NotNil(t, inline)
	require.Equal(t, inlineID, inline.ID)
	missing, err := reader.ReadPolicyIdentity("absent-policy", sid)
	require.NoError(t, err)
	require.Nil(t, missing)
	missing, err = reader.ReadPolicyIdentity(label, "different-stack")
	require.NoError(t, err)
	require.Nil(t, missing)

	refs, err := ds.GetStacksReferencingPolicy(label)
	require.NoError(t, err)
	require.Contains(t, refs, label)
	update := makeCommand()
	update.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: sid, Label: label, Description: "new description"}, Operation: stack_update.StackOperationUpdate}}
	update.PolicyUpdates = []policy_update.PolicyUpdate{{PolicyID: bound.ID, Policy: policy(120), Operation: policy_update.PolicyOperationUpdate}, {PolicyID: pid, StackID: sid, StackLabel: label, PolicyRef: label, Operation: policy_update.PolicyOperationDetach}}
	_, err = a.AdmitFormaCommand(update, request(update))
	require.NoError(t, err)
	refs, err = ds.GetStacksReferencingPolicy(label)
	require.NoError(t, err)
	require.NotContains(t, refs, label)
	stack, err := ds.GetStackByLabel(label)
	require.NoError(t, err)
	require.Equal(t, "new description", stack.Description)
	updatedIdentity, err := reader.ReadPolicyIdentity(label, "")
	require.NoError(t, err)
	require.Equal(t, pid, updatedIdentity.ID)
	require.Greater(t, updatedIdentity.Version, bound.Version)

	wrongVersion := makeCommand()
	wrongVersion.PolicyUpdates = []policy_update.PolicyUpdate{{PolicyID: pid, StackID: sid, StackLabel: label, PolicyRef: label, Version: "not-current", Operation: policy_update.PolicyOperationAttach}}
	_, err = a.AdmitFormaCommand(wrongVersion, request(wrongVersion))
	require.ErrorIs(t, err, datastore.ErrAdmissionConflict)
	// Cascade failure after removing junction rows must abort every effect.
	require.NoError(t, ds.AttachPolicyToStack(sid, label))
	deletion := makeCommand()
	deletion.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: sid, Label: label}, Operation: stack_update.StackOperationDelete}}
	deletion.PolicyUpdates = []policy_update.PolicyUpdate{{PolicyID: inlineID, StackID: sid, StackLabel: label, Policy: &pkgmodel.TTLPolicy{Label: label, TTLSeconds: 3600}, Operation: policy_update.PolicyOperationDelete}}
	r := request(deletion)
	deletion.PolicyUpdates[0].ExpectedVersion = "wrong-live-version"
	_, err = a.AdmitFormaCommand(deletion, r)
	require.ErrorIs(t, err, datastore.ErrAdmissionConflict)
	preservedStack, err := ds.GetStackByLabel(label)
	require.NoError(t, err)
	require.NotNil(t, preservedStack)
	require.Equal(t, sid, preservedStack.ID)
	preservedRefs, err := ds.GetStacksReferencingPolicy(label)
	require.NoError(t, err)
	require.Contains(t, preservedRefs, label)
	preservedPolicy, err := reader.ReadPolicyIdentity(label, sid)
	require.NoError(t, err)
	require.Equal(t, inline, preservedPolicy)
	receipt, err := a.LookupCommandAdmission(r.PrincipalScope, r.IdempotencyKey)
	require.NoError(t, err)
	require.Nil(t, receipt)
	_, err = ds.GetFormaCommandByCommandID(deletion.ID)
	require.Error(t, err)
	check, err := fixture.Begin()
	require.NoError(t, err)
	for _, table := range []string{"stacks", "policies"} {
		row, e := check.Query("SELECT command_id FROM "+table+" WHERE command_id=?", deletion.ID)
		require.NoError(t, e)
		require.Nil(t, row)
	}
	require.NoError(t, check.Rollback())
	deletion.PolicyUpdates[0].ExpectedVersion = inline.Version
	failing := fixture
	failing.Begin = func() (datastore.AdmissionTransaction, error) {
		tx, e := fixture.Begin()
		if e != nil {
			return nil, e
		}
		return setupFailureTx{tx, "INSERT INTO policies"}, nil
	}
	_, err = failing.AdmitFormaCommand(deletion, r)
	require.ErrorContains(t, err, "injected")
	refs, err = ds.GetStacksReferencingPolicy(label)
	require.NoError(t, err)
	require.Contains(t, refs, label)
	stack, err = ds.GetStackByLabel(label)
	require.NoError(t, err)
	require.Equal(t, sid, stack.ID)
	_, err = a.AdmitFormaCommand(deletion, r)
	require.NoError(t, err)
	stack, err = ds.GetStackByLabel(label)
	require.NoError(t, err)
	require.Nil(t, stack)
	refs, err = ds.GetStacksReferencingPolicy(label)
	require.NoError(t, err)
	require.NotContains(t, refs, label)
	// A reviewed old identity cannot be rebound even when supplied fresh guards.
	newStackID := mksuid.New().String()
	recreateVersion, createErr := ds.CreateStack(&pkgmodel.Stack{ID: newStackID, Label: label}, "recreate")
	err = createErr
	require.NoError(t, err)
	stack, err = ds.GetStackByLabel(label)
	require.NoError(t, err)
	require.NotNil(t, stack)
	require.Equal(t, newStackID, stack.ID)
	update.ID = mksuid.New().String()
	rejected, err := a.AdmitFormaCommand(update, request(update))
	require.ErrorIs(t, err, datastore.ErrAdmissionConflict, "old=%s new=%s version=%s result=%+v", sid, newStackID, recreateVersion, rejected)
	orphan := makeCommand()
	orphan.PolicyUpdates = []policy_update.PolicyUpdate{{PolicyID: mksuid.New().String(), StackID: sid, Policy: policy(120), Operation: policy_update.PolicyOperationCreate}}
	_, err = a.AdmitFormaCommand(orphan, request(orphan))
	require.ErrorIs(t, err, datastore.ErrAdmissionConflict)
	// Standalone delete carries this command's provenance.
	standalone := makeCommand()
	standalone.Stacks = nil
	standalone.PolicyUpdates = []policy_update.PolicyUpdate{{PolicyID: updatedIdentity.ID, Policy: policy(120), Operation: policy_update.PolicyOperationDelete}}
	_, err = a.AdmitFormaCommand(standalone, request(standalone))
	require.NoError(t, err)
	tx, err := fixture.Begin()
	require.NoError(t, err)
	row, err := tx.Query("SELECT command_id FROM policies WHERE id=? AND operation='delete'", pid)
	require.NoError(t, err)
	require.Equal(t, []string{standalone.ID}, row)
	require.NoError(t, tx.Rollback())
	missing, err = reader.ReadPolicyIdentity(label, "")
	require.NoError(t, err)
	require.Nil(t, missing)

}

type lostSetupResponseTx struct{ datastore.AdmissionTransaction }

func (t lostSetupResponseTx) Commit() error {
	if err := t.AdmissionTransaction.Commit(); err != nil {
		return err
	}
	return fmt.Errorf("lost committed response")
}

func runAtomicSetupBinaryVersionOrdering(t *testing.T, ds datastore.Datastore, fixture datastore.AdmissionStore) {
	sid := mksuid.New().String()
	newID := mksuid.New().String()
	label := "binary-setup-" + sid
	tx, err := fixture.Begin()
	require.NoError(t, err)
	prefix := strings.Repeat("0", 26)
	for _, row := range []struct{ id, version, op string }{{sid, prefix + "Z", "create"}, {sid, prefix + "a", "delete"}, {newID, prefix + "b", "create"}} {
		require.NoError(t, tx.Exec("INSERT INTO stacks(id,version,command_id,operation,label,description) VALUES (?,?,?,?,?,?)", row.id, row.version, "fixture", row.op, label, ""))
	}
	require.NoError(t, tx.Commit())
	current, err := ds.GetStackByLabel(label)
	require.NoError(t, err)
	require.Equal(t, newID, current.ID)
	c := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
	c.Stacks = []forma_command.CommandStack{{ID: sid, Label: label}}
	c.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: sid, Label: label}, Operation: stack_update.StackOperationUpdate, Version: prefix + "z"}}
	a := ds.(datastore.CommandAdmitter)
	guards, err := a.ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
	require.NoError(t, err)
	_, err = a.AdmitFormaCommand(c, datastore.CommandAdmission{Guards: guards, PrincipalScope: label, IdempotencyKey: c.ID, RequestDigest: strings.Repeat("a", 64), Receipt: []byte(`{"ok":true}`)})
	require.ErrorIs(t, err, datastore.ErrAdmissionConflict, "locale ordering must never select an old live incarnation over its later tombstone")
	tx, err = fixture.Begin()
	require.NoError(t, err)
	for _, v := range []string{prefix + "Z", prefix + "a"} {
		require.NoError(t, tx.Exec("INSERT INTO policies(id,version,command_id,operation,label,policy_type,stack_id,policy_data) VALUES (?,?,?,'update',?,'ttl','','{}')", sid, v, "fixture", label))
	}
	require.NoError(t, tx.Commit())
	policy, err := ds.(datastore.PolicyIdentityReader).ReadPolicyIdentity(label, "")
	require.NoError(t, err)
	require.Equal(t, prefix+"a", policy.Version)

}

func runAtomicSetupMembershipPreservation(t *testing.T, ds datastore.Datastore) {
	a := ds.(datastore.CommandAdmitter)
	c := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
	label := "allocated-membership-" + mksuid.New().String()
	c.Stacks = nil
	c.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{Label: label}, Operation: stack_update.StackOperationCreate}}
	guards, err := a.ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
	require.NoError(t, err)
	accepted, err := a.AdmitFormaCommand(c, datastore.CommandAdmission{Guards: guards, PrincipalScope: label, IdempotencyKey: c.ID, RequestDigest: strings.Repeat("a", 64), Receipt: []byte(`{"ok":true}`)})
	require.NoError(t, err)
	require.Empty(t, c.Stacks)
	require.Empty(t, c.StackUpdates[0].Stack.ID)
	require.Len(t, accepted.Command.Stacks, 1)
	require.NotEmpty(t, accepted.Command.Stacks[0].ID)
	authoritative := accepted.Command.Stacks
	for _, stale := range [][]forma_command.CommandStack{nil, {{ID: mksuid.New().String(), Label: label}}, {{ID: mksuid.New().String(), Label: "contradictory-label"}}} {
		c.Stacks = stale
		require.NoError(t, ds.StoreFormaCommand(c, c.ID), "generic save stays storage-only")
		loaded, err := ds.GetFormaCommandByCommandID(c.ID)
		require.NoError(t, err)
		require.Equal(t, authoritative, loaded.Stacks)
		history, err := ds.QueryFormaCommands(&datastore.StatusQuery{CommandID: &datastore.QueryItem[string]{Item: c.ID}, Stack: &datastore.QueryItem[string]{Item: label}})
		require.NoError(t, err)
		require.Len(t, history, 1)
		require.Equal(t, authoritative, history[0].Stacks)
		stack, err := ds.GetStackByLabel(label)
		require.NoError(t, err)
		require.Equal(t, authoritative[0].ID, stack.ID)
	}
}
