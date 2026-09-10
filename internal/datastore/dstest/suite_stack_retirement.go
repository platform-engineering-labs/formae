//go:build unit || integration

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package dstest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// retirementBarrier keeps the real transaction open after its last ownership
// read. Independent admission must wait, then reject its now-stale plan.
type retirementBarrier struct {
	datastore.AdmissionTransaction
	reached, release chan struct{}
}

func (b retirementBarrier) Query(q string, args ...any) ([]string, error) {
	row, err := b.AdmissionTransaction.Query(q, args...)
	if strings.HasPrefix(q, "WITH eligible AS") {
		close(b.reached)
		<-b.release
	}
	return row, err
}

type retirementFailure struct {
	datastore.AdmissionTransaction
	query bool
}

func (f retirementFailure) Query(q string, args ...any) ([]string, error) {
	if f.query && strings.HasPrefix(q, "WITH eligible AS") {
		return nil, fmt.Errorf("injected retirement read failure")
	}
	return f.AdmissionTransaction.Query(q, args...)
}
func (f retirementFailure) Exec(q string, args ...any) error {
	if !f.query && strings.HasPrefix(q, "INSERT INTO stacks(") {
		return fmt.Errorf("injected retirement tombstone failure")
	}
	return f.AdmissionTransaction.Exec(q, args...)
}

