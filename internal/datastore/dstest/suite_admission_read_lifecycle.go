// © 2026 Platform Engineering Labs Inc.
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
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func RunAdmissionReadLifecycle(t *testing.T, ds, other datastore.Datastore, fixture datastore.AdmissionStore) {
	for _, source := range []forma_command.Source{forma_command.SourceSynchronizer, forma_command.SourceDiscovery} {
		t.Run("read_lifecycle_"+string(source), func(t *testing.T) {
			stack := &pkgmodel.Stack{Label: "read-" + mksuid.New().String()}
			_, err := ds.CreateStack(stack, "setup")
			require.NoError(t, err)
			keys, err := ds.(datastore.AdmissionScopeResolver).ResolveAdmissionStackGuards([]string{stack.Label})
			require.NoError(t, err)
			keys = append(keys, datastore.AdmissionStackGuardKey(stack.ID), datastore.AdmissionTopologyGuard, datastore.AdmissionTargetGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard, datastore.AdmissionStackMappingGuard)
			a := ds.(datastore.CommandAdmitter)
			sample := func() []datastore.RevisionGuard {
				g, e := a.ReadAdmissionRevisions(keys)
				require.NoError(t, e)
				return g
			}
			c := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
			c.Command = pkgmodel.CommandSync
			c.Source = source
			c.Stacks = []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}}
			before := sample()
			require.NoError(t, other.StoreFormaCommand(c, c.ID))
			require.Equal(t, before, sample(), "read command membership must not invalidate planning")
			require.NoError(t, other.UpdateFormaCommandProgress(c.ID, forma_command.CommandStateInProgress, time.Now()))
			require.Equal(t, before, sample(), "read progress must not invalidate planning")
			require.NoError(t, other.UpdateFormaCommandProgress(c.ID, forma_command.CommandStateSuccess, time.Now()))
			require.Equal(t, before, sample(), "read completion must not invalidate planning")
			require.NoError(t, other.DeleteFormaCommand(c, c.ID))
			require.Equal(t, before, sample(), "read removal must not invalidate planning")
			admitted := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, 0, nil)
			_, err = a.AdmitFormaCommand(admitted, datastore.CommandAdmission{Guards: before, PrincipalScope: "read-lifecycle", IdempotencyKey: mksuid.New().String(), RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{}`)})
			require.NoError(t, err)

			// A source or command transition must invalidate OLD and NEW membership.
			require.NoError(t, other.StoreFormaCommand(c, c.ID))
			before = sample()
			tx, err := fixture.Begin()
			require.NoError(t, err)
			defer func(tx datastore.AdmissionTransaction) { _ = tx.Rollback() }(tx)
			require.NoError(t, tx.Exec("UPDATE forma_commands SET source=? WHERE command_id=?", "user", c.ID))
			require.NoError(t, tx.Commit())
			require.NotEqual(t, before, sample(), "caller source text cannot hide an eligible command")
			before = sample()
			tx, err = fixture.Begin()
			require.NoError(t, err)
			defer func(tx datastore.AdmissionTransaction) { _ = tx.Rollback() }(tx)
			require.NoError(t, tx.Exec("UPDATE forma_commands SET source=? WHERE command_id=?", string(source), c.ID))
			require.NoError(t, tx.Commit())
			require.NotEqual(t, before, sample(), "removal of eligible OLD command must invalidate")
			before = sample()
			tx, err = fixture.Begin()
			require.NoError(t, err)
			defer func(tx datastore.AdmissionTransaction) { _ = tx.Rollback() }(tx)
			require.NoError(t, tx.Exec("UPDATE forma_commands SET command=? WHERE command_id=?", "apply", c.ID))
			require.NoError(t, tx.Commit())
			require.NotEqual(t, before, sample(), "source alone must never exempt an apply command")
			before = sample()
			tx, err = fixture.Begin()
			require.NoError(t, err)
			defer func(tx datastore.AdmissionTransaction) { _ = tx.Rollback() }(tx)
			require.NoError(t, tx.Exec("UPDATE forma_commands SET command=? WHERE command_id=?", "sync", c.ID))
			require.NoError(t, tx.Commit())
			require.NotEqual(t, before, sample(), "OLD apply command must invalidate when becoming sync")
			eligible := reconcileBuilder(forma_command.CommandStateNotStarted, pkgmodel.FormaApplyModeReconcile, 0, nil)
			require.NoError(t, other.StoreFormaCommand(eligible, eligible.ID))
			before = sample()
			tx, err = fixture.Begin()
			require.NoError(t, err)
			defer func(tx datastore.AdmissionTransaction) { _ = tx.Rollback() }(tx)
			require.NoError(t, tx.Exec("UPDATE command_stacks SET command_id=? WHERE command_id=?", eligible.ID, c.ID))
			require.NoError(t, tx.Commit())
			require.NotEqual(t, before, sample(), "NEW eligible command membership must invalidate")
			before = sample()
			tx, err = fixture.Begin()
			require.NoError(t, err)
			defer func(tx datastore.AdmissionTransaction) { _ = tx.Rollback() }(tx)
			require.NoError(t, tx.Exec("UPDATE command_stacks SET command_id=? WHERE command_id=?", c.ID, eligible.ID))
			require.NoError(t, tx.Commit())
			require.NotEqual(t, before, sample(), "OLD eligible command membership must invalidate")
			before = sample()
			r := pkgmodel.Resource{Ksuid: mksuid.New().String(), Stack: stack.Label, Label: "observed", Type: "AWS::S3::Bucket", Target: "target", Properties: json.RawMessage(`{"name":"changed"}`), Managed: true}
			_, err = other.StoreResource(&r, c.ID)
			require.NoError(t, err)
			require.NotEqual(t, before, sample(), "actual synchronized resource writes must still invalidate")
		})
	}
}

func RunAdmissionLargeGuardSet(t *testing.T, ds, other datastore.Datastore, fixture datastore.AdmissionStore) {
	t.Run("large_guard_set", func(t *testing.T) {
		const count = 5000
		ids := make([]string, count)
		prefix := mksuid.New().String()
		for i := range ids {
			ids[i] = fmt.Sprintf("%s-%05d", prefix, i)
		}
		metrics := &admissionSQLMetrics{}
		begin := fixture.Begin
		fixture.Begin = func() (datastore.AdmissionTransaction, error) {
			tx, err := begin()
			if err != nil {
				return nil, err
			}
			return &countedAdmissionTx{AdmissionTransaction: tx, metrics: metrics}, nil
		}
		keys, err := fixture.ResolveAdmissionResourceIdentityGuards(ids)
		require.NoError(t, err)
		require.Len(t, keys, count)
		registrationCalls := metrics.calls
		a := fixture
		guards, err := a.ReadAdmissionRevisions(keys)
		require.NoError(t, err)
		revisionCalls := metrics.calls - registrationCalls
		req := datastore.CommandAdmission{Guards: guards, PrincipalScope: prefix, IdempotencyKey: "large", RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{}`)}
		// A real insertion satisfying the last formerly absent predicate must stale.
		r := pkgmodel.Resource{Ksuid: ids[count-1], Stack: "large-" + prefix, Label: "last", Target: "target", Type: "AWS::S3::Bucket", Properties: json.RawMessage(`{"name":"last"}`), Managed: true}
		_, err = other.StoreResource(&r, "sync")
		require.NoError(t, err)
		c := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, 0, nil)
		_, err = a.AdmitFormaCommand(c, req)
		require.ErrorIs(t, err, datastore.ErrStaleAdmission)
		stored, err := a.LookupCommandAdmission(prefix, "large")
		require.NoError(t, err)
		require.Nil(t, stored)
		req.Guards, err = a.ReadAdmissionRevisions(keys)
		require.NoError(t, err)
		result, err := a.AdmitFormaCommand(c, req)
		require.NoError(t, err)
		require.False(t, result.Replayed)
		retry, err := a.AdmitFormaCommand(c, req)
		require.NoError(t, err)
		require.True(t, retry.Replayed)
		require.Equal(t, result.CommandID, retry.CommandID)
		t.Logf("registration SQL calls=%d; one revision sample SQL calls=%d; max binds=%d; largest returned row=%d bytes", registrationCalls, revisionCalls, metrics.maxBinds, metrics.maxRow)
		require.LessOrEqual(t, metrics.maxBinds, 6)
		require.Less(t, metrics.maxRow, datastore.MaxAdmissionReceiptBytes)
		t.Logf("%d identity guards: last-predicate insertion invalidated, fresh admission and durable replay passed", count)
	})
}

type admissionSQLMetrics struct{ calls, maxBinds, maxRow int }
type countedAdmissionTx struct {
	datastore.AdmissionTransaction
	metrics *admissionSQLMetrics
}

func (t *countedAdmissionTx) Exec(q string, args ...any) error {
	t.metrics.calls++
	t.metrics.maxBinds = max(t.metrics.maxBinds, len(args))
	return t.AdmissionTransaction.Exec(q, args...)
}
func (t *countedAdmissionTx) Query(q string, args ...any) ([]string, error) {
	t.metrics.calls++
	t.metrics.maxBinds = max(t.metrics.maxBinds, len(args))
	row, err := t.AdmissionTransaction.Query(q, args...)
	n := 0
	for _, v := range row {
		n += len(v)
	}
	t.metrics.maxRow = max(t.metrics.maxRow, n)
	return row, err
}
