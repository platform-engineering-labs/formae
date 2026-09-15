// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func RunAdmissionWriterReviewFixes(t *testing.T, ds, other datastore.Datastore, fixture, otherFixture datastore.AdmissionStore) {
	RunAdmissionPredicates(t, ds, other, fixture, otherFixture)
	RunAdmissionReadLifecycle(t, ds, other, otherFixture)
	RunAdmissionLargeGuardSet(t, ds, other, otherFixture)
	a := ds.(datastore.CommandAdmitter)
	sample := func(t *testing.T, keys []string) []datastore.RevisionGuard {
		t.Helper()
		g, e := a.ReadAdmissionRevisions(keys)
		require.NoError(t, e)
		return g
	}
	assertStale := func(t *testing.T, g []datastore.RevisionGuard) {
		t.Helper()
		c := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, 0, nil)
		req := datastore.CommandAdmission{Guards: g, PrincipalScope: "review-fix", IdempotencyKey: mksuid.New().String(), RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{}`)}
		_, e := a.AdmitFormaCommand(c, req)
		require.ErrorIs(t, e, datastore.ErrStaleAdmission)
		receipt, e := a.LookupCommandAdmission(req.PrincipalScope, req.IdempotencyKey)
		require.NoError(t, e)
		require.Nil(t, receipt)
		_, e = ds.GetFormaCommandByCommandID(c.ID)
		require.Error(t, e)
	}
	setup := func(t *testing.T) (*pkgmodel.Stack, *pkgmodel.Stack, pkgmodel.Resource, []string) {
		t.Helper()
		one := &pkgmodel.Stack{Label: "move-A-" + mksuid.New().String()}
		two := &pkgmodel.Stack{Label: "move-B-" + mksuid.New().String()}
		_, e := ds.CreateStack(one, "setup")
		require.NoError(t, e)
		_, e = ds.CreateStack(two, "setup")
		require.NoError(t, e)
		r := pkgmodel.Resource{Ksuid: mksuid.New().String(), NativeID: mksuid.New().String(), Stack: one.Label, Label: mksuid.New().String(), Target: "default-target", Type: "AWS::S3::Bucket", Properties: json.RawMessage(`{"name":"A"}`), Managed: true}
		_, e = ds.StoreResource(&r, "sync")
		require.NoError(t, e)
		keys, e := ds.(datastore.AdmissionScopeResolver).ResolveAdmissionStackGuards([]string{one.Label})
		require.NoError(t, e)
		keys = append(keys, datastore.AdmissionStackGuardKey(one.ID), datastore.AdmissionStackMappingGuard, datastore.AdmissionTargetGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard, datastore.AdmissionTopologyGuard)
		return one, two, r, keys
	}
	for _, bulk := range []bool{false, true} {
		name := "store"
		if bulk {
			name = "bulk"
		}
		t.Run("logical_uri_move_"+name, func(t *testing.T) {
			one, two, r, keys := setup(t)
			g := sample(t, keys)
			before, e := ds.LoadResourcesByStack(one.Label)
			require.NoError(t, e)
			require.Len(t, before, 1)
			require.Equal(t, g, sample(t, keys))
			r.Stack = two.Label
			r.Properties = json.RawMessage(`{"name":"B"}`)
			if bulk {
				_, e = other.BulkStoreResources([]pkgmodel.Resource{r}, "sync")
			} else {
				_, e = other.StoreResource(&r, "sync")
			}
			require.NoError(t, e)
			after, e := ds.LoadResourcesByStack(one.Label)
			require.NoError(t, e)
			require.Empty(t, after)
			assertStale(t, g)
		})
	}
	t.Run("logical_uri_latest_removal_exposes_old_scope", func(t *testing.T) {
		one, two, r, keys := setup(t)
		r.Stack = two.Label
		r.Properties = json.RawMessage(`{"name":"B"}`)
		id, e := other.StoreResource(&r, "sync")
		require.NoError(t, e)
		version := strings.Split(id, "_")[1]
		g := sample(t, keys)
		before, e := ds.LoadResourcesByStack(one.Label)
		require.NoError(t, e)
		require.Empty(t, before)
		tx, e := otherFixture.Begin()
		require.NoError(t, e)
		defer func(cleanup func() error) { _ = cleanup() }(tx.Rollback)
		require.NoError(t, tx.Exec("DELETE FROM resources WHERE uri=? AND version=?", string(r.URI()), version))
		require.NoError(t, tx.Commit())
		after, e := ds.LoadResourcesByStack(one.Label)
		require.NoError(t, e)
		require.Len(t, after, 1)
		assertStale(t, g)
	})

	t.Run("logical_uri_waiter_reads_committed_history", func(t *testing.T) {
		_, two, r, _ := setup(t)
		third := &pkgmodel.Stack{Label: "move-C-" + mksuid.New().String()}
		_, e := ds.CreateStack(third, "setup")
		require.NoError(t, e)
		bkeys, e := ds.(datastore.AdmissionScopeResolver).ResolveAdmissionStackGuards([]string{two.Label})
		require.NoError(t, e)
		predicateResolver := ds.(datastore.AdmissionPredicateResolver)
		targetKeys, e := predicateResolver.ResolveAdmissionTargetInventoryGuards([]string{two.Label})
		require.NoError(t, e)
		identityKeys, e := predicateResolver.ResolveAdmissionResourceIdentityGuards([]string{two.Label})
		require.NoError(t, e)
		bkeys = append(bkeys, targetKeys...)
		bkeys = append(bkeys, identityKeys...)
		insert := func(tx datastore.AdmissionTransaction, label, version string) error {
			rr := r
			rr.Stack = label
			rr.Target = label
			rr.Ksuid = label
			raw, e := json.Marshal(rr)
			if e != nil {
				return e
			}
			payload := "?"
			if fixture.Dialect == "postgres" {
				payload = "CAST(? AS JSONB)"
			}
			return tx.Exec("INSERT INTO resources(uri,version,command_id,operation,native_id,stack,type,label,target,data,ksuid) VALUES (?,?,?,?,?,?,?,?,?,"+payload+",?)", string(r.URI()), version, "sync", "update", r.NativeID, label, r.Type, r.Label, rr.Target, string(raw), rr.Ksuid)
		}
		held, e := fixture.Begin()
		require.NoError(t, e)
		defer func(cleanup func() error) { _ = cleanup() }(held.Rollback)
		require.NoError(t, insert(held, two.Label, "M"+mksuid.New().String()))
		bRevisions := map[string]int64{}
		for _, key := range bkeys {
			atB, e := held.Query("SELECT CAST(revision AS VARCHAR(20)) FROM admission_revisions WHERE guard_key=?", key)
			require.NoError(t, e)
			require.Len(t, atB, 1)
			rev, e := strconv.ParseInt(atB[0], 10, 64)
			require.NoError(t, e)
			bRevisions[key] = rev
		}
		started, done := make(chan struct{}), make(chan error, 1)
		go func() {
			tx, e := otherFixture.Begin()
			if e != nil {
				close(started)
				done <- e
				return
			}
			defer func(cleanup func() error) { _ = cleanup() }(tx.Rollback)
			close(started)
			e = insert(tx, third.Label, "N"+mksuid.New().String())
			if e == nil {
				e = tx.Commit()
			}
			done <- e
		}()
		admissionReceive(t, started)
		select {
		case e := <-done:
			t.Fatalf("same-URI writer did not wait: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
		require.NoError(t, held.Commit())
		require.NoError(t, admissionReceive(t, done))
		after := sample(t, bkeys)
		for _, g := range after {
			require.Greater(t, g.Revision, bRevisions[g.Key], "waiter must invalidate B stack/target/identity, first committed while its statement was waiting: %s", g.Key)
		}
	})
	if fixture.Dialect == "sqlite" {
		t.Run("sqlite_removed_command_intents", func(t *testing.T) {
			tx, e := fixture.Begin()
			require.NoError(t, e)
			row, e := tx.Query("SELECT CAST(recursive_triggers AS TEXT) FROM pragma_recursive_triggers")
			require.NoError(t, e)
			require.Equal(t, []string{"0"}, row)
			require.NoError(t, tx.Rollback())
			empty := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, 0, nil)
			emptyKeys := []string{datastore.AdmissionTargetGuard, datastore.AdmissionTopologyGuard, datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard}
			before := sample(t, emptyKeys)
			require.NoError(t, ds.StoreFormaCommand(empty, empty.ID))
			require.Equal(t, before, sample(t, emptyKeys))
			for _, kind := range []string{"target", "stack", "policy"} {
				t.Run(kind, func(t *testing.T) {
					c := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, 0, nil)
					var keys []string
					switch kind {
					case "target":
						c.TargetUpdates = []target_update.TargetUpdate{{Target: pkgmodel.Target{Label: "pending", Namespace: "AWS"}, Operation: target_update.TargetOperationUpdate}}
						keys = []string{datastore.AdmissionTargetGuard, datastore.AdmissionTopologyGuard}
					case "stack":
						c.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{Label: "pending"}, Operation: stack_update.StackOperationUpdate}}
						keys = []string{datastore.AdmissionStackMappingGuard}
					case "policy":
						c.PolicyUpdates = []policy_update.PolicyUpdate{{Policy: inlinePoliciesTTL("pending", "", 100), Operation: policy_update.PolicyOperationUpdate}}
						keys = []string{datastore.AdmissionPolicyGuard}
					}
					require.NoError(t, ds.StoreFormaCommand(c, c.ID))
					g := sample(t, keys)
					c.TargetUpdates = nil
					c.StackUpdates = nil
					c.PolicyUpdates = nil
					require.NoError(t, other.StoreFormaCommand(c, c.ID))
					assertStale(t, g)
				})
			}
		})
	}
}
