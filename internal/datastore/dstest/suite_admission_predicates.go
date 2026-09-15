//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package dstest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func RunAdmissionPredicates(t *testing.T, ds, other datastore.Datastore, fixture, otherFixture datastore.AdmissionStore) {
	t.Run("predicate_guards", func(t *testing.T) {
		resolver, ok := ds.(interface {
			ResolveAdmissionTargetInventoryGuards([]string) ([]string, error)
			ResolveAdmissionResourceIdentityGuards([]string) ([]string, error)
		})
		require.True(t, ok, "datastore must expose durable predicate guard resolution")
		a := ds.(datastore.CommandAdmitter)
		sample := func(keys []string) []datastore.RevisionGuard {
			g, e := a.ReadAdmissionRevisions(keys)
			require.NoError(t, e)
			return g
		}
		stale := func(g []datastore.RevisionGuard) {
			c := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, 0, nil)
			req := datastore.CommandAdmission{Guards: g, PrincipalScope: "predicate", IdempotencyKey: mksuid.New().String(), RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{}`)}
			_, e := a.AdmitFormaCommand(c, req)
			require.ErrorIs(t, e, datastore.ErrStaleAdmission)
			stored, e := a.LookupCommandAdmission(req.PrincipalScope, req.IdempotencyKey)
			require.NoError(t, e)
			require.Nil(t, stored)
		}
		for _, bulk := range []bool{false, true} {
			t.Run(map[bool]string{false: "store", true: "bulk"}[bulk], func(t *testing.T) {
				target := "predicate-T-" + mksuid.New().String()
				otherTarget := "predicate-U-" + mksuid.New().String()
				id := mksuid.New().String()
				tk, e := resolver.ResolveAdmissionTargetInventoryGuards([]string{target})
				require.NoError(t, e)
				uk, e := resolver.ResolveAdmissionTargetInventoryGuards([]string{otherTarget})
				require.NoError(t, e)
				ik, e := resolver.ResolveAdmissionResourceIdentityGuards([]string{id})
				require.NoError(t, e)
				g := sample(append(append([]string{}, tk...), ik...))
				independence := sample(append(uk, datastore.AdmissionTargetGuard))
				missing, e := ds.LoadResourceById(id)
				require.NoError(t, e)
				require.Nil(t, missing)
				r := pkgmodel.Resource{Ksuid: id, NativeID: id, Stack: "virtual-" + mksuid.New().String(), Target: target, Type: "AWS::S3::Bucket", Label: id, Managed: true, Properties: json.RawMessage(`{"name":"one"}`)}
				if bulk {
					_, e = other.BulkStoreResources([]pkgmodel.Resource{r}, "sync")
				} else {
					_, e = other.StoreResource(&r, "sync")
				}
				require.NoError(t, e)
				require.NotEqual(t, g, sample(append(append([]string{}, tk...), ik...)))
				require.Equal(t, independence, sample(append(uk, datastore.AdmissionTargetGuard)))
				stale(g)
				beforeMove := sample(tk)
				r.Target = otherTarget
				r.Managed = false
				r.Properties = json.RawMessage(`{"name":"moved"}`)
				_, e = other.StoreResource(&r, "sync")
				require.NoError(t, e)
				stale(beforeMove)
				// Removing the newest row exposes the old target and must invalidate its empty predicate.
				beforeExpose := sample(tk)
				tx, e := otherFixture.Begin()
				require.NoError(t, e)
				require.NoError(t, tx.Exec("DELETE FROM resources WHERE uri=? AND version=(SELECT MAX(version) FROM resources WHERE uri=?)", string(r.URI()), string(r.URI())))
				require.NoError(t, tx.Commit())
				stale(beforeExpose)
				// Physical identity changes invalidate both an existing hit and a reserved absence.
				nextID := mksuid.New().String()
				nk, e := resolver.ResolveAdmissionResourceIdentityGuards([]string{nextID})
				require.NoError(t, e)
				old := sample(ik)
				next := sample(nk)
				tx, e = otherFixture.Begin()
				require.NoError(t, e)
				require.NoError(t, tx.Exec("UPDATE resources SET ksuid=? WHERE uri=?", nextID, string(r.URI())))
				require.NoError(t, tx.Commit())
				stale(old)
				stale(next)
				beforeRemoval := sample(nk)
				tx, e = otherFixture.Begin()
				require.NoError(t, e)
				require.NoError(t, tx.Exec("DELETE FROM resources WHERE uri=?", string(r.URI())))
				require.NoError(t, tx.Commit())
				stale(beforeRemoval)
				missing, e = ds.LoadResourceById(nextID)
				require.NoError(t, e)
				require.Nil(t, missing)

			})
		}
		t.Run("long_embedded_target_rewrite", func(t *testing.T) {
			var b strings.Builder
			for range 350 {
				b.WriteString(mksuid.New().String())
			}
			longTarget := b.String()
			keys, e := resolver.ResolveAdmissionTargetInventoryGuards([]string{longTarget})
			require.NoError(t, e)
			require.Len(t, keys, 1)
			require.Less(t, len(keys[0]), 450)
			again, e := resolver.ResolveAdmissionTargetInventoryGuards([]string{longTarget, longTarget})
			require.NoError(t, e)
			require.Equal(t, keys, again)
			r := pkgmodel.Resource{Ksuid: mksuid.New().String(), Stack: "virtual-long", Type: "Test::Resource", Label: "long", Target: "short-" + mksuid.New().String(), Managed: true, Properties: json.RawMessage(`{"x":1}`)}
			stored, e := other.StoreResource(&r, "sync")
			require.NoError(t, e)
			version := strings.TrimPrefix(stored, r.Ksuid+"_")
			before := sample(keys)
			r.Target = longTarget
			require.NoError(t, other.UpdateResourceVersionData(string(r.URI()), version, &r))
			stale(before)
			before = sample(keys)
			r.Managed = false
			require.NoError(t, other.UpdateResourceVersionData(string(r.URI()), version, &r))
			stale(before)
			if fixture.Dialect == "mssql" {
				equivalent, e := resolver.ResolveAdmissionTargetInventoryGuards([]string{strings.ToUpper(longTarget) + "   "})
				require.NoError(t, e)
				require.Equal(t, keys, equivalent)
				caseID := mksuid.New().String()
				lower, e := resolver.ResolveAdmissionResourceIdentityGuards([]string{caseID})
				require.NoError(t, e)
				upper, e := resolver.ResolveAdmissionResourceIdentityGuards([]string{strings.ToUpper(caseID)})
				require.NoError(t, e)
				require.NotEqual(t, lower, upper)
			}
		})
		t.Run("resource_predicate_rollback", func(t *testing.T) {
			id := mksuid.New().String()
			target := "rollback-" + id
			tk, e := resolver.ResolveAdmissionTargetInventoryGuards([]string{target})
			require.NoError(t, e)
			ik, e := resolver.ResolveAdmissionResourceIdentityGuards([]string{id})
			require.NoError(t, e)
			keys := append(tk, ik...)
			before := sample(keys)
			tx, e := otherFixture.Begin()
			require.NoError(t, e)
			require.NoError(t, tx.Exec("INSERT INTO resources(uri,version,ksuid,stack,target,data) VALUES (?,?,?,?,?,?)", "resource://"+id, id, id, "virtual-rollback", target, `{}`))
			require.NoError(t, tx.Rollback())
			require.Equal(t, before, sample(keys))
		})

		t.Run("reaped_observation_is_not_confirmed_deletion", func(t *testing.T) {
			id := mksuid.New().String()
			label := "reap-" + id
			target := &pkgmodel.Target{Label: label, Namespace: "AWS", Config: json.RawMessage(`{}`)}
			_, e := ds.CreateTarget(target)
			require.NoError(t, e)
			loaded, e := ds.LoadTarget(label)
			require.NoError(t, e)
			r := pkgmodel.Resource{Ksuid: id, NativeID: id, Stack: "virtual-" + id, Target: label, Type: "Test::Resource", Label: id, Managed: true, Properties: json.RawMessage(`{"x":1}`)}
			baseline := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, -10*time.Minute, nil)
			require.NoError(t, ds.StoreFormaCommand(baseline, baseline.ID))
			_, e = ds.StoreResource(&r, baseline.ID)
			require.NoError(t, e)
			tk, e := resolver.ResolveAdmissionTargetInventoryGuards([]string{label})
			require.NoError(t, e)
			ik, e := resolver.ResolveAdmissionResourceIdentityGuards([]string{id})
			require.NoError(t, e)
			reader := ds.(datastore.ResourceObservationReader)
			before, e := reader.GetResourceObservation(id)
			require.NoError(t, e)
			require.NotNil(t, before)
			tx, e := fixture.Begin()
			require.NoError(t, e)
			require.NoError(t, tx.Exec("UPDATE targets SET health_state='unreachable',last_seen_at='2020-01-01',last_sample_at='2020-01-01',unreachable_accum_seconds=999999 WHERE label=?", label))
			require.NoError(t, tx.Commit())
			failedRead, e := reader.GetResourceObservation(id)
			require.NoError(t, e)
			require.Equal(t, before, failedRead, "target read failure alone is not a resource deletion")
			guards := sample(append(tk, ik...))
			reaped, _, e := other.PersistTargetReap(datastore.PersistTargetReapRequest{Label: label, IncarnationID: loaded.Health.IncarnationID, LastSeenBefore: time.Now(), LastSampleBefore: time.Now(), ReapedAt: time.Now()})
			require.NoError(t, e)
			require.True(t, reaped)
			stale(guards)
			observation, e := reader.GetResourceObservation(id)
			require.NoError(t, e)
			require.NotNil(t, observation)
			require.Equal(t, "reaped", observation.Operation)
			require.Equal(t, before.Version, observation.Version)
			require.False(t, observation.ConfirmedDeletion)
			for i := range 2 {
				if i == 1 {
					latest, e := reader.GetResourceObservation(id)
					require.NoError(t, e)
					tx, e := otherFixture.Begin()
					require.NoError(t, e)
					require.NoError(t, tx.Exec("INSERT INTO resources(uri,version,ksuid,stack,target,data,operation,managed) SELECT uri,?,ksuid,stack,target,data,operation,managed FROM resources WHERE uri=? AND version=?", mksuid.New().String(), latest.URI, latest.Version))
					require.NoError(t, tx.Commit())
				}
				_, e = other.DeleteResource(&r, "forget-reaped")
				require.NoError(t, e)
				forgotten, e := reader.GetResourceObservation(id)
				require.NoError(t, e)
				require.Equal(t, "delete", forgotten.Operation)
				require.False(t, forgotten.ConfirmedDeletion, "DB-only reap cleanup does not prove a cloud deletion")
			}
			_, e = other.UpdateTarget(target)
			require.NoError(t, e)
			r.Properties = json.RawMessage(`{"x":2}`)
			_, e = other.StoreResource(&r, "fresh-live")
			require.NoError(t, e)
			_, e = other.DeleteResource(&r, "confirmed-after-live")
			require.NoError(t, e)
			fresh, e := reader.GetResourceObservation(id)
			require.NoError(t, e)
			require.True(t, fresh.ConfirmedDeletion, "fresh managed live evidence resets earlier reap uncertainty")

			mods, e := ds.GetResourceModificationsSinceLastReconcile(r.Stack)
			require.NoError(t, e)
			for _, mod := range mods {
				require.NotEqual(t, "delete", mod.Operation)
			}
		})
		t.Run("observation_absence_duplicates_and_physical_identity", func(t *testing.T) {
			id := mksuid.New().String()
			reader := ds.(datastore.ResourceObservationReader)
			missing, e := reader.GetResourceObservation(id)
			require.NoError(t, e)
			require.Nil(t, missing)
			r := pkgmodel.Resource{Ksuid: id, NativeID: id, Stack: "virtual-" + id, Target: "obs", Type: "Test::Resource", Label: id, Managed: true, Properties: json.RawMessage(`{"x":1}`)}
			_, e = ds.StoreResource(&r, "sync")
			require.NoError(t, e)
			row, e := reader.GetResourceObservation(id)
			require.NoError(t, e)
			require.NotNil(t, row)
			r.Ksuid = "embedded-false-identity"
			require.NoError(t, ds.UpdateResourceVersionData(row.URI, row.Version, &r))
			row, e = reader.GetResourceObservation(id)
			require.NoError(t, e)
			require.Equal(t, id, row.KSUID)
			require.Equal(t, id, row.Resource.Ksuid)
			tx, e := otherFixture.Begin()
			require.NoError(t, e)
			require.NoError(t, tx.Exec("INSERT INTO resources(uri,version,ksuid,stack,target,data,operation) SELECT ?,version,ksuid,stack,target,data,operation FROM resources WHERE uri=? AND version=?", "resource://duplicate-"+id, row.URI, row.Version))
			require.NoError(t, tx.Commit())
			_, e = reader.GetResourceObservation(id)
			require.ErrorContains(t, e, "ambiguous current resource identity")
			tx, e = otherFixture.Begin()
			require.NoError(t, e)
			require.NoError(t, tx.Exec("UPDATE resources SET operation='delete',version=? WHERE uri=? AND version=?", mksuid.New().String(), row.URI, row.Version))
			require.NoError(t, tx.Commit())
			_, e = reader.GetResourceObservation(id)
			require.ErrorContains(t, e, "ambiguous current resource identity", "newest old-alias tombstone cannot hide another URI's live candidate")

		})

		t.Run("target_registration_waits_and_rechecks", func(t *testing.T) {
			for _, commit := range []bool{true, false} {
				label := "registration-" + mksuid.New().String()
				held, e := fixture.Begin()
				require.NoError(t, e)
				require.NoError(t, held.Exec("INSERT INTO admission_inventory_targets(label) VALUES (?)", label))
				inserted, e := held.Query("SELECT guard_key FROM admission_inventory_targets WHERE label=?", label)
				require.NoError(t, e)
				type resolved struct {
					keys []string
					err  error
				}
				started, done := make(chan struct{}), make(chan resolved, 1)
				go func() {
					close(started)
					keys, err := other.(datastore.AdmissionPredicateResolver).ResolveAdmissionTargetInventoryGuards([]string{label})
					done <- resolved{keys, err}
				}()
				admissionReceive(t, started)
				select {
				case result := <-done:
					_ = held.Rollback()
					t.Fatalf("registration did not wait: %+v", result)
				case <-time.After(100 * time.Millisecond):
				}
				if commit {
					require.NoError(t, held.Commit())
				} else {
					require.NoError(t, held.Rollback())
				}
				result := admissionReceive(t, done)
				require.NoError(t, result.err)
				require.Len(t, result.keys, 1)
				if commit {
					require.Equal(t, inserted, result.keys)
				} else {
					require.NotEqual(t, inserted, result.keys)
				}
				again, e := resolver.ResolveAdmissionTargetInventoryGuards([]string{label})
				require.NoError(t, e)
				require.Equal(t, result.keys, again)
			}
		})

		t.Run("unmanaged_forgetting_is_not_confirmed_deletion", func(t *testing.T) {
			id := mksuid.New().String()
			r := pkgmodel.Resource{Ksuid: id, NativeID: id, Stack: "$unmanaged", Target: "default", Type: "Test::Resource", Label: id, Managed: false, Properties: json.RawMessage(`{"x":1}`)}
			_, e := other.StoreResource(&r, "discovery")
			require.NoError(t, e)
			_, e = other.DeleteResource(&r, "discovery-filter-eviction")
			require.NoError(t, e)
			observation, e := ds.(datastore.ResourceObservationReader).GetResourceObservation(id)
			require.NoError(t, e)
			require.NotNil(t, observation)
			require.Equal(t, "delete", observation.Operation)
			require.False(t, observation.ConfirmedDeletion)
		})

		t.Run("observation_identity_rewrite_does_not_resurrect_history", func(t *testing.T) {
			id := mksuid.New().String()
			next := mksuid.New().String()
			reader := ds.(datastore.ResourceObservationReader)
			r := pkgmodel.Resource{Ksuid: id, NativeID: id, Stack: "virtual-" + id, Target: "default", Type: "Test::Resource", Label: id, Managed: true, Properties: json.RawMessage(`{"x":1}`)}
			_, e := ds.StoreResource(&r, "sync")
			require.NoError(t, e)
			prior, e := reader.GetResourceObservation(id)
			require.NoError(t, e)
			require.NotNil(t, prior)
			tx, e := otherFixture.Begin()
			require.NoError(t, e)
			require.NoError(t, tx.Exec("INSERT INTO resources(uri,version,ksuid,stack,target,data,operation,managed) SELECT uri,?,?,stack,target,data,operation,managed FROM resources WHERE uri=? AND version=?", mksuid.New().String(), next, prior.URI, prior.Version))
			require.NoError(t, tx.Commit())
			old, e := reader.GetResourceObservation(id)
			require.NoError(t, e)
			require.Nil(t, old, "old physical identity cannot select a superseded URI version")
			latest, e := reader.GetResourceObservation(next)
			require.NoError(t, e)
			require.NotNil(t, latest)
			require.Equal(t, next, latest.KSUID)
			require.Equal(t, next, latest.Resource.Ksuid)
		})

	})
}
