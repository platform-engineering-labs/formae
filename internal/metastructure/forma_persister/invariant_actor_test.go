//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package forma_persister

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// Inject ordinary admission outcomes at the real datastore/actor boundary.
type rejectingAdmissionDatastore struct{ datastore.Datastore }

func (d rejectingAdmissionDatastore) AdmitFormaCommand(_ *forma_command.FormaCommand, a datastore.CommandAdmission) (datastore.AdmissionResult, error) {
	causes := map[string]error{"stale": datastore.ErrStaleAdmission, "conflict": datastore.ErrAdmissionConflict, "invalid": datastore.ErrInvalidAdmission}
	return datastore.AdmissionResult{}, fmt.Errorf("injected admission: %w", causes[a.IdempotencyKey])
}
func (d rejectingAdmissionDatastore) ReadAdmissionRevisions(keys []string) ([]datastore.RevisionGuard, error) {
	return d.Datastore.(datastore.CommandAdmitter).ReadAdmissionRevisions(keys)
}
func (d rejectingAdmissionDatastore) LookupCommandAdmission(scope, key string) (*datastore.StoredAdmission, error) {
	return d.Datastore.(datastore.CommandAdmitter).LookupCommandAdmission(scope, key)
}

type corruptCompletion struct {
	messages.MarkResourceUpdateAsComplete
}
type readPending struct{ commandID string }
type invariantProbePersister struct {
	FormaCommandPersister
	started chan gen.PID
	stopped chan error
}

func (p *invariantProbePersister) Init(args ...any) error {
	if err := p.FormaCommandPersister.Init(args...); err != nil {
		return err
	}
	p.started <- p.PID()
	return nil
}
func (p *invariantProbePersister) Terminate(reason error) { p.stopped <- reason }
func (p *invariantProbePersister) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch msg := request.(type) {
	case corruptCompletion:
		cached, err := p.getOrLoadCommand(msg.CommandID)
		if err != nil {
			return nil, err
		}
		cached.pendingCompletions = 0 // Deliberately model a corrupted cached counter.
		// Execute the real mutation-before-invariant handler, not a fabricated reply.
		return p.FormaCommandPersister.HandleCall(from, ref, msg.MarkResourceUpdateAsComplete)
	case readPending:
		cached, err := p.getOrLoadCommand(msg.commandID)
		if err != nil {
			return nil, err
		}
		return cached.pendingCompletions, nil
	default:
		return p.FormaCommandPersister.HandleCall(from, ref, request)
	}
}

type invariantTestSupervisor struct {
	act.Supervisor
	factory gen.ProcessFactory
}

func (s *invariantTestSupervisor) Init(...any) (act.SupervisorSpec, error) {
	spec := act.SupervisorSpec{Type: act.SupervisorTypeOneForOne, Children: []act.SupervisorChildSpec{{Name: "InvariantPersister", Factory: s.factory}}}
	spec.Restart.Strategy = act.SupervisorStrategyTransient
	spec.Restart.Intensity = 2
	spec.Restart.Period = 5
	return spec, nil
}

func TestPersisterInvariantRestartsActorWhileAdmissionErrorsRemainRequests(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: t.TempDir() + "/invariant.db"}}, "test")
	require.NoError(t, err)
	defer ds.Close()
	command := newFormaCommandWithCreateResourceUpdate()
	require.NoError(t, ds.StoreFormaCommand(command, command.ID))
	started := make(chan gen.PID, 4)
	stopped := make(chan error, 4)
	options := gen.NodeOptions{Env: map[gen.Env]any{"Datastore": rejectingAdmissionDatastore{ds}}}
	options.Network.Mode = gen.NetworkModeDisabled
	options.Log.DefaultLogger.DisableBanner = true
	node, err := ergo.StartNode(gen.Atom("invariant-"+util.RandomString(8)+"@localhost"), options)
	require.NoError(t, err)
	defer node.StopForce()
	factory := func() gen.ProcessBehavior { return &invariantProbePersister{started: started, stopped: stopped} }
	_, err = node.Spawn(func() gen.ProcessBehavior { return &invariantTestSupervisor{factory: factory} }, gen.ProcessOptions{})
	require.NoError(t, err)
	var first gen.PID
	select {
	case first = <-started:
	case <-time.After(time.Second):
		t.Fatal("persister did not start")
	}
	for key, cause := range map[string]error{"stale": datastore.ErrStaleAdmission, "conflict": datastore.ErrAdmissionConflict, "invalid": datastore.ErrInvalidAdmission} {
		candidate := *command
		candidate.ID = util.NewID()
		response, err := node.Call(first, StoreNewFormaCommand{Command: candidate, Admission: &datastore.CommandAdmission{PrincipalScope: "test", IdempotencyKey: key, RequestDigest: strings.Repeat("a", 64), Receipt: []byte(`{}`)}})
		require.NoError(t, err, "ordinary admission failure must reply from the same actor")
		result, ok := response.(CommandPersistResult)
		require.True(t, ok)
		require.ErrorIs(t, result.CallFailure(), cause)
		select {
		case reason := <-stopped:
			t.Fatalf("admission failure terminated actor: %v", reason)
		default:
		}
	}
	ru := command.ResourceUpdates[0]
	response, callErr := node.CallWithTimeout(first, corruptCompletion{messages.MarkResourceUpdateAsComplete{CommandID: command.ID, ResourceURI: ru.URI(), Operation: ru.Operation, FinalState: resource_update.ResourceUpdateStateSuccess, ResourceModifiedTs: util.TimeNow()}}, 1)
	require.Error(t, callErr, "invariant failure must terminate, not return request reply %v", response)
	select {
	case reason := <-stopped:
		require.True(t, errors.Is(reason, errInvariantViolation), "unexpected termination: %v", reason)
	case <-time.After(time.Second):
		t.Fatal("invariant did not terminate persister")
	}
	var restarted gen.PID
	select {
	case restarted = <-started:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not restart persister")
	}
	require.NotEqual(t, first, restarted)
	pending, err := node.Call(restarted, readPending{command.ID})
	require.NoError(t, err)
	require.Equal(t, 0, pending, "restarted cache must rebuild from durable completed resource, not keep -1")
	persisted, err := ds.GetFormaCommandByCommandID(command.ID)
	require.NoError(t, err)
	require.Equal(t, resource_update.ResourceUpdateStateSuccess, persisted.ResourceUpdates[0].State)
}
