//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

// admissionBoundaryDatastore changes only the post-certification admission
// window. Refreshing the supplied guard revisions models removal of that final
// optimistic check while retaining the real planner, persister and datastore.
type admissionBoundaryDatastore struct {
	*scopedReadBarrier
	beforeAdmission func() error
	refreshGuards   bool
}

// finalizationStoreBarrier pauses only the full command re-store which closes
// the real persister's last-resource-terminal/command-terminal handoff.
type finalizationStoreBarrier struct {
	*scopedReadBarrier
	commandID string
	entered   chan struct{}
	release   chan struct{}
	blocked   atomic.Bool
}

func (d *finalizationStoreBarrier) StoreFormaCommand(command *forma_command.FormaCommand, commandID string) error {
	if commandID == d.commandID && command.IsInFinalState() && d.blocked.CompareAndSwap(false, true) {
		close(d.entered)
		<-d.release
	}
	return d.Datastore.StoreFormaCommand(command, commandID)
}

func (d *admissionBoundaryDatastore) AdmitFormaCommand(command *forma_command.FormaCommand, admission datastore.CommandAdmission) (datastore.AdmissionResult, error) {
	if d.beforeAdmission != nil {
		before := d.beforeAdmission
		d.beforeAdmission = nil
		if err := before(); err != nil {
			return datastore.AdmissionResult{}, err
		}
	}
	if d.refreshGuards {
		keys := make([]string, len(admission.Guards))
		for i := range admission.Guards {
			keys[i] = admission.Guards[i].Key
		}
		guards, err := d.CommandAdmitter.ReadAdmissionRevisions(keys)
		if err != nil {
			return datastore.AdmissionResult{}, err
		}
		admission.Guards = guards
	}
	return d.CommandAdmitter.AdmitFormaCommand(command, admission)
}

func admissionBoundaryForma(stack, label, value string) *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: stack}},
		Targets: []pkgmodel.Target{{Label: "target", Namespace: "FakeAWS", Config: []byte(`{}`)}},
		Resources: []pkgmodel.Resource{{
			Stack: stack, Target: "target", Label: label, Type: "FakeAWS::S3::Bucket",
			Properties: []byte(`{"foo":"` + value + `"}`), Schema: pkgmodel.Schema{Fields: []string{"foo"}},
		}},
	}
}

func waitForAdmissionBoundaryCommand(t *testing.T, ds datastore.Datastore, commandID string) *forma_command.FormaCommand {
	t.Helper()
	var command *forma_command.FormaCommand
	require.Eventually(t, func() bool {
		var err error
		command, err = ds.GetFormaCommandByCommandID(commandID)
		return err == nil && command != nil && command.IsInFinalState()
	}, 5*time.Second, 10*time.Millisecond)
	return command
}

// Removing the active-command check would let competitor reach its provider
// create while busy-resource is held. The free-stack create is the negative
// control proving the held provider call does not globally stop useful work.
func TestAdmissionBoundariesSameStackConflictAllowsIndependentProgress(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/actor.db"
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}, "test")
		require.NoError(t, err)

		busyEntered := make(chan struct{})
		releaseBusy := make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(releaseBusy) })
		var writesMu sync.Mutex
		var writes []string
		overrides := &plugin.ResourcePluginOverrides{Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
			writesMu.Lock()
			writes = append(writes, request.Label)
			writesMu.Unlock()
			if request.Label == "busy-resource" {
				close(busyEntered)
				<-releaseBusy
			}
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
				Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess,
				NativeID: request.Label, ResourceProperties: request.Properties,
			}}, nil
		}}
		m := startScopedActor(t, ds, path, overrides)

		busy, err := m.ApplyForma(admissionBoundaryForma("busy", "busy-resource", "one"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		select {
		case <-busyEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("busy command did not reach the provider barrier")
		}

		_, err = m.ApplyForma(admissionBoundaryForma("busy", "competitor", "two"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		var conflict apimodel.FormaConflictingCommandsError
		require.ErrorAs(t, err, &conflict)
		require.Len(t, conflict.ConflictingCommands, 1)
		require.Equal(t, busy.CommandID, conflict.ConflictingCommands[0].CommandID)

		independent, err := m.ApplyForma(admissionBoundaryForma("free", "free-resource", "three"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, ds, independent.CommandID).State)

		writesMu.Lock()
		gotWrites := append([]string(nil), writes...)
		writesMu.Unlock()
		require.ElementsMatch(t, []string{"busy-resource", "free-resource"}, gotWrites)

		releaseOnce.Do(func() { close(releaseBusy) })
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, ds, busy.CommandID).State)
		commands, err := ds.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, commands, 2, "rejected competitor must not leave accepted intent")
		acceptedIDs := []string{commands[0].ID, commands[1].ID}
		require.ElementsMatch(t, []string{busy.CommandID, independent.CommandID}, acceptedIDs)
	})
}

