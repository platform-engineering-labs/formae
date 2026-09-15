//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/testplugin/fakeaws"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

type uncertainScopedCommit struct {
	*scopedReadBarrier
	lose            atomic.Bool
	beforeAdmission func()
	beforePin       func()
}

func (d *uncertainScopedCommit) AdmitFormaCommand(c *forma_command.FormaCommand, a datastore.CommandAdmission) (datastore.AdmissionResult, error) {
	if d.beforeAdmission != nil {
		f := d.beforeAdmission
		d.beforeAdmission = nil
		f()
	}
	result, err := d.CommandAdmitter.AdmitFormaCommand(c, a)
	if err == nil && !result.Replayed && d.lose.CompareAndSwap(true, false) {
		return datastore.AdmissionResult{}, errors.New("injected lost commit response")
	}
	return result, err
}
func (d *uncertainScopedCommit) PinCommandTargetIncarnation(commandID, target, incarnation string, refs []datastore.ResourceUpdateRef) error {
	if d.beforePin != nil {
		f := d.beforePin
		d.beforePin = nil
		f()
	}
	return d.CommandTargetIdentityWriter.PinCommandTargetIncarnation(commandID, target, incarnation, refs)
}
func startScopedActor(t *testing.T, ds datastore.Datastore, path string, overrides *plugin.ResourcePluginOverrides) *Metastructure {
	t.Helper()
	cfg := &pkgmodel.Config{Agent: pkgmodel.AgentConfig{Server: pkgmodel.ServerConfig{Nodename: "scope-" + util.RandomString(8), Hostname: "localhost"}, Datastore: pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.SqliteDatastore, Sqlite: pkgmodel.SqliteConfig{FilePath: path}}, Retry: pkgmodel.RetryConfig{MaxRetries: 1, RetryDelay: time.Millisecond, StatusCheckInterval: time.Millisecond}, Discovery: pkgmodel.DiscoveryConfig{Interval: time.Hour}}}
	ctx, cancel := testutil.PluginOverridesContext(overrides)
	t.Cleanup(cancel)
	m, err := NewMetastructureWithDataStoreAndContext(ctx, cfg, nil, nil, ds, "test")
	require.NoError(t, err)
	m.TestResourcePlugin = fakeaws.NewFakeAWS()
	require.NoError(t, m.Start())
	t.Cleanup(func() {
		if m.Node.IsAlive() {
			m.Stop(true)
		}
	})
	return m
}
func scopedActorForma() *pkgmodel.Forma {
	return &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "scope"}}, Targets: []pkgmodel.Target{{Label: "target", Namespace: "FakeAWS", Config: []byte(`{}`)}}, Resources: []pkgmodel.Resource{{Stack: "scope", Target: "target", Label: "resource", Type: "FakeAWS::S3::Bucket", Properties: []byte(`{"foo":"bar"}`), Schema: pkgmodel.Schema{Fields: []string{"foo"}}}}}
}

func TestScopedActorLostCommitRetryAndRestart(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/actor.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		wrapper := &uncertainScopedCommit{scopedReadBarrier: withScopedBarrier(ds, nil)}
		wrapper.lose.Store(true)
		var creates atomic.Int64
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		overrides := &plugin.ResourcePluginOverrides{Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
			creates.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: []byte(`{"foo":"bar"}`)}}, nil
		}, Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
			return &resource.ReadResult{ResourceType: request.ResourceType, Properties: `{"foo":"bar"}`}, nil
		}}
		m := startScopedActor(t, wrapper, path, overrides)
		forma := scopedActorForma()
		options := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}
		_, err = m.applyFormaWithKey(forma, options, "client", "authenticated-subject", "display", "key")
		require.ErrorContains(t, err, "lost commit response")
		require.Zero(t, creates.Load())
		commands, err := ds.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, commands, 1)
		original := commands[0]
		require.True(t, original.Setup.Committed)
		require.NotEmpty(t, original.Stacks[0].ID)
		first, err := m.applyFormaWithKey(forma, options, "client", "authenticated-subject", "display", "key")
		require.NoError(t, err)
		require.Equal(t, original.ID, first.CommandID)
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("original admitted provider work was stranded")
		}
		second, err := m.applyFormaWithKey(forma, options, "client", "authenticated-subject", "display", "key")
		require.NoError(t, err)
		require.Equal(t, original.ID, second.CommandID)
		require.EqualValues(t, 1, creates.Load())
		changed := *options
		changed.Message = "different payload"
		_, err = m.applyFormaWithKey(forma, &changed, "client", "authenticated-subject", "display", "key")
		require.ErrorIs(t, err, datastore.ErrAdmissionConflict)
		close(release)
		require.Eventually(t, func() bool {
			c, e := ds.GetFormaCommandByCommandID(original.ID)
			return e == nil && c.State == forma_command.CommandStateSuccess
		}, 5*time.Second, 10*time.Millisecond)
		require.Eventually(t, func() bool {
			reply, e := m.callActor(gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: m.Node.Name()}, changeset.DispatchAdmittedChangeset{CommandID: original.ID})
			if e != nil {
				return false
			}
			state, ok := reply.(changeset.AdmittedDispatchResult)
			return ok && !state.Owned
		}, time.Second, 10*time.Millisecond, "terminal command ownership should be retired")
		m.Stop(true)
		reopened, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "restart")
		require.NoError(t, err)
		restarted := startScopedActor(t, reopened, path, overrides)
		third, err := restarted.applyFormaWithKey(forma, options, "client", "authenticated-subject", "display", "key")
		require.NoError(t, err)
		require.Equal(t, original.ID, third.CommandID)
		require.EqualValues(t, 1, creates.Load())
		commands, err = reopened.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, commands, 1)
		require.Equal(t, original.Stacks, commands[0].Stacks)
	})
}

