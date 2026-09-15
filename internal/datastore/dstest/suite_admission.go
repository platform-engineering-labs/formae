// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// RunAdmissionPrimitive uses explicit revision SQL fixtures. This does NOT test
// writer invalidation or establish reviewed-plan safety. other must be a separate
// connection/pool to the same DB; reopen must close/recreate the primary handle.
func RunAdmissionPrimitive(t *testing.T, ds datastore.Datastore, other datastore.CommandAdmitter, fixture datastore.AdmissionStore, otherFixture datastore.AdmissionStore, reopen func() datastore.CommandAdmitter) {
	t.Run("atomic_membership_preservation", func(t *testing.T) { runAtomicSetupMembershipPreservation(t, ds) })
	t.Run("atomic_binary_versions", func(t *testing.T) { runAtomicSetupBinaryVersionOrdering(t, ds, fixture) })
	t.Run("atomic_policy_lifecycle", func(t *testing.T) { runAtomicPolicyLifecycle(t, ds, fixture) })
	t.Run("atomic_setup", func(t *testing.T) { runAtomicSetup(t, ds, fixture, other) })
	a := ds.(datastore.CommandAdmitter)
	scope := "test-" + mksuid.New().String()
	key := "stack:" + scope
	guards, err := a.ReadAdmissionRevisions([]string{key + "z", key, key})
	require.NoError(t, err)
	require.Equal(t, []datastore.RevisionGuard{{Key: key}, {Key: key + "z"}}, guards)
	base := datastore.CommandAdmission{PrincipalScope: scope, IdempotencyKey: "one", RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{"review":"one"}`), Guards: guards}
	command := func() *forma_command.FormaCommand {
		c := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, 0, nil)
		c.Stacks = []forma_command.CommandStack{{ID: scope, Label: scope}}
		return c
	}
	c := command()
	result, err := a.AdmitFormaCommand(c, base)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	require.Equal(t, c.ID, result.CommandID)
	loaded, err := ds.GetFormaCommandByCommandID(c.ID)
	require.NoError(t, err)
	require.Equal(t, c.Stacks, loaded.Stacks)
	tx, err := fixture.Begin()
	require.NoError(t, err)
	require.NoError(t, tx.Exec("UPDATE admission_revisions SET revision=revision+1 WHERE guard_key=?", key))
	require.NoError(t, tx.Commit())
	replayRequest := base
	replayRequest.Receipt = json.RawMessage(`{"review":"must-not-replace-original"}`)
	retry, err := a.AdmitFormaCommand(command(), replayRequest)
	require.NoError(t, err)
	require.True(t, retry.Replayed)
	require.Equal(t, result.StoredAdmission, retry.StoredAdmission)
	conflict := base
	conflict.RequestDigest = strings.Repeat("b", 64)
	_, err = a.AdmitFormaCommand(command(), conflict)
	require.ErrorIs(t, err, datastore.ErrAdmissionConflict)
	stale := base
	stale.IdempotencyKey = "stale"
	staleCommand := command()
	_, err = a.AdmitFormaCommand(staleCommand, stale)
	require.ErrorIs(t, err, datastore.ErrStaleAdmission)
	absent, err := a.LookupCommandAdmission(scope, "stale")
	require.NoError(t, err)
	require.Nil(t, absent)
	_, err = ds.GetFormaCommandByCommandID(staleCommand.ID)
	require.Error(t, err)
	c.State = forma_command.CommandStateFailed
	require.NoError(t, ds.StoreFormaCommand(c, c.ID))
	saved, err := a.LookupCommandAdmission(scope, "one")
	require.NoError(t, err)
	require.Equal(t, result.StoredAdmission, *saved)

	t.Run("rollback_all_contributions_and_reservation", func(t *testing.T) {
		bad := command()
		bad.ResourceUpdates = []resource_update.ResourceUpdate{
			resourceUpdate(scope, "accept-"+scope, "accept", `{}`, types.OperationAccept, resource_update.FormaCommandSourceUser),
			resourceUpdate(scope, "ordinary-"+scope, "ordinary", `{invalid`, types.OperationCreate, resource_update.FormaCommandSourceUser),
		}
		req := base
		req.IdempotencyKey = "rollback"
		req.Guards = []datastore.RevisionGuard{{Key: key + "rollback"}}
		_, err := a.AdmitFormaCommand(bad, req)
		require.Error(t, err)
		saved, err := a.LookupCommandAdmission(scope, req.IdempotencyKey)
		require.NoError(t, err)
		require.Nil(t, saved)
		_, err = ds.GetFormaCommandByCommandID(bad.ID)
		require.Error(t, err)
		updates, err := ds.LoadResourceUpdates(bad.ID)
		require.NoError(t, err)
		require.Empty(t, updates)
		tx, err := fixture.Begin()
		require.NoError(t, err)
		defer func(cleanup func() error) { _ = cleanup() }(tx.Rollback)
		row, err := tx.Query("SELECT command_id FROM command_stacks WHERE command_id=?", bad.ID)
		require.NoError(t, err)
		require.Nil(t, row)
		row, err = tx.Query("SELECT guard_key FROM admission_revisions WHERE guard_key=?", key+"rollback")
		require.NoError(t, err)
		require.Nil(t, row, "guard seed must roll back too")
		require.NoError(t, tx.Rollback())
		bad.ResourceUpdates[1].DesiredState.Properties = json.RawMessage(`{}`)
		admitted, err := a.AdmitFormaCommand(bad, req)
		require.NoError(t, err)
		require.Equal(t, bad.ID, admitted.CommandID)
		updates, err = ds.LoadResourceUpdates(bad.ID)
		require.NoError(t, err)
		require.Len(t, updates, 2)
		saved, err = a.LookupCommandAdmission(scope, req.IdempotencyKey)
		require.NoError(t, err)
		require.Equal(t, req.Receipt, saved.Receipt)

	})

	t.Run("separate_connections_same_key", func(t *testing.T) {
		req := base
		req.IdempotencyKey = "race"
		req.Guards = []datastore.RevisionGuard{{Key: key + "race"}}
		type outcome struct {
			r   datastore.AdmissionResult
			err error
		}
		start := make(chan struct{})
		done := make(chan outcome, 2)
		candidates := []*forma_command.FormaCommand{command(), command()}
		for i, store := range []datastore.CommandAdmitter{a, other} {
			go func(s datastore.CommandAdmitter, c *forma_command.FormaCommand) {
				<-start
				r, err := s.AdmitFormaCommand(c, req)
				done <- outcome{r, err}
			}(store, candidates[i])
		}
		close(start)
		first, second := admissionReceive(t, done), admissionReceive(t, done)
		require.NoError(t, first.err)
		require.NoError(t, second.err)
		require.Equal(t, first.r.CommandID, second.r.CommandID)
		require.NotEqual(t, first.r.Replayed, second.r.Replayed)
		for _, candidate := range candidates {
			_, err := ds.GetFormaCommandByCommandID(candidate.ID)
			if candidate.ID == first.r.CommandID {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		}
	})

	t.Run("committed_fixture_before_admission_rejects", func(t *testing.T) {
		req := base
		req.IdempotencyKey = "locked"
		req.Guards = []datastore.RevisionGuard{{Key: key + "locked"}}
		_, err := a.ReadAdmissionRevisions([]string{key + "locked"})
		require.NoError(t, err)
		tx, err := fixture.Begin()
		require.NoError(t, err)
		defer func(cleanup func() error) { _ = cleanup() }(tx.Rollback)
		require.NoError(t, tx.Exec("UPDATE admission_revisions SET revision=revision+1 WHERE guard_key=?", key+"locked"))
		started := make(chan struct{})
		done := make(chan error, 1)
		go func() { close(started); _, err := other.AdmitFormaCommand(command(), req); done <- err }()
		admissionReceive(t, started)
		select {
		case err := <-done:
			t.Fatalf("admission bypassed held revision lock: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		require.NoError(t, tx.Commit())
		select {
		case err := <-done:
			require.ErrorIs(t, err, datastore.ErrStaleAdmission)
		case <-time.After(15 * time.Second):
			t.Fatal("admission did not finish after fixture commit")
		}
	})

	t.Run("admission_holds_guard_until_commit", func(t *testing.T) {
		req := base
		req.IdempotencyKey = "held"
		req.Guards = []datastore.RevisionGuard{{Key: key + "held"}}
		_, err := a.ReadAdmissionRevisions([]string{key + "held"})
		require.NoError(t, err)
		checked := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		done := make(chan error, 1)
		gated := fixture
		gated.Begin = func() (datastore.AdmissionTransaction, error) {
			tx, err := fixture.Begin()
			return admissionGate{AdmissionTransaction: tx, checked: checked, release: release}, err
		}
		go func() { _, err := gated.AdmitFormaCommand(command(), req); done <- err }()
		select {
		case <-checked:
		case <-time.After(15 * time.Second):
			t.Fatal("admission did not reach store")
		}
		writerStarted := make(chan struct{})
		writerDone := make(chan error, 1)
		go func() {
			tx, err := otherFixture.Begin()
			if err != nil {
				writerDone <- err
				return
			}
			defer func(cleanup func() error) { _ = cleanup() }(tx.Rollback)
			close(writerStarted)
			if err = tx.Exec("UPDATE admission_revisions SET revision=revision+1 WHERE guard_key=?", key+"held"); err == nil {
				err = tx.Commit()
			}
			writerDone <- err
		}()
		admissionReceive(t, writerStarted)
		select {
		case err := <-writerDone:
			t.Fatalf("writer bypassed admission lock: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		unblock()
		require.NoError(t, admissionReceive(t, done))
		require.NoError(t, admissionReceive(t, writerDone))
		now, err := a.ReadAdmissionRevisions([]string{key + "held"})
		require.NoError(t, err)
		require.Equal(t, int64(1), now[0].Revision)
	})

	t.Run("separate_connections_conflicting_key", func(t *testing.T) {
		req := base
		req.IdempotencyKey = "conflicting-race"
		req.Guards = []datastore.RevisionGuard{{Key: key + "conflicting-race"}}
		second := req
		second.RequestDigest = strings.Repeat("c", 64)
		start := make(chan struct{})
		done := make(chan error, 2)
		go func() { <-start; _, err := a.AdmitFormaCommand(command(), req); done <- err }()
		go func() { <-start; _, err := other.AdmitFormaCommand(command(), second); done <- err }()
		close(start)
		firstErr, secondErr := admissionReceive(t, done), admissionReceive(t, done)
		if firstErr == nil {
			require.ErrorIs(t, secondErr, datastore.ErrAdmissionConflict)
		} else {
			require.ErrorIs(t, firstErr, datastore.ErrAdmissionConflict)
			require.NoError(t, secondErr)
		}
	})

	t.Run("binary_identities_and_existing_command", func(t *testing.T) {
		upper := base
		upper.IdempotencyKey = "Case"
		upper.Guards = []datastore.RevisionGuard{{Key: key + "Case"}}
		lower := upper
		lower.IdempotencyKey = "case"
		lower.Guards = []datastore.RevisionGuard{{Key: key + "case"}}
		first := command()
		_, err := a.AdmitFormaCommand(first, upper)
		require.NoError(t, err)
		second := command()
		_, err = a.AdmitFormaCommand(second, lower)
		require.NoError(t, err)
		check, err := a.ReadAdmissionRevisions([]string{key + "Case", key + "case"})
		require.NoError(t, err)
		require.Len(t, check, 2)
		ordinary := command()
		require.NoError(t, ds.StoreFormaCommand(ordinary, ordinary.ID))
		noReceipt := lower
		noReceipt.IdempotencyKey = "existing-ordinary"
		ordinary.State = forma_command.CommandStateCanceled
		createTx, err := fixture.Begin()
		require.NoError(t, err)
		defer func(cleanup func() error) { _ = cleanup() }(createTx.Rollback)
		require.Error(t, createTx.Store(ordinary, ordinary.ID), "guarded writer must use INSERT even after an absent-row check races")
		require.NoError(t, createTx.Rollback())

		_, err = a.AdmitFormaCommand(ordinary, noReceipt)
		require.ErrorIs(t, err, datastore.ErrAdmissionConflict)
		preserved, err := ds.GetFormaCommandByCommandID(ordinary.ID)
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, preserved.State)
		noMapping, err := a.LookupCommandAdmission(scope, noReceipt.IdempotencyKey)
		require.NoError(t, err)
		require.Nil(t, noMapping)
		third := lower
		third.IdempotencyKey = "different-key-same-command"
		_, err = a.AdmitFormaCommand(first, third)
		require.ErrorIs(t, err, datastore.ErrAdmissionConflict)
		missing, err := a.LookupCommandAdmission(scope, third.IdempotencyKey)
		require.NoError(t, err)
		require.Nil(t, missing)
	})

	t.Run("receipt_service_bound", func(t *testing.T) {
		req := base
		req.IdempotencyKey = "receipt-limit"
		req.Guards = []datastore.RevisionGuard{{Key: key + "receipt-limit"}}
		req.Receipt = json.RawMessage(`{"value":"` + strings.Repeat("x", datastore.MaxAdmissionReceiptBytes-len(`{"value":""}`)) + `"}`)
		c := command()
		result, err := a.AdmitFormaCommand(c, req)
		require.NoError(t, err)
		saved, err := a.LookupCommandAdmission(scope, req.IdempotencyKey)
		require.NoError(t, err)
		require.Equal(t, req.Receipt, saved.Receipt)
		replay, err := other.AdmitFormaCommand(command(), req)
		require.NoError(t, err)
		require.True(t, replay.Replayed)
		require.Equal(t, result.StoredAdmission, replay.StoredAdmission)
		req.IdempotencyKey = "receipt-too-large"
		req.Receipt = json.RawMessage(`{"value":"` + strings.Repeat("x", datastore.MaxAdmissionReceiptBytes+1-len(`{"value":""}`)) + `"}`)
		rejected := command()
		_, err = a.AdmitFormaCommand(rejected, req)
		require.ErrorIs(t, err, datastore.ErrInvalidAdmission)
		absent, err := a.LookupCommandAdmission(scope, req.IdempotencyKey)
		require.NoError(t, err)
		require.Nil(t, absent)
		_, err = ds.GetFormaCommandByCommandID(rejected.ID)
		require.Error(t, err)
	})

	a = reopen()
	replay, err := a.AdmitFormaCommand(command(), base)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, result.StoredAdmission, replay.StoredAdmission)
	t.Run("large_review_reopen", func(t *testing.T) {
		c := command()
		c.Message = "durable large resolution"
		c.Resolution = &pkgmodel.DriftReview{ObservationID: "observation", ReviewID: "review"}
		for i := 0; i < 180; i++ {
			id := fmt.Sprintf("%s-%04d", scope, i)
			c.Resolution.Decisions = append(c.Resolution.Decisions, pkgmodel.DriftDecision{ResourceID: id, Action: "absorb"})
			c.Resolution.Observations = append(c.Resolution.Observations, pkgmodel.DriftObservation{ResourceID: id, StackID: scope, Stack: scope, Type: "AWS::S3::Bucket", Label: id, Kind: "update", ObservedVersion: id, ObservedCommandID: id, BaselineCommandID: id})
			c.ResourceUpdates = append(c.ResourceUpdates, resourceUpdate(scope, id, id, `{"foo":"accepted"}`, types.OperationAccept, resource_update.FormaCommandSourceUser))
		}
		raw, err := json.Marshal(c.Resolution)
		require.NoError(t, err)
		require.Greater(t, len(raw), 64*1024, "exercise chunked Aurora command hydration beyond a full Data API row")
		req := base
		req.IdempotencyKey = "large-review"
		req.Receipt = json.RawMessage(`{"identity":true}`)
		req.Guards, err = a.ReadAdmissionRevisions([]string{key + "large-review"})
		require.NoError(t, err)
		result, err := a.AdmitFormaCommand(c, req)
		require.NoError(t, err)
		reopened := reopen()
		retry, err := reopened.AdmitFormaCommand(c, req)
		require.NoError(t, err)
		require.True(t, retry.Replayed)
		require.Equal(t, result.StoredAdmission, retry.StoredAdmission)
		stored, err := reopened.(datastore.Datastore).GetFormaCommandByCommandID(result.CommandID)
		require.NoError(t, err)
		require.Equal(t, c.Resolution, stored.Resolution)
		require.Equal(t, c.Message, stored.Message)
		require.Equal(t, forma_command.CommandStateSuccess, stored.State)
		require.Len(t, stored.ResourceUpdates, len(c.ResourceUpdates))
		for _, u := range stored.ResourceUpdates {
			require.Equal(t, types.OperationAccept, u.Operation)
			require.JSONEq(t, `{"foo":"accepted"}`, string(u.DesiredState.Properties))
		}
		t.Logf("rehydrated review bytes=%d with %d accepted desired contributions after reopen", len(raw), len(stored.ResourceUpdates))
	})
}

// admissionGate pauses after the engine locked and validated all guards, before
// invoking the real transaction-bound command writer. Only test code uses it.
type admissionGate struct {
	datastore.AdmissionTransaction
	checked chan struct{}
	release chan struct{}
}

func (g admissionGate) Store(c *forma_command.FormaCommand, id string) error {
	close(g.checked)
	<-g.release
	return g.AdmissionTransaction.Store(c, id)
}
