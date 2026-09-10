// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
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

func admissionReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(20 * time.Second):
		t.Fatal("admission test channel timed out")
		var zero T
		return zero
	}
}

// RunAdmissionWriters exercises public writers through actual independent DB
// connections. Fixture is used only for barriers and exceptional historical SQL.
func RunAdmissionWriters(t *testing.T, ds, other datastore.Datastore, fixture datastore.AdmissionStore) {
	a := ds.(datastore.CommandAdmitter)
	resolver := ds.(datastore.AdmissionScopeResolver)
	scope := "writer-" + mksuid.New().String()
	stack := &pkgmodel.Stack{ID: mksuid.New().String(), Label: scope}
	_, err := ds.CreateStack(stack, "setup")
	require.NoError(t, err)
	unrelated := &pkgmodel.Stack{ID: mksuid.New().String(), Label: scope + "-other"}
	_, err = ds.CreateStack(unrelated, "setup")
	require.NoError(t, err)
	labelKeys, err := resolver.ResolveAdmissionStackGuards([]string{scope})
	require.NoError(t, err)
	keys := append(labelKeys, datastore.AdmissionStackGuardKey(stack.ID), datastore.AdmissionStackMappingGuard, datastore.AdmissionTopologyGuard, datastore.AdmissionTargetGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard)
	snapshot := func(ks []string) []datastore.RevisionGuard {
		g, e := a.ReadAdmissionRevisions(ks)
		require.NoError(t, e)
		return g
	}
	command := func() *forma_command.FormaCommand {
		c := reconcileBuilder(forma_command.CommandStateSuccess, pkgmodel.FormaApplyModeReconcile, 0, nil)
		c.Stacks = []forma_command.CommandStack{{ID: stack.ID, Label: scope}}
		return c
	}
	request := func(g []datastore.RevisionGuard) datastore.CommandAdmission {
		return datastore.CommandAdmission{Guards: g, PrincipalScope: scope, IdempotencyKey: mksuid.New().String(), RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{"writer":true}`)}
	}
	stale := func(g []datastore.RevisionGuard) {
		t.Helper()
		req := request(g)
		c := command()
		_, e := a.AdmitFormaCommand(c, req)
		require.ErrorIs(t, e, datastore.ErrStaleAdmission)
		receipt, e := a.LookupCommandAdmission(scope, req.IdempotencyKey)
		require.NoError(t, e)
		require.Nil(t, receipt)
		_, e = ds.GetFormaCommandByCommandID(c.ID)
		require.Error(t, e)
	}
	resource := func(label string) pkgmodel.Resource {
		return pkgmodel.Resource{Ksuid: mksuid.New().String(), Stack: label, Label: mksuid.New().String(), Type: "AWS::S3::Bucket", Target: "test-target", Properties: json.RawMessage(`{"name":"one"}`), Managed: true}
	}
	r := resource(scope)
	_, err = ds.StoreResource(&r, "sync")
	require.NoError(t, err)
	t.Run("unchanged_and_unrelated_inventory", func(t *testing.T) {
		g := snapshot(keys)
		u := resource(unrelated.Label)
		_, e := other.BulkStoreResources([]pkgmodel.Resource{u}, "sync")
		require.NoError(t, e)
		require.Equal(t, g, snapshot(keys))
		_, e = a.AdmitFormaCommand(command(), request(g))
		require.NoError(t, e)
	})
	t.Run("sync_commit_before_admission", func(t *testing.T) {
		g := snapshot(keys)
		done := make(chan error, 1)
		go func() {
			rr := r
			rr.Properties = json.RawMessage(`{"name":"changed"}`)
			_, e := other.StoreResource(&rr, "sync")
			done <- e
		}()
		require.NoError(t, admissionReceive(t, done))
		stale(g)
	})
	t.Run("bulk_scopes", func(t *testing.T) {
		g := snapshot(keys)
		one, two := resource(scope), resource(unrelated.Label)
		_, e := other.BulkStoreResources([]pkgmodel.Resource{one, two}, "sync")
		require.NoError(t, e)
		stale(g)
	})
	t.Run("command_eligibility_and_ownership", func(t *testing.T) {
		u := resourceUpdate(scope, r.Ksuid, r.Label, `{"name":"desired"}`, types.OperationUpdate, resource_update.FormaCommandSourceUser)
		c := command()
		c.ResourceUpdates = []resource_update.ResourceUpdate{u}
		require.NoError(t, ds.StoreFormaCommand(c, c.ID))
		g := snapshot(keys)
		require.NoError(t, other.UpdateFormaCommandProgress(c.ID, forma_command.CommandStateFailed, time.Now()))
		stale(g)
		g = snapshot(keys)
		c.ResourceUpdates[0].DesiredState.Properties = json.RawMessage(`{"name":"owned"}`)
		c.ResourceUpdates[0].State = resource_update.ResourceUpdateStateNotStarted
		require.NoError(t, other.BulkStoreResourceUpdates(c.ID, c.ResourceUpdates))
		stale(g)
		g = snapshot(keys)
		require.NoError(t, other.UpdateResourceUpdateState(c.ID, r.Ksuid, types.OperationUpdate, resource_update.ResourceUpdateStateFailed, time.Now()))
		stale(g)
	})
	t.Run("incoming_reference_and_removal", func(t *testing.T) {
		incoming := resource(unrelated.Label)
		incoming.Properties = json.RawMessage(`{"link":{"\u0024ref":"formae://` + r.Ksuid + `#/name"}}`)
		g := snapshot(keys)
		_, e := other.StoreResource(&incoming, "sync")
		require.NoError(t, e)
		stale(g)
		g = snapshot(keys)
		incoming.Properties = json.RawMessage(`{"name":"removed-edge"}`)
		_, e = other.StoreResource(&incoming, "sync")
		require.NoError(t, e)
		stale(g)
	})
	t.Run("target_heartbeat_config_and_reap", func(t *testing.T) {
		target := &pkgmodel.Target{Label: scope, Namespace: "AWS", Config: json.RawMessage(`{"region":"one"}`)}
		_, e := ds.CreateTarget(target)
		require.NoError(t, e)
		loaded, e := ds.LoadTarget(scope)
		require.NoError(t, e)
		g := snapshot(keys)
		now := time.Now().UTC()
		_, e = other.UpdateTargetHealth(pkgmodel.TargetHealthObservation{TargetLabel: scope, IncarnationID: loaded.Health.IncarnationID, State: pkgmodel.TargetHealthStateReachable, ObservedAt: now, LastSeenAt: &now})
		require.NoError(t, e)
		require.Equal(t, g, snapshot(keys))
		target.Config = json.RawMessage(`{"region":"ONE"}`)
		_, e = other.UpdateTarget(target)
		require.NoError(t, e)
		stale(g)
		g = snapshot(keys)
		_, e = other.DeleteTarget(scope)
		require.NoError(t, e)
		stale(g)
	})
	t.Run("absence_recreation_long_unicode", func(t *testing.T) {
		label := strings.Repeat("界", 180) + scope
		lk, e := resolver.ResolveAdmissionStackGuards([]string{label})
		require.NoError(t, e)
		g := snapshot(lk)
		s := &pkgmodel.Stack{ID: mksuid.New().String(), Label: label}
		_, e = other.CreateStack(s, "create")
		require.NoError(t, e)
		stale(g)
		g = snapshot(lk)
		_, e = other.DeleteStack(label, "delete")
		require.NoError(t, e)
		stale(g)
		g = snapshot(lk)
		s.ID = mksuid.New().String()
		_, e = other.CreateStack(s, "recreate")
		require.NoError(t, e)
		stale(g)
		again, e := resolver.ResolveAdmissionStackGuards([]string{label})
		require.NoError(t, e)
		require.Equal(t, lk, again)
	})

	t.Run("history_ownership_and_old_new_scope", func(t *testing.T) {
		h := resource(scope)
		version, e := ds.StoreResource(&h, "sync")
		require.NoError(t, e)
		version = strings.Split(version, "_")[1]
		h.Version = version
		g := snapshot(keys)
		h.OwnedMembers = pkgmodel.OwnedMembers{"name": {Rule: "Mapping", Members: []string{"mine"}}}
		require.NoError(t, other.UpdateResourceVersionData(string(h.URI()), version, &h))
		stale(g)
		g = snapshot(keys)
		h.Stack = unrelated.Label
		h.Properties = json.RawMessage(`{"link":{"$ref":"formae://` + r.Ksuid + `#/name"}}`)
		require.NoError(t, other.UpdateResourceVersionData(string(h.URI()), version, &h))
		stale(g)
	})
	t.Run("policy_and_generator_domains", func(t *testing.T) {
		p := inlinePoliciesTTL(scope+"-policy", "", 3600)
		g := snapshot(keys)
		_, e := other.CreatePolicy(p, "policy")
		require.NoError(t, e)
		stale(g)
		g = snapshot(keys)
		require.NoError(t, other.AttachPolicyToStack(stack.ID, p.Label))
		stale(g)
		g = snapshot(keys)
		require.NoError(t, other.DetachPolicyFromStack(stack.Label, p.Label))
		stale(g)
		gen := testPasswordGenerator(scope+"-generator", stack, 16)
		g = snapshot(keys)
		_, e = other.CreateGenerator(gen, "generator")
		require.NoError(t, e)
		stale(g)
		g = snapshot(keys)
		gen.Length = 24
		_, e = other.UpdateGenerator(gen, "update")
		require.NoError(t, e)
		stale(g)
		identity, e := ds.GetGeneratorIdentity(gen.Label, stack.Label)
		require.NoError(t, e)
		g = snapshot(keys)
		require.NoError(t, other.AdvanceGeneration(identity.ID, mksuid.New().String(), "generation", json.RawMessage(`{}`)))
		stale(g)
	})
	t.Run("actual_target_reap_and_recovery", func(t *testing.T) {
		label := scope + "-reap"
		target := &pkgmodel.Target{Label: label, Namespace: "AWS", Config: json.RawMessage(`{}`)}
		_, e := ds.CreateTarget(target)
		require.NoError(t, e)
		loaded, e := ds.LoadTarget(label)
		require.NoError(t, e)
		tx, e := fixture.Begin()
		require.NoError(t, e)
		defer func(cleanup func() error) { _ = cleanup() }(tx.Rollback)
		require.NoError(t, tx.Exec("UPDATE targets SET health_state='unreachable',last_seen_at='2020-01-01',last_sample_at='2020-01-01',unreachable_accum_seconds=999999 WHERE label=?", label))
		require.NoError(t, tx.Commit())
		g := snapshot(keys)
		reaped, _, e := other.PersistTargetReap(datastore.PersistTargetReapRequest{Label: label, IncarnationID: loaded.Health.IncarnationID, LastSeenBefore: time.Now(), LastSampleBefore: time.Now(), ReapedAt: time.Now()})
		require.NoError(t, e)
		require.True(t, reaped)
		stale(g)
		g = snapshot(keys)
		_, e = other.UpdateTarget(target)
		require.NoError(t, e)
		stale(g)
	})
	if fixture.Dialect == "mssql" {
		t.Run("authoritative_collation_equivalent_labels", func(t *testing.T) {
			lower, e := resolver.ResolveAdmissionStackGuards([]string{scope})
			require.NoError(t, e)
			upper, e := resolver.ResolveAdmissionStackGuards([]string{strings.ToUpper(scope) + " "})
			require.NoError(t, e)
			require.Equal(t, lower, upper)
			g := snapshot(lower)
			u := resource(strings.ToUpper(scope))
			_, e = other.StoreResource(&u, "sync")
			require.NoError(t, e)
			stale(g)
		})
	}
	t.Run("concurrent_label_registration", func(t *testing.T) {
		label := scope + "-new"
		type result struct {
			keys []string
			err  error
		}
		done := make(chan result, 2)
		start := make(chan struct{})
		for _, d := range []datastore.Datastore{ds, other} {
			go func(d datastore.Datastore) {
				<-start
				k, e := d.(datastore.AdmissionScopeResolver).ResolveAdmissionStackGuards([]string{label})
				done <- result{k, e}
			}(d)
		}
		close(start)
		x, y := admissionReceive(t, done), admissionReceive(t, done)
		require.NoError(t, x.err)
		require.NoError(t, y.err)
		require.Equal(t, x.keys, y.keys)
	})
	t.Run("writer_waits_for_admitted_transaction", func(t *testing.T) {
		g := snapshot(keys)
		checked, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		gated := fixture
		gated.Begin = func() (datastore.AdmissionTransaction, error) {
			tx, e := fixture.Begin()
			return admissionGate{AdmissionTransaction: tx, checked: checked, release: release}, e
		}
		done := make(chan error, 1)
		go func() { _, e := gated.AdmitFormaCommand(command(), request(g)); done <- e }()
		admissionReceive(t, checked)
		writerDone := make(chan error, 1)
		started := make(chan struct{})
		go func() {
			close(started)
			rr := r
			rr.Properties = json.RawMessage(`{"name":"overlap"}`)
			_, e := other.StoreResource(&rr, "sync")
			writerDone <- e
		}()
		admissionReceive(t, started)
		select {
		case e := <-writerDone:
			t.Fatalf("writer bypassed admission: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
		unblock()
		require.NoError(t, admissionReceive(t, done))
		require.NoError(t, admissionReceive(t, writerDone))
		stale(g)
	})

	t.Run("target_only_command_eligibility", func(t *testing.T) {
		c := command()
		c.Stacks = nil
		require.NoError(t, ds.StoreFormaCommand(c, c.ID))
		g := snapshot([]string{datastore.AdmissionTargetGuard})
		require.NoError(t, other.UpdateFormaCommandTargetUpdates(c.ID, json.RawMessage(`[ {"Target":{"Label":"pending-target","Namespace":"AWS"},"Operation":"update","State":"NotStarted"} ]`), forma_command.CommandStateFailed, time.Now()))
		stale(g)
	})
	t.Run("stack_recreation_waits_for_admission", func(t *testing.T) {
		label := scope + "-race-recreate"
		s := &pkgmodel.Stack{ID: mksuid.New().String(), Label: label}
		_, e := ds.CreateStack(s, "setup")
		require.NoError(t, e)
		_, e = ds.DeleteStack(label, "setup")
		require.NoError(t, e)
		lk, e := resolver.ResolveAdmissionStackGuards([]string{label})
		require.NoError(t, e)
		g := snapshot(append(lk, keys...))
		checked, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		gated := fixture
		gated.Begin = func() (datastore.AdmissionTransaction, error) {
			tx, e := fixture.Begin()
			return admissionGate{AdmissionTransaction: tx, checked: checked, release: release}, e
		}
		done := make(chan error, 1)
		go func() { _, e := gated.AdmitFormaCommand(command(), request(g)); done <- e }()
		admissionReceive(t, checked)
		writerDone := make(chan error, 1)
		started := make(chan struct{})
		go func() {
			close(started)
			s.ID = mksuid.New().String()
			_, e := other.CreateStack(s, "recreate")
			writerDone <- e
		}()
		admissionReceive(t, started)
		select {
		case e := <-writerDone:
			t.Fatalf("recreation bypassed admission: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
		unblock()
		require.NoError(t, admissionReceive(t, done))
		require.NoError(t, admissionReceive(t, writerDone))
		stale(g)
	})

	t.Run("label_registry_survives_lifecycle_replacement", func(t *testing.T) {
		c := command()
		require.NoError(t, ds.StoreFormaCommand(c, c.ID))
		c.State = forma_command.CommandStateFailed
		require.NoError(t, ds.StoreFormaCommand(c, c.ID))
		again, e := resolver.ResolveAdmissionStackGuards([]string{scope})
		require.NoError(t, e)
		require.Equal(t, labelKeys, again)
	})
	if fixture.Dialect == "sqlite" {
		t.Run("sqlite_replace_preserves_old_scope", func(t *testing.T) {
			h := resource(scope)
			id, e := ds.StoreResource(&h, "setup")
			require.NoError(t, e)
			version := strings.Split(id, "_")[1]
			g := snapshot(labelKeys)
			tx, e := fixture.Begin()
			require.NoError(t, e)
			defer func(cleanup func() error) { _ = cleanup() }(tx.Rollback)
			raw, e := json.Marshal(map[string]string{"Stack": unrelated.Label})
			require.NoError(t, e)
			require.NoError(t, tx.Exec("INSERT OR REPLACE INTO resources(uri,version,stack,data,ksuid) VALUES (?,?,?,?,?)", string(h.URI()), version, unrelated.Label, string(raw), h.Ksuid))
			require.NoError(t, tx.Commit())
			stale(g)
		})
	}
	t.Run("actual_scope_same_key_concurrent_retries", func(t *testing.T) {
		req := request(snapshot(keys))
		type outcome struct {
			result datastore.AdmissionResult
			err    error
		}
		done := make(chan outcome, 2)
		start := make(chan struct{})
		for _, d := range []datastore.Datastore{ds, other} {
			go func(d datastore.Datastore) {
				<-start
				r, e := d.(datastore.CommandAdmitter).AdmitFormaCommand(command(), req)
				done <- outcome{r, e}
			}(d)
		}
		close(start)
		x, y := admissionReceive(t, done), admissionReceive(t, done)
		require.NoError(t, x.err)
		require.NoError(t, y.err)
		require.Equal(t, x.result.CommandID, y.result.CommandID)
		require.NotEqual(t, x.result.Replayed, y.result.Replayed)
	})
	t.Run("failed_contribution_rolls_back_revisions", func(t *testing.T) {
		g := snapshot(keys)
		c := command()
		c.ResourceUpdates = []resource_update.ResourceUpdate{resourceUpdate(scope, r.Ksuid, r.Label, `{}`, types.OperationAccept, resource_update.FormaCommandSourceUser), resourceUpdate(scope, mksuid.New().String(), "bad", `{invalid`, types.OperationCreate, resource_update.FormaCommandSourceUser)}
		req := request(g)
		_, e := a.AdmitFormaCommand(c, req)
		require.Error(t, e)
		require.Equal(t, g, snapshot(keys))
		receipt, e := a.LookupCommandAdmission(scope, req.IdempotencyKey)
		require.NoError(t, e)
		require.Nil(t, receipt)
	})
}