// The terminal resource update remains excluded from accepted desired intent
// while its command is InProgress. Finalizing only the command after a later
// apply has planned must invalidate that plan, because finalization makes the
// terminal declaration eligible and changes the planning baseline.
func TestAdmissionBoundariesTerminalResourceBeforeCommandFinalizationInvalidatesPlan(t *testing.T) {
	m, writer, forma, options := scopedFixture(t)
	stack, err := writer.GetStackByLabel("a")
	require.NoError(t, err)
	require.NotNil(t, stack)
	current, err := writer.LoadResourceById("a")
	require.NoError(t, err)
	require.NotNil(t, current)
	storeDesired(t, writer, *current, resource_update.OperationCreate, forma_command.CommandStateSuccess)
	desired := *current
	desired.Properties = []byte(`{"name":"terminal-resource"}`)
	now := time.Now().UTC()
	finishing := &forma_command.FormaCommand{
		ID: util.NewID(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser,
		Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		State:  forma_command.CommandStateInProgress, StartTs: now, ModifiedTs: now,
		Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}},
		ResourceUpdates: []resource_update.ResourceUpdate{{
			DesiredState: desired, Version: current.Version, StackLabel: stack.Label,
			Operation: resource_update.OperationUpdate, Source: resource_update.FormaCommandSourceUser,
			State: resource_update.ResourceUpdateStateSuccess,
		}},
	}
	require.NoError(t, writer.StoreFormaCommand(finishing, finishing.ID))

	extracted, err := m.ExtractDesiredStacks("stack:a")
	require.NoError(t, err)
	require.Len(t, extracted.Stacks, 1)
	require.Len(t, extracted.Resources, 1)
	require.JSONEq(t, `{"name":"before"}`, string(extracted.Resources[0].Properties), "InProgress command intent remains excluded even after its last resource becomes terminal")

	plan, err := m.prepareGuardedApply(forma, options, "client", "subject", "")
	require.NoError(t, err, "a command with no unfinished resource operations does not conflict")
	require.NoError(t, writer.UpdateFormaCommandProgress(finishing.ID, forma_command.CommandStateSuccess, now.Add(time.Second)))
	extracted, err = m.ExtractDesiredStacks("stack:a")
	require.NoError(t, err)
	require.Len(t, extracted.Resources, 1)
	require.JSONEq(t, `{"name":"terminal-resource"}`, string(extracted.Resources[0].Properties), "command finalization makes the terminal declaration accepted desired intent")
	beforeRejectedAdmission, err := writer.LoadFormaCommands()
	require.NoError(t, err)
	require.ErrorIs(t, admitScopedPlan(t, m, plan), datastore.ErrStaleAdmission)
	afterRejectedAdmission, err := writer.LoadFormaCommands()
	require.NoError(t, err)
	require.Len(t, afterRejectedAdmission, len(beforeRejectedAdmission), "stale plan must leave no candidate intent")
	beforeIDs := make([]string, len(beforeRejectedAdmission))
	for i := range beforeRejectedAdmission {
		beforeIDs[i] = beforeRejectedAdmission[i].ID
	}
	afterIDs := make([]string, len(afterRejectedAdmission))
	for i := range afterRejectedAdmission {
		afterIDs[i] = afterRejectedAdmission[i].ID
	}
	require.ElementsMatch(t, beforeIDs, afterIDs)

	freshPlan, err := m.prepareGuardedApply(forma, options, "client", "subject", "")
	require.NoError(t, err)
	require.NoError(t, admitScopedPlan(t, m, freshPlan), "a plan certified after finalization remains admissible")
}