func RunStackRetirement(t *testing.T, ds, other datastore.Datastore, store datastore.AdmissionStore) {
	newStack := func(t *testing.T) *pkgmodel.Stack {
		t.Helper()
		label := "retire-" + util.NewID()
		_, err := ds.CreateStack(&pkgmodel.Stack{Label: label}, "setup")
		require.NoError(t, err)
		s, err := ds.GetStackByLabel(label)
		require.NoError(t, err)
		return s
	}
	command := func(s *pkgmodel.Stack) *forma_command.FormaCommand {
		return &forma_command.FormaCommand{ID: util.NewID(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: forma_command.CommandStateNotStarted, StartTs: time.Now().UTC(), Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, Stacks: []forma_command.CommandStack{{ID: s.ID, Label: s.Label}}, ResourceUpdates: []resource_update.ResourceUpdate{{DesiredState: pkgmodel.Resource{Ksuid: util.NewID(), Stack: s.Label, Label: "pending", Type: "Test::Resource", Target: "t", Managed: true, Properties: []byte(`{}`)}, StackLabel: s.Label, Operation: resource_update.OperationCreate, Source: resource_update.FormaCommandSourceUser, State: resource_update.ResourceUpdateStateNotStarted}}}
	}
	admission := func(t *testing.T, s *pkgmodel.Stack) datastore.CommandAdmission {
		t.Helper()
		keys, err := ds.(datastore.AdmissionScopeResolver).ResolveAdmissionStackGuards([]string{s.Label})
		require.NoError(t, err)
		keys = append(keys, datastore.AdmissionStackMappingGuard, datastore.AdmissionStackGuardKey(s.ID), datastore.AdmissionGeneratorGuard, datastore.AdmissionPolicyGuard)
		guards, err := ds.(datastore.CommandAdmitter).ReadAdmissionRevisions(keys)
		require.NoError(t, err)
		return datastore.CommandAdmission{PrincipalScope: "retirement", IdempotencyKey: util.NewID(), RequestDigest: strings.Repeat("a", 64), Receipt: []byte(`{}`), Guards: guards}
	}
	t.Run("admission_before_retirement", func(t *testing.T) {
		s := newStack(t)
		c := command(s)
		_, err := other.(datastore.CommandAdmitter).AdmitFormaCommand(c, admission(t, s))
		require.NoError(t, err)
		retired, err := ds.(datastore.EmptyStackRetirer).TryRetireEmptyStack(s.ID, s.Label, "")
		require.NoError(t, err)
		require.False(t, retired)
		c.State = forma_command.CommandStateFailed
		require.NoError(t, other.StoreFormaCommand(c, c.ID))
		retired, err = ds.(datastore.EmptyStackRetirer).TryRetireEmptyStack(s.ID, s.Label, "")
		require.NoError(t, err)
		require.False(t, retired)
		baseline, err := ds.GetResourcesAtLastReconcile(s.Label)
		require.NoError(t, err)
		require.Len(t, baseline, 1)
	})
	t.Run("retirement_before_admission", func(t *testing.T) {
		s := newStack(t)
		c := command(s)
		a := admission(t, s)
		reached, release := make(chan struct{}), make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		barrier := store
		barrier.Begin = func() (datastore.AdmissionTransaction, error) {
			tx, e := store.Begin()
			if e != nil {
				return nil, e
			}
			return retirementBarrier{tx, reached, release}, nil
		}
		retired := make(chan error, 1)
		go func() {
			ok, e := barrier.TryRetireEmptyStack(s.ID, s.Label, "")
			if e == nil && !ok {
				e = fmt.Errorf("empty stack did not retire")
			}
			retired <- e
		}()
		select {
		case <-reached:
		case e := <-retired:
			t.Fatalf("retirement ended before barrier: %v", e)
		case <-time.After(15 * time.Second):
			t.Fatal("retirement never reached protected emptiness read")
		}
		admitted := make(chan error, 1)
		go func() { _, e := other.(datastore.CommandAdmitter).AdmitFormaCommand(c, a); admitted <- e }()
		select {
		case e := <-admitted:
			t.Fatalf("independent admission escaped held retirement guards: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
		close(release)
		require.NoError(t, <-retired)
		require.ErrorIs(t, <-admitted, datastore.ErrStaleAdmission)
		stack, err := ds.GetStackByLabel(s.Label)
		require.NoError(t, err)
		require.Nil(t, stack)
		_, err = ds.CreateStack(&pkgmodel.Stack{Label: s.Label}, "recreate")
		require.NoError(t, err)
		ok, err := ds.(datastore.EmptyStackRetirer).TryRetireEmptyStack(s.ID, s.Label, "")
		require.NoError(t, err)
		require.False(t, ok)
		current, err := ds.GetStackByLabel(s.Label)
		require.NoError(t, err)
		require.NotNil(t, current)
		require.NotEqual(t, s.ID, current.ID)
	})
	t.Run("durable_terminal_cleanup_and_policy_cascade", func(t *testing.T) {
		s := newStack(t)
		_, err := ds.CreatePolicy(&pkgmodel.TTLPolicy{Label: "ttl", StackID: s.ID, TTLSeconds: 60}, "setup")
		require.NoError(t, err)

		c := command(s)
		c.Command = pkgmodel.CommandDestroy
		c.ResourceUpdates = nil
		require.NoError(t, ds.StoreFormaCommand(c, c.ID))
		ok, err := ds.(datastore.EmptyStackRetirer).TryRetireEmptyStack(s.ID, s.Label, c.ID)
		require.NoError(t, err)
		require.False(t, ok)
		c.State = forma_command.CommandStateSuccess
		require.NoError(t, ds.StoreFormaCommand(c, c.ID))
		ok, err = ds.(datastore.EmptyStackRetirer).TryRetireEmptyStack(s.ID, s.Label, c.ID)
		require.NoError(t, err)
		require.True(t, ok)
		policies, err := ds.GetInlinePoliciesForStack(s.ID)
		require.NoError(t, err)
		require.Empty(t, policies)
	})
	for _, read := range []bool{true, false} {
		t.Run(fmt.Sprintf("failure_rollback_read_%t", read), func(t *testing.T) {
			s := newStack(t)
			_, err := ds.CreatePolicy(&pkgmodel.TTLPolicy{Label: "ttl", StackID: s.ID, TTLSeconds: 60}, "setup")
			require.NoError(t, err)
			failing := store
			failing.Begin = func() (datastore.AdmissionTransaction, error) {
				tx, e := store.Begin()
				if e != nil {
					return nil, e
				}
				return retirementFailure{tx, read}, nil
			}
			ok, err := failing.TryRetireEmptyStack(s.ID, s.Label, "")
			require.Error(t, err)
			require.False(t, ok)
			stack, err := ds.GetStackByLabel(s.Label)
			require.NoError(t, err)
			require.NotNil(t, stack)
			policies, err := ds.GetInlinePoliciesForStack(s.ID)
			require.NoError(t, err)
			require.Len(t, policies, 1, "tombstone failure must roll policy cascade back")
		})
	}
}