func TestScopedActorWriterBeforeAdmissionLeavesNoIntent(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/race.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		defer writer.Close()
		wrapper := &uncertainScopedCommit{scopedReadBarrier: withScopedBarrier(ds, nil), beforeAdmission: func() {
			_, e := writer.StoreResource(&pkgmodel.Resource{Ksuid: "phantom", Stack: "scope", Target: "target", Label: "phantom", Type: "FakeAWS::S3::Bucket", Managed: true, Properties: []byte(`{"foo":"concurrent"}`)}, "independent")
			require.NoError(t, e)
		}}
		var calls atomic.Int64
		m := startScopedActor(t, wrapper, path, &plugin.ResourcePluginOverrides{Create: func(*resource.CreateRequest) (*resource.CreateResult, error) { calls.Add(1); return nil, nil }})
		_, err = m.applyFormaWithKey(scopedActorForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "", "stale")
		require.ErrorIs(t, err, datastore.ErrStaleAdmission)
		require.Zero(t, calls.Load())
		commands, err := writer.LoadFormaCommands()
		require.NoError(t, err)
		require.Empty(t, commands)
		stack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		require.Nil(t, stack)
	})
}

func TestScopedActorMissingLocalPlanRecoversDurableIntent(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/missing-plan.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		wrapper := &uncertainScopedCommit{scopedReadBarrier: withScopedBarrier(ds, nil)}
		wrapper.lose.Store(true)
		var calls atomic.Int64
		m := startScopedActor(t, wrapper, path, &plugin.ResourcePluginOverrides{Create: func(*resource.CreateRequest) (*resource.CreateResult, error) { calls.Add(1); return nil, nil }})
		forma := scopedActorForma()
		options := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}
		_, err = m.applyFormaWithKey(forma, options, "client", "subject", "", "key")
		require.ErrorContains(t, err, "lost commit response")
		commands, err := ds.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, commands, 1)
		require.Contains(t, m.pendingApplyDispatch, commands[0].ID)
		delete(m.pendingApplyDispatch, commands[0].ID) // Models lost local dispatch inputs, without resetting provider state.
		replayed, err := m.applyFormaWithKey(forma, options, "client", "subject", "", "key")
		require.NoError(t, err)
		require.Equal(t, commands[0].ID, replayed.CommandID)
		require.Eventually(t, func() bool { return calls.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
		repeated, err := m.applyFormaWithKey(forma, options, "client", "subject", "", "key")
		require.NoError(t, err)
		require.Equal(t, replayed.CommandID, repeated.CommandID)
		require.EqualValues(t, 1, calls.Load())
	})
}

func TestScopedActorTargetRecreatedBeforeExecutionPinBlocksProvider(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/target-pin.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		defer writer.Close()
		wrapper := &uncertainScopedCommit{scopedReadBarrier: withScopedBarrier(ds, nil), beforePin: func() {
			_, e := writer.DeleteTarget("target")
			require.NoError(t, e)
			_, e = writer.CreateTarget(&pkgmodel.Target{Label: "target", Namespace: "FakeAWS", Config: []byte(`{}`)})
			require.NoError(t, e)
		}}
		var calls atomic.Int64
		m := startScopedActor(t, wrapper, path, &plugin.ResourcePluginOverrides{Create: func(*resource.CreateRequest) (*resource.CreateResult, error) { calls.Add(1); return nil, nil }})
		_, err = m.ApplyForma(scopedActorForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			commands, e := writer.LoadFormaCommands()
			return e == nil && len(commands) == 1 && commands[0].IsInFinalState()
		}, 5*time.Second, 10*time.Millisecond)
		require.Zero(t, calls.Load(), "unverifiable committed target must fail before provider dispatch")
		finished, e := writer.LoadFormaCommands()
		require.NoError(t, e)
		require.Len(t, finished, 1)
		require.Len(t, finished[0].TargetUpdates, 1)
		require.Contains(t, finished[0].TargetUpdates[0].ErrorMessage, "incarnation")
		rows, err := writer.LoadResourcesByStack("scope")
		require.NoError(t, err)
		require.Empty(t, rows)
	})
}