// Removing planning invalidation from the real persister's final full command
// re-store would admit the candidate planned while the finishing command was
// still excluded from accepted desired state.
func TestAdmissionBoundariesPersisterFinalizationInvalidatesConcurrentPlan(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/persister-finalization.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "actor")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		plannerDS, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "planner")
		require.NoError(t, err)
		t.Cleanup(func() { plannerDS.Close() })

		stackID, err := writer.CreateStack(&pkgmodel.Stack{Label: "handoff"}, "seed")
		require.NoError(t, err)
		_, err = writer.CreateTarget(&pkgmodel.Target{Label: "target", Namespace: "FakeAWS", Config: []byte(`{}`)})
		require.NoError(t, err)
		before := admissionBoundaryForma("handoff", "resource", "before").Resources[0]
		before.Ksuid = util.NewID()
		before.Managed = true
		before.NativeID = "native-resource"
		_, err = writer.StoreResource(&before, "seed")
		require.NoError(t, err)
		beforeRow, err := writer.LoadResourceById(before.Ksuid)
		require.NoError(t, err)
		require.NotNil(t, beforeRow)
		storeDesired(t, writer, *beforeRow, resource_update.OperationCreate, forma_command.CommandStateSuccess)

		barrier := &finalizationStoreBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			entered:           make(chan struct{}),
			release:           make(chan struct{}),
		}
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(barrier.release) })
		m := startScopedActor(t, barrier, path, &plugin.ResourcePluginOverrides{})
		_, err = testutil.StartTestHelperActor(m.Node, make(chan any, 1))
		require.NoError(t, err)

		terminal := *beforeRow
		terminal.Properties = []byte(`{"foo":"terminal"}`)
		now := time.Now().UTC()
		finishing := forma_command.FormaCommand{
			ID: util.NewID(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser,
			Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			State:  forma_command.CommandStateInProgress, StartTs: now, ModifiedTs: now,
			Stacks: []forma_command.CommandStack{{ID: stackID, Label: "handoff"}},
			ResourceUpdates: []resource_update.ResourceUpdate{{
				DesiredState: terminal, PriorState: *beforeRow, Version: beforeRow.Version,
				StackLabel: "handoff", Operation: resource_update.OperationUpdate,
				Source: resource_update.FormaCommandSourceUser, State: resource_update.ResourceUpdateStateNotStarted,
			}},
		}
		stored, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{Command: finishing})
		require.NoError(t, err)
		require.True(t, stored.(forma_persister.CommandPersistResult).OK)
		barrier.commandID = finishing.ID

		// The provider/result persistence precedes the completion handoff in the
		// workflow. The command remains InProgress until the persister's full
		// re-store below is released.
		_, err = writer.StoreResource(&terminal, finishing.ID)
		require.NoError(t, err)
		type completionResult struct {
			response any
			err      error
		}
		completed := make(chan completionResult, 1)
		go func() {
			response, callErr := testutil.Call(m.Node, "FormaCommandPersister", messages.MarkResourceUpdateAsComplete{
				CommandID: finishing.ID, ResourceURI: terminal.URI(), Operation: resource_update.OperationUpdate,
				FinalState: resource_update.ResourceUpdateStateSuccess, ResourceStartTs: now, ResourceModifiedTs: now.Add(time.Second),
			})
			completed <- completionResult{response: response, err: callErr}
		}()
		select {
		case <-barrier.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("persister did not reach the final full command store")
		}

		persistedGap, err := plannerDS.GetFormaCommandByCommandID(finishing.ID)
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateInProgress, persistedGap.State)
		require.Len(t, persistedGap.ResourceUpdates, 1)
		require.Equal(t, resource_update.ResourceUpdateStateSuccess, persistedGap.ResourceUpdates[0].State)
		planner := &Metastructure{Datastore: plannerDS, Cfg: &pkgmodel.Config{}}
		extracted, err := planner.ExtractDesiredStacks("stack:handoff")
		require.NoError(t, err)
		require.Len(t, extracted.Resources, 1)
		require.JSONEq(t, `{"foo":"before"}`, string(extracted.Resources[0].Properties), "the InProgress command is not accepted desired intent")

		candidate := admissionBoundaryForma("handoff", "resource", "candidate")
		plan, err := planner.prepareGuardedApply(candidate, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Force: true}, "client", "subject", "")
		require.NoError(t, err, "the terminal resource update leaves no active provider work to conflict")
		beforeAdmission, err := plannerDS.LoadFormaCommands()
		require.NoError(t, err)

		releaseOnce.Do(func() { close(barrier.release) })
		select {
		case result := <-completed:
			require.NoError(t, result.err)
			require.True(t, result.response.(forma_persister.CommandPersistResult).OK)
		case <-time.After(5 * time.Second):
			t.Fatal("persister finalization did not complete after release")
		}
		finalized, err := plannerDS.GetFormaCommandByCommandID(finishing.ID)
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, finalized.State)

		require.ErrorIs(t, admitScopedPlan(t, planner, plan), datastore.ErrStaleAdmission)
		afterAdmission, err := plannerDS.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, afterAdmission, len(beforeAdmission), "stale admission must leave no candidate intent")
		for _, command := range afterAdmission {
			require.NotEqual(t, plan.Command.ID, command.ID)
		}

		extracted, err = planner.ExtractDesiredStacks("stack:handoff")
		require.NoError(t, err)
		require.Len(t, extracted.Resources, 1)
		require.JSONEq(t, `{"foo":"terminal"}`, string(extracted.Resources[0].Properties), "finalization makes the literal terminal declaration accepted")
		fresh, err := planner.prepareGuardedApply(candidate, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Force: true}, "client", "subject", "")
		require.NoError(t, err)
		require.Len(t, fresh.Command.ResourceUpdates, 1)
		require.JSONEq(t, `{"foo":"terminal"}`, string(fresh.Command.ResourceUpdates[0].PriorState.Properties), "fresh planning reads the provider state committed by the finishing command")
	})
}

// A reviewed absorption is bound to the observation shown in its preview. If
// that observation changes before public submission, the public ApplyForma path
// must reject without admitting new intent or dispatching a provider write.
func TestAdmissionBoundariesReviewedAbsorptionRejectsChangedObservationBeforeSubmit(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/pre-submit-review.db"
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}, "test")
		require.NoError(t, err)
		var writes atomic.Int64
		m := startScopedActor(t, ds, path, &plugin.ResourcePluginOverrides{
			Create: func(*resource.CreateRequest) (*resource.CreateResult, error) { writes.Add(1); return nil, nil },
			Update: func(*resource.UpdateRequest) (*resource.UpdateResult, error) { writes.Add(1); return nil, nil },
			Delete: func(*resource.DeleteRequest) (*resource.DeleteResult, error) { writes.Add(1); return nil, nil },
		})

		_, err = ds.CreateStack(&pkgmodel.Stack{Label: "review"}, "seed")
		require.NoError(t, err)
		_, err = ds.CreateTarget(&pkgmodel.Target{Label: "target", Namespace: "FakeAWS", Config: []byte(`{}`)})
		require.NoError(t, err)
		declared := admissionBoundaryForma("review", "resource", "before")
		row := declared.Resources[0]
		row.Ksuid = util.NewID()
		row.Managed = true
		row.NativeID = "native-resource"
		_, err = ds.StoreResource(&row, "seed")
		require.NoError(t, err)
		rowAtBaseline, err := ds.LoadResourceById(row.Ksuid)
		require.NoError(t, err)
		storeDesired(t, ds, *rowAtBaseline, resource_update.OperationCreate, forma_command.CommandStateSuccess)
		commands, err := ds.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, commands, 1)
		commands[0].ResourceUpdates[0].Version = rowAtBaseline.Version
		require.NoError(t, ds.StoreFormaCommand(commands[0], commands[0].ID))

		firstObservation := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandSync, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
		require.NoError(t, ds.StoreFormaCommand(firstObservation, firstObservation.ID))
		rowAtBaseline.Properties = []byte(`{"foo":"first-drift"}`)
		_, err = ds.StoreResource(rowAtBaseline, firstObservation.ID)
		require.NoError(t, err)
		rejected := observeResolution(t, m, declared)
		options := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{
			ObservationID: rejected.ObservationID,
			Decisions:     []pkgmodel.DriftDecision{{ResourceID: row.Ksuid, Action: "absorb"}},
		}}
		preview, err := m.ApplyForma(declared, options, "client", "subject", "")
		require.NoError(t, err)
		require.NotNil(t, preview.Review)

		secondObservation := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandSync, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
		require.NoError(t, ds.StoreFormaCommand(secondObservation, secondObservation.ID))
		rowAtBaseline.Properties = []byte(`{"foo":"second-drift"}`)
		_, err = ds.StoreResource(rowAtBaseline, secondObservation.ID)
		require.NoError(t, err)
		beforeCommands, err := ds.LoadFormaCommands()
		require.NoError(t, err)
		beforeWrites := writes.Load()

		options.Simulate = false
		options.Resolution.ReviewID = preview.Review.ReviewID
		options.Resolution.IdempotencyKey = "changed-before-submit"
		_, err = m.ApplyForma(declared, options, "client", "subject", "")
		var stale apimodel.DriftResolutionError
		require.ErrorAs(t, err, &stale)
		require.Equal(t, "stale-review", stale.Code)
		afterCommands, loadErr := ds.LoadFormaCommands()
		require.NoError(t, loadErr)
		require.Len(t, afterCommands, len(beforeCommands), "stale review must leave no newly accepted command")
		require.Equal(t, beforeWrites, writes.Load(), "stale review must not dispatch a provider write")
	})
}

type absorptionBoundaryFixture struct {
	m       *Metastructure
	ds      datastore.Datastore
	wrapper *admissionBoundaryDatastore
	forma   *pkgmodel.Forma
	opts    *config.FormaCommandConfig
	row     *pkgmodel.Resource
	syncID  string
	calls   *atomic.Int64
}

func newAbsorptionBoundaryFixture(t *testing.T) absorptionBoundaryFixture {
	t.Helper()
	path := t.TempDir() + "/absorption.db"
	cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
	require.NoError(t, err)
	t.Cleanup(func() { writer.Close() })
	wrapper := &admissionBoundaryDatastore{scopedReadBarrier: withScopedBarrier(ds, nil)}
	var calls atomic.Int64
	overrides := &plugin.ResourcePluginOverrides{
		Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
			calls.Add(1)
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: request.Properties}}, nil
		},
		Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
			calls.Add(1)
			return &resource.ReadResult{ResourceType: request.ResourceType, Properties: `{"foo":"declared"}`}, nil
		},
	}
	m := startScopedActor(t, wrapper, path, overrides)
	forma := admissionBoundaryForma("reviewed", "resource", "declared")
	initial, err := m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
	require.NoError(t, err)
	require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
	rows, err := writer.LoadResourcesByStack("reviewed")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	syncID := util.NewID()
	now := time.Now().UTC()
	syncCommand := &forma_command.FormaCommand{ID: syncID, StartTs: now, ModifiedTs: now, Command: pkgmodel.CommandSync, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
	require.NoError(t, writer.StoreFormaCommand(syncCommand, syncID))
	rows[0].Properties = []byte(`{"foo":"first-drift"}`)
	_, err = writer.StoreResource(rows[0], syncID)
	require.NoError(t, err)
	rejected := observeResolution(t, m, forma)
	opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{
		ObservationID: rejected.ObservationID,
		Decisions:     []pkgmodel.DriftDecision{{ResourceID: rows[0].Ksuid, Action: "absorb"}},
	}}
	preview, err := m.ApplyForma(forma, opts, "client", "subject", "")
	require.NoError(t, err)
	opts.Simulate = false
	opts.Resolution.ReviewID = preview.Review.ReviewID
	opts.Resolution.IdempotencyKey = "reviewed-absorption"
	return absorptionBoundaryFixture{m: m, ds: writer, wrapper: wrapper, forma: forma, opts: opts, row: rows[0], syncID: syncID, calls: &calls}
}

// Refreshing the certified guards immediately before AdmitFormaCommand is the
// counterfactual: it accepts an absorption whose observed input changed after
// review. Normal guards reject the same exact interleaving; no concurrent
// write is the negative control and remains a metadata-only acceptance.
func TestAdmissionBoundariesReviewedAbsorptionNeedsPostCertificationGuard(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mutate        bool
		refreshGuards bool
		wantStale     bool
	}{
		{name: "unchanged review", mutate: false, wantStale: false},
		{name: "observation changes before admission", mutate: true, wantStale: true},
		{name: "refreshed guard ablation accepts stale review", mutate: true, refreshGuards: true, wantStale: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
				fixture := newAbsorptionBoundaryFixture(t)
				beforeCalls := fixture.calls.Load()
				beforeCommands, err := fixture.ds.LoadFormaCommands()
				require.NoError(t, err)
				if tc.mutate {
					changed := *fixture.row
					changed.Properties = []byte(`{"foo":"second-drift"}`)
					fixture.wrapper.beforeAdmission = func() error {
						_, err := fixture.ds.StoreResource(&changed, fixture.syncID)
						return err
					}
				}
				fixture.wrapper.refreshGuards = tc.refreshGuards

				accepted, err := fixture.m.ApplyForma(fixture.forma, fixture.opts, "client", "subject", "")
				if tc.wantStale {
					require.ErrorIs(t, err, datastore.ErrStaleAdmission)
					afterCommands, loadErr := fixture.ds.LoadFormaCommands()
					require.NoError(t, loadErr)
					require.Len(t, afterCommands, len(beforeCommands), "stale review must leave no accepted intent")
				} else {
					require.NoError(t, err)
					require.NotNil(t, accepted)
					stored := waitForAdmissionBoundaryCommand(t, fixture.ds, accepted.CommandID)
					require.Equal(t, forma_command.CommandStateSuccess, stored.State)
					require.Len(t, stored.ResourceUpdates, 1)
					require.Equal(t, "accept", string(stored.ResourceUpdates[0].Operation))
					require.JSONEq(t, `{"foo":"first-drift"}`, string(stored.ResourceUpdates[0].DesiredState.Properties), "accepted intent must contain the observation reviewed before admission")
				}
				require.Equal(t, beforeCalls, fixture.calls.Load(), "absorption admission must not call the provider")
				if tc.refreshGuards {
					current, loadErr := fixture.ds.LoadResourceById(fixture.row.Ksuid)
					require.NoError(t, loadErr)
					require.JSONEq(t, `{"foo":"second-drift"}`, string(current.Properties))
				}
			})
		})
	}
}
