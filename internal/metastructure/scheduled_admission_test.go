//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

type expiredStackReadBarrier struct {
	*scopedReadBarrier
	blockNext atomic.Bool
	observed  chan []datastore.ExpiredStackInfo
	release   chan struct{}

	blockTargetsNext  atomic.Bool
	targetsObserved   chan struct{}
	targetsRelease    chan struct{}
	activeObserved    chan string
	beforeAdmission   func() error
	beforeRetirement  func() error
	admissionResult   chan error
	rotationObserved  chan struct{}
	observeRotations  atomic.Bool
	blockRotationNext atomic.Bool
	rotationRelease   chan struct{}
}

func (d *expiredStackReadBarrier) GetExpiredStacks() ([]datastore.ExpiredStackInfo, error) {
	expired, err := d.Datastore.GetExpiredStacks()
	if err != nil {
		return nil, err
	}
	if d.observed != nil {
		select {
		case d.observed <- append([]datastore.ExpiredStackInfo(nil), expired...):
		default:
		}
	}
	if d.blockNext.CompareAndSwap(true, false) {
		<-d.release
	}
	return expired, nil
}

func (d *expiredStackReadBarrier) TryRetireEmptyStack(expectedStackID, label, cleanupCommandID string) (bool, error) {
	return d.Datastore.(datastore.EmptyStackRetirer).TryRetireEmptyStack(expectedStackID, label, cleanupCommandID)
}

func (d *expiredStackReadBarrier) TryRetireExpiredEmptyStack(candidate datastore.ExpiredStackInfo, expected []datastore.RevisionGuard, cleanupCommandID string) (bool, error) {
	if d.beforeRetirement != nil {
		f := d.beforeRetirement
		d.beforeRetirement = nil
		if err := f(); err != nil {
			return false, err
		}
	}
	return d.Datastore.(datastore.ExpiredEmptyStackRetirer).TryRetireExpiredEmptyStack(candidate, expected, cleanupCommandID)
}

func (d *expiredStackReadBarrier) AdmitFormaCommand(command *forma_command.FormaCommand, admission datastore.CommandAdmission) (datastore.AdmissionResult, error) {
	if d.beforeAdmission != nil {
		f := d.beforeAdmission
		d.beforeAdmission = nil
		if err := f(); err != nil {
			if d.admissionResult != nil {
				d.admissionResult <- err
			}
			return datastore.AdmissionResult{}, err
		}
	}
	result, err := d.CommandAdmitter.AdmitFormaCommand(command, admission)
	if d.admissionResult != nil {
		d.admissionResult <- err
	}
	return result, err
}

func (d *expiredStackReadBarrier) LoadAllTargets() ([]*pkgmodel.Target, error) {
	targets, err := d.Datastore.LoadAllTargets()
	if err == nil && d.blockTargetsNext.CompareAndSwap(true, false) {
		d.targetsObserved <- struct{}{}
		<-d.targetsRelease
	}
	return targets, err
}

func (d *expiredStackReadBarrier) StackHasActiveCommands(label string) (bool, error) {
	active, err := d.Datastore.StackHasActiveCommands(label)
	if d.activeObserved != nil {
		d.activeObserved <- label
	}
	return active, err
}

func (d *expiredStackReadBarrier) GetGeneratorsWithRotation() ([]datastore.GeneratorRotationInfo, error) {
	infos, err := d.Datastore.GetGeneratorsWithRotation()
	if d.rotationObserved != nil && d.observeRotations.Load() {
		select {
		case d.rotationObserved <- struct{}{}:
		default:
		}
	}
	if d.blockRotationNext.CompareAndSwap(true, false) {
		<-d.rotationRelease
	}
	return infos, err
}

func scheduledSecretSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Id",
		Fields:     []string{"Name", "Description", "SecretString", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"SecretString": {Opaque: true},
		},
	}
}

func stackExpirerCommands(t *testing.T, ds datastore.Datastore) []*forma_command.FormaCommand {
	t.Helper()
	commands, err := ds.LoadFormaCommands()
	require.NoError(t, err)
	var result []*forma_command.FormaCommand
	for _, command := range commands {
		if command.Source == forma_command.SourceStackExpirer {
			result = append(result, command)
		}
	}
	return result
}

// The certified expired-stack reread must reject a stale candidate after a
// same-stack apply has already started, then a later sweep may retry it.
func TestScheduledAdmissionExpiredCandidateWaitsForBusyStackAndRetries(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/ttl-race.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })

		barrier := &expiredStackReadBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			observed:          make(chan []datastore.ExpiredStackInfo, 4),
			release:           make(chan struct{}),
		}
		var releaseReadOnce sync.Once
		t.Cleanup(func() { releaseReadOnce.Do(func() { close(barrier.release) }) })

		updateEntered := make(chan struct{})
		releaseUpdate := make(chan struct{})
		deleteEntered := make(chan struct{}, 1)
		var updateActive atomic.Bool
		var deleteOverlapped atomic.Bool
		var releaseUpdateOnce sync.Once
		t.Cleanup(func() { releaseUpdateOnce.Do(func() { close(releaseUpdate) }) })
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess,
					NativeID: request.Label, ResourceProperties: request.Properties,
				}}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				updateActive.Store(true)
				close(updateEntered)
				<-releaseUpdate
				updateActive.Store(false)
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation: resource.OperationUpdate, OperationStatus: resource.OperationStatusSuccess,
					NativeID: request.NativeID, ResourceProperties: request.DesiredProperties,
				}}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				deleteOverlapped.Store(updateActive.Load())
				deleteEntered <- struct{}{}
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation: resource.OperationDelete, OperationStatus: resource.OperationStatusSuccess,
					NativeID: request.NativeID,
				}}, nil
			},
		}
		m := startScopedActor(t, barrier, path, overrides)
		t.Cleanup(func() {
			releaseReadOnce.Do(func() { close(barrier.release) })
			releaseUpdateOnce.Do(func() { close(releaseUpdate) })
		})
		initial, err := m.ApplyForma(scopedActorForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		stack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		require.NotNil(t, stack)
		_, err = writer.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "expired", ExpiresAt: time.Now().UTC().Add(-time.Hour),
			OnDependents: "cascade", StackID: stack.ID,
		}, "seed-expiry")
		require.NoError(t, err)
		seededStack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		require.Len(t, seededStack.Policies, 1)

		barrier.blockNext.Store(true)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		first := <-barrier.observed
		require.Len(t, first, 1)
		require.Equal(t, stack.ID, first[0].StackID)

		updated := scopedActorForma()
		updated.Stacks[0].Policies = seededStack.Policies
		updated.Resources[0].Properties = []byte(`{"foo":"updated"}`)
		applying, err := m.ApplyForma(updated, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Force: true}, "client", "subject", "")
		require.NoError(t, err)
		select {
		case <-updateEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("same-stack apply did not reach the held provider update")
		}

		currentStack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		require.Len(t, currentStack.Policies, 1)
		require.JSONEq(t, string(seededStack.Policies[0]), string(currentStack.Policies[0]))
		var currentTTL pkgmodel.TTLPolicy
		require.NoError(t, json.Unmarshal(currentStack.Policies[0], &currentTTL))
		require.True(t, currentTTL.ExpiresAt.Before(time.Now().UTC()))
		freshExpired, err := writer.GetExpiredStacks()
		require.NoError(t, err)
		require.Empty(t, freshExpired, "ordinary busy-stack expiry query must stay empty")

		releaseReadOnce.Do(func() { close(barrier.release) })
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		revalidation := <-barrier.observed
		require.Empty(t, revalidation, "certified revalidation must see the busy stack as ineligible")
		witness := <-barrier.observed
		require.Empty(t, witness, "the queued same-actor sweep witnesses completion and still sees the busy stack")
		require.Empty(t, stackExpirerCommands(t, writer), "the stale candidate must not leave accepted delete intent")
		require.False(t, deleteOverlapped.Load(), "the stale candidate must not overlap the held update")

		releaseUpdateOnce.Do(func() { close(releaseUpdate) })
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, applying.CommandID).State)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		third := <-barrier.observed
		require.Len(t, third, 1, "the ordinary later sweep must retry the still-expired stack")
		select {
		case <-deleteEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("retry did not reach the provider delete")
		}
		require.False(t, deleteOverlapped.Load())
		require.Eventually(t, func() bool {
			commands := stackExpirerCommands(t, writer)
			return len(commands) == 1 && commands[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 10*time.Millisecond)
	})
}

// The certified expired-stack reread must reject a stale expiry candidate
// after a user destroy starts, so the two deletes cannot overlap.
func TestScheduledAdmissionExpiredCandidateConflictsWithUserDestroy(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/ttl-user-destroy.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		barrier := &expiredStackReadBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			observed:          make(chan []datastore.ExpiredStackInfo, 3),
			release:           make(chan struct{}),
		}
		var releaseReadOnce sync.Once
		t.Cleanup(func() { releaseReadOnce.Do(func() { close(barrier.release) }) })
		deleteEntered := make(chan struct{}, 2)
		releaseDeletes := make(chan struct{})
		var releaseDeletesOnce sync.Once
		t.Cleanup(func() { releaseDeletesOnce.Do(func() { close(releaseDeletes) }) })
		var deletes atomic.Int64
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess,
					NativeID: request.Label, ResourceProperties: request.Properties,
				}}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				deletes.Add(1)
				deleteEntered <- struct{}{}
				<-releaseDeletes
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation: resource.OperationDelete, OperationStatus: resource.OperationStatusSuccess,
					NativeID: request.NativeID,
				}}, nil
			},
		}
		m := startScopedActor(t, barrier, path, overrides)
		t.Cleanup(func() {
			releaseReadOnce.Do(func() { close(barrier.release) })
			releaseDeletesOnce.Do(func() { close(releaseDeletes) })
		})
		initial, err := m.ApplyForma(scopedActorForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		stack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		_, err = writer.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "expired", ExpiresAt: time.Now().UTC().Add(-time.Hour),
			OnDependents: "cascade", StackID: stack.ID,
		}, "seed-expiry")
		require.NoError(t, err)

		barrier.blockNext.Store(true)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		first := <-barrier.observed
		require.Len(t, first, 1)

		userDestroy, err := m.DestroyForma(scopedActorForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		select {
		case <-deleteEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("user destroy did not reach the held provider delete")
		}

		releaseReadOnce.Do(func() { close(barrier.release) })
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		revalidation := <-barrier.observed
		require.Empty(t, revalidation, "certified revalidation must see the user destroy")
		witness := <-barrier.observed
		require.Empty(t, witness, "the queued same-actor sweep witnesses prior expiry-attempt completion")
		commands, err := writer.LoadFormaCommands()
		require.NoError(t, err)
		var destroys []*forma_command.FormaCommand
		for _, command := range commands {
			if command.Command == pkgmodel.CommandDestroy {
				destroys = append(destroys, command)
			}
		}
		require.Len(t, destroys, 1, "only one conflicting destroy may be admitted")
		require.Equal(t, userDestroy.CommandID, destroys[0].ID)
		require.Equal(t, forma_command.SourceUser, destroys[0].Source)
		require.Empty(t, stackExpirerCommands(t, writer))
		require.EqualValues(t, 1, deletes.Load())

		releaseDeletesOnce.Do(func() { close(releaseDeletes) })
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, userDestroy.CommandID).State)
	})
}

// The inverse submission order exercises the public caller boundary: once the
// scheduled expiry owns the stack, a user Destroy must receive the typed
// conflict that the HTTP layer maps to 409 and must not admit a second delete.
func TestScheduledAdmissionUserDestroyConflictsWithAdmittedExpiry(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/expiry-first.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		barrier := &expiredStackReadBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			observed:          make(chan []datastore.ExpiredStackInfo, 4),
			release:           make(chan struct{}),
		}
		close(barrier.release)
		deleteEntered := make(chan struct{}, 1)
		releaseDelete := make(chan struct{})
		var releaseDeleteOnce sync.Once
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: request.Properties}}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				deleteEntered <- struct{}{}
				<-releaseDelete
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationDelete, OperationStatus: resource.OperationStatusSuccess, NativeID: request.NativeID}}, nil
			},
		}
		m := startScopedActor(t, barrier, path, overrides)
		t.Cleanup(func() { releaseDeleteOnce.Do(func() { close(releaseDelete) }) })
		initial, err := m.ApplyForma(scopedActorForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		stack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		_, err = writer.CreatePolicy(&pkgmodel.TTLPolicy{Type: "ttl", Label: "expired", ExpiresAt: time.Now().UTC().Add(-time.Hour), OnDependents: "cascade", StackID: stack.ID}, "seed-expiry")
		require.NoError(t, err)

		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		require.Len(t, <-barrier.observed, 1)
		require.Len(t, <-barrier.observed, 1)
		select {
		case <-deleteEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("scheduled expiry did not reach the held provider delete")
		}

		_, err = m.DestroyForma(scopedActorForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		var conflict apimodel.FormaConflictingCommandsError
		require.ErrorAs(t, err, &conflict)
		require.NotEmpty(t, conflict.ConflictingCommands)
		commands, err := writer.LoadFormaCommands()
		require.NoError(t, err)
		var destroys []*forma_command.FormaCommand
		for _, command := range commands {
			if command.Command == pkgmodel.CommandDestroy {
				destroys = append(destroys, command)
			}
		}
		require.Len(t, destroys, 1)
		require.Equal(t, forma_command.SourceStackExpirer, destroys[0].Source)

		releaseDeleteOnce.Do(func() { close(releaseDelete) })
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, destroys[0].ID).State)
	})
}

// Removing expiry-predicate certification lets a stale candidate destroy a
// stack after an inline-only TTL extension has committed with no active RU row.
func TestScheduledAdmissionExpiredCandidateRejectsCommittedTTLExtension(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/ttl-extension.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })

		barrier := &expiredStackReadBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			observed:          make(chan []datastore.ExpiredStackInfo, 3),
			release:           make(chan struct{}),
		}
		var releaseReadOnce sync.Once
		t.Cleanup(func() { releaseReadOnce.Do(func() { close(barrier.release) }) })

		var deletes atomic.Int64
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess,
					NativeID: request.Label, ResourceProperties: request.Properties,
				}}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				deletes.Add(1)
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation: resource.OperationDelete, OperationStatus: resource.OperationStatusSuccess,
					NativeID: request.NativeID,
				}}, nil
			},
		}
		m := startScopedActor(t, barrier, path, overrides)
		t.Cleanup(func() { releaseReadOnce.Do(func() { close(barrier.release) }) })
		initial, err := m.ApplyForma(scopedActorForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		stack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		require.NotNil(t, stack)
		_, err = writer.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "expired", ExpiresAt: time.Now().UTC().Add(-time.Hour),
			OnDependents: "cascade", StackID: stack.ID,
		}, "seed-expiry")
		require.NoError(t, err)
		seededStack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		require.Len(t, seededStack.Policies, 1)

		barrier.blockNext.Store(true)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		first := <-barrier.observed
		require.Len(t, first, 1)
		require.Equal(t, stack.ID, first[0].StackID)

		var extended pkgmodel.TTLPolicy
		require.NoError(t, json.Unmarshal(seededStack.Policies[0], &extended))
		extended.ExpiresAt = time.Now().UTC().Add(time.Hour)
		extendedJSON, err := json.Marshal(&extended)
		require.NoError(t, err)
		updated := scopedActorForma()
		updated.Stacks[0].Policies = []json.RawMessage{extendedJSON}
		accepted, err := m.ApplyForma(updated, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		acceptedCommand := waitForAdmissionBoundaryCommand(t, writer, accepted.CommandID)
		require.Equal(t, forma_command.CommandStateSuccess, acceptedCommand.State)
		require.Empty(t, acceptedCommand.ResourceUpdates, "the committed TTL extension must leave no active resource row")
		require.Len(t, acceptedCommand.PolicyUpdates, 1)
		freshExpired, err := writer.GetExpiredStacks()
		require.NoError(t, err)
		require.Empty(t, freshExpired, "the committed TTL extension makes the stack ineligible")

		releaseReadOnce.Do(func() { close(barrier.release) })
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		revalidation := <-barrier.observed
		require.Empty(t, revalidation, "certified revalidation must reject the extended TTL")
		witness := <-barrier.observed
		require.Empty(t, witness, "the queued same-actor sweep witnesses completion after the extension")
		require.Empty(t, stackExpirerCommands(t, writer), "stale expiry must leave no accepted delete intent")
		require.Zero(t, deletes.Load(), "stale expiry must not reach the provider")
		retained, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		require.NotNil(t, retained)
		require.Equal(t, stack.ID, retained.ID)
		require.Len(t, retained.Policies, 1)
		var retainedTTL pkgmodel.TTLPolicy
		require.NoError(t, json.Unmarshal(retained.Policies[0], &retainedTTL))
		require.True(t, retainedTTL.ExpiresAt.After(time.Now().UTC()))
	})
}

// Removing expected-expiry validation from empty-stack retirement lets a stale
// candidate erase a stack and its newly extended TTL before persister admission.
func TestScheduledAdmissionEmptyExpiredCandidateRejectsCommittedTTLExtension(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/empty-ttl-extension.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		barrier := &expiredStackReadBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			observed:          make(chan []datastore.ExpiredStackInfo, 3),
			release:           make(chan struct{}),
		}
		var releaseReadOnce sync.Once
		t.Cleanup(func() { releaseReadOnce.Do(func() { close(barrier.release) }) })
		m := startScopedActor(t, barrier, path, &plugin.ResourcePluginOverrides{})
		t.Cleanup(func() { releaseReadOnce.Do(func() { close(barrier.release) }) })
		_, err = writer.CreateStack(&pkgmodel.Stack{Label: "empty"}, "seed-stack")
		require.NoError(t, err)
		stack, err := writer.GetStackByLabel("empty")
		require.NoError(t, err)
		require.NotNil(t, stack)
		expired := &pkgmodel.TTLPolicy{
			Type: "ttl", Label: "empty-expiry", ExpiresAt: time.Now().UTC().Add(-time.Hour),
			OnDependents: "abort", StackID: stack.ID,
		}
		_, err = writer.CreatePolicy(expired, "seed-policy")
		require.NoError(t, err)

		barrier.blockNext.Store(true)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		first := <-barrier.observed
		require.Len(t, first, 1)
		require.Equal(t, stack.ID, first[0].StackID)

		extended := *expired
		extended.ExpiresAt = time.Now().UTC().Add(time.Hour)
		_, err = writer.UpdatePolicy(&extended, "extend-policy")
		require.NoError(t, err)
		freshExpired, err := writer.GetExpiredStacks()
		require.NoError(t, err)
		require.Empty(t, freshExpired)

		releaseReadOnce.Do(func() { close(barrier.release) })
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		revalidation := <-barrier.observed
		require.Empty(t, revalidation, "certified revalidation must reject the extended TTL")
		witness := <-barrier.observed
		require.Empty(t, witness, "the queued same-actor sweep witnesses retirement-attempt completion")
		retained, err := writer.GetStackByLabel("empty")
		require.NoError(t, err)
		require.NotNil(t, retained, "a stale empty-stack candidate must not retire the extended stack")
		require.Equal(t, stack.ID, retained.ID)
		require.Len(t, retained.Policies, 1)
		var retainedTTL pkgmodel.TTLPolicy
		require.NoError(t, json.Unmarshal(retained.Policies[0], &retainedTTL))
		require.True(t, retainedTTL.ExpiresAt.After(time.Now().UTC()))
		require.Empty(t, stackExpirerCommands(t, writer))
	})
}

// Removing the expected stack ID check from atomic retirement lets an old
// expiry candidate tombstone a newer stack that reused the same label.
func TestScheduledAdmissionEmptyExpiredCandidatePreservesRecreatedStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/empty-ttl-incarnation.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		barrier := &expiredStackReadBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			observed:          make(chan []datastore.ExpiredStackInfo, 3),
			release:           make(chan struct{}),
		}
		var releaseReadOnce sync.Once
		t.Cleanup(func() { releaseReadOnce.Do(func() { close(barrier.release) }) })
		m := startScopedActor(t, barrier, path, &plugin.ResourcePluginOverrides{})
		t.Cleanup(func() { releaseReadOnce.Do(func() { close(barrier.release) }) })

		_, err = writer.CreateStack(&pkgmodel.Stack{Label: "recreated"}, "seed-stack")
		require.NoError(t, err)
		old, err := writer.GetStackByLabel("recreated")
		require.NoError(t, err)
		_, err = writer.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "old-expiry", ExpiresAt: time.Now().UTC().Add(-time.Hour),
			OnDependents: "abort", StackID: old.ID,
		}, "seed-policy")
		require.NoError(t, err)

		barrier.blockNext.Store(true)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		first := <-barrier.observed
		require.Len(t, first, 1)
		require.Equal(t, old.ID, first[0].StackID)

		_, err = writer.DeleteStack("recreated", "replace-delete")
		require.NoError(t, err)
		newStack := &pkgmodel.Stack{Label: "recreated"}
		_, err = writer.CreateStack(newStack, "replace-create")
		require.NoError(t, err)
		require.NotEqual(t, old.ID, newStack.ID)
		_, err = writer.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "new-expiry", ExpiresAt: time.Now().UTC().Add(time.Hour),
			OnDependents: "abort", StackID: newStack.ID,
		}, "replace-policy")
		require.NoError(t, err)

		releaseReadOnce.Do(func() { close(barrier.release) })
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		revalidation := <-barrier.observed
		require.Empty(t, revalidation, "certified revalidation must reject the old incarnation")
		witness := <-barrier.observed
		require.Empty(t, witness, "the queued same-actor sweep witnesses stale-candidate completion")
		retained, err := writer.GetStackByLabel("recreated")
		require.NoError(t, err)
		require.NotNil(t, retained)
		require.Equal(t, newStack.ID, retained.ID)
		require.Empty(t, stackExpirerCommands(t, writer))
	})
}

// Removing the exact-policy check from certified empty retirement could make
// the stale controls pass by suppressing all retirement, including eligible work.
func TestScheduledAdmissionEmptyExpiredCandidateRetiresWhenUnchanged(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/empty-ttl-success.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		barrier := &expiredStackReadBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			observed:          make(chan []datastore.ExpiredStackInfo, 3),
			release:           make(chan struct{}),
		}
		close(barrier.release)
		m := startScopedActor(t, barrier, path, &plugin.ResourcePluginOverrides{})
		_, err = writer.CreateStack(&pkgmodel.Stack{Label: "eligible-empty"}, "seed-stack")
		require.NoError(t, err)
		stack, err := writer.GetStackByLabel("eligible-empty")
		require.NoError(t, err)
		_, err = writer.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "expired", ExpiresAt: time.Now().UTC().Add(-time.Hour),
			OnDependents: "abort", StackID: stack.ID,
		}, "seed-policy")
		require.NoError(t, err)

		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		first := <-barrier.observed
		require.Len(t, first, 1)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		revalidation := <-barrier.observed
		require.Len(t, revalidation, 1, "certified revalidation must see the unchanged candidate")
		witness := <-barrier.observed
		require.Empty(t, witness, "the next actor sweep proves eligible retirement completed")
		retired, err := writer.GetStackByLabel("eligible-empty")
		require.NoError(t, err)
		require.Nil(t, retired)
	})
}

// Revalidation can succeed and the policy can still change before the
// persister transaction begins. The carried policy guard closes that last
// interval; a fresh reread alone does not.
func TestScheduledAdmissionExpiredCandidateRejectsTTLChangeAfterCertification(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/ttl-after-certification.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		barrier := &expiredStackReadBarrier{scopedReadBarrier: withScopedBarrier(ds, nil), observed: make(chan []datastore.ExpiredStackInfo, 4), release: make(chan struct{})}
		close(barrier.release)
		var deletes atomic.Int64
		m := startScopedActor(t, barrier, path, &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: request.Properties}}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				deletes.Add(1)
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationDelete, OperationStatus: resource.OperationStatusSuccess, NativeID: request.NativeID}}, nil
			},
		})
		initial, err := m.ApplyForma(scopedActorForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		stack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		policy := &pkgmodel.TTLPolicy{Type: "ttl", Label: "expired", ExpiresAt: time.Now().UTC().Add(-time.Hour), OnDependents: "cascade", StackID: stack.ID}
		_, err = writer.CreatePolicy(policy, "seed-expiry")
		require.NoError(t, err)
		barrier.beforeAdmission = func() error {
			extended := *policy
			extended.ExpiresAt = time.Now().UTC().Add(time.Hour)
			_, err := writer.UpdatePolicy(&extended, "extend-after-certificate")
			return err
		}

		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		require.Len(t, <-barrier.observed, 1)
		require.Len(t, <-barrier.observed, 1)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
	queuedSweep:
		for {
			select {
			case observed := <-barrier.observed:
				if len(observed) == 0 {
					break queuedSweep
				}
				// Planning may expand resource/target scope and recertify. The first
				// empty observation is the queued ordinary sweep.
			case <-time.After(5 * time.Second):
				t.Fatal("queued expiry sweep did not produce an empty observation")
			}
		}
		require.Empty(t, stackExpirerCommands(t, writer))
		require.Zero(t, deletes.Load())
	})
}

// Empty-stack cleanup has no command admission, so its own transaction must
// compare the certified policy guard before proving emptiness and tombstoning.
func TestScheduledAdmissionEmptyExpiredCandidateRejectsTTLChangeAfterCertification(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/empty-ttl-after-certification.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		barrier := &expiredStackReadBarrier{scopedReadBarrier: withScopedBarrier(ds, nil), observed: make(chan []datastore.ExpiredStackInfo, 4), release: make(chan struct{})}
		close(barrier.release)
		m := startScopedActor(t, barrier, path, &plugin.ResourcePluginOverrides{})
		_, err = writer.CreateStack(&pkgmodel.Stack{Label: "empty-final"}, "seed-stack")
		require.NoError(t, err)
		stack, err := writer.GetStackByLabel("empty-final")
		require.NoError(t, err)
		policy := &pkgmodel.TTLPolicy{Type: "ttl", Label: "expired", ExpiresAt: time.Now().UTC().Add(-time.Hour), OnDependents: "abort", StackID: stack.ID}
		_, err = writer.CreatePolicy(policy, "seed-expiry")
		require.NoError(t, err)
		barrier.beforeRetirement = func() error {
			extended := *policy
			extended.ExpiresAt = time.Now().UTC().Add(time.Hour)
			_, err := writer.UpdatePolicy(&extended, "extend-after-certificate")
			return err
		}

		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		require.Len(t, <-barrier.observed, 1)
		require.Len(t, <-barrier.observed, 1)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		require.Empty(t, <-barrier.observed, "next sweep witnesses guarded retirement rejection")
		retained, err := writer.GetStackByLabel("empty-final")
		require.NoError(t, err)
		require.NotNil(t, retained)
		require.Equal(t, stack.ID, retained.ID)
	})
}

// An abort-mode expiry must bind the absence of external consumers through
// final admission. A consumer created after dependency planning invalidates the
// topology guard; once present, fresh preparation is a deliberate nil/no-op.
func TestScheduledAdmissionExpiryRejectsCrossStackConsumerAddedAfterCertification(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/expiry-topology.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		barrier := &expiredStackReadBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			observed:          make(chan []datastore.ExpiredStackInfo, 8),
			release:           make(chan struct{}),
		}
		close(barrier.release)
		var deletes atomic.Int64
		m := startScopedActor(t, barrier, path, &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: request.Properties}}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				deletes.Add(1)
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationDelete, OperationStatus: resource.OperationStatusSuccess, NativeID: request.NativeID}}, nil
			},
		})
		initial, err := m.ApplyForma(scopedActorForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		parentRows, err := writer.LoadResourcesByStack("scope")
		require.NoError(t, err)
		require.Len(t, parentRows, 1)
		_, err = writer.CreateStack(&pkgmodel.Stack{Label: "consumer"}, "seed-consumer-stack")
		require.NoError(t, err)
		stack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		_, err = writer.CreatePolicy(&pkgmodel.TTLPolicy{Type: "ttl", Label: "expired", ExpiresAt: time.Now().UTC().Add(-time.Hour), OnDependents: "abort", StackID: stack.ID}, "seed-expiry")
		require.NoError(t, err)
		barrier.admissionResult = make(chan error, 1)
		barrier.beforeAdmission = func() error {
			consumer := &pkgmodel.Resource{
				Ksuid: util.NewID(), Stack: "consumer", Target: "target", Label: "consumer", Type: "FakeAWS::S3::Bucket",
				NativeID: "consumer", Managed: true, Properties: []byte(`{"foo":{"$ref":"formae://` + parentRows[0].Ksuid + `#/foo"}}`), Schema: pkgmodel.Schema{Fields: []string{"foo"}},
			}
			_, err := writer.StoreResource(consumer, "add-cross-stack-consumer")
			return err
		}

		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.StackExpirer, Node: m.Node.Name()}, CheckExpiredStacks{}))
		require.Len(t, <-barrier.observed, 1)
		require.Len(t, <-barrier.observed, 1)
		require.ErrorIs(t, <-barrier.admissionResult, datastore.ErrStaleAdmission)
		require.Empty(t, stackExpirerCommands(t, writer))
		require.Zero(t, deletes.Load())

		current, err := writer.GetExpiredStacks()
		require.NoError(t, err)
		require.Len(t, current, 1)
		certified, err := certifyExpiredStack(writer, current[0])
		require.NoError(t, err)
		require.NotNil(t, certified)
		require.False(t, certified.empty)
		require.Nil(t, certified.result, "an existing external dependent makes abort-mode expiry a safe no-op")
	})
}

// A scheduled ReconcileStack can finish its early busy check before a user
// destroy is admitted. Its scheduled guarded final check must reject that
// stale plan, and the ordinary next scheduled attempt must observe the busy
// command rather than writing concurrently.
func TestScheduledAdmissionReconcileConflictsWithUserDestroyAfterPlanningStarts(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/reconcile-destroy.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		wrapper := &expiredStackReadBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			targetsObserved:   make(chan struct{}, 1),
			targetsRelease:    make(chan struct{}),
			activeObserved:    make(chan string, 4),
		}
		deleteEntered := make(chan struct{}, 1)
		releaseDelete := make(chan struct{})
		var releaseTargetsOnce, releaseDeleteOnce sync.Once
		var updates atomic.Int64
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: request.Properties}}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				updates.Add(1)
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationUpdate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.NativeID, ResourceProperties: request.DesiredProperties}}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				deleteEntered <- struct{}{}
				<-releaseDelete
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationDelete, OperationStatus: resource.OperationStatusSuccess, NativeID: request.NativeID}}, nil
			},
		}
		m := startScopedActor(t, wrapper, path, overrides)
		t.Cleanup(func() {
			releaseTargetsOnce.Do(func() { close(wrapper.targetsRelease) })
			releaseDeleteOnce.Do(func() { close(releaseDelete) })
		})
		forma := scopedActorForma()
		forma.Stacks[0].Policies = []json.RawMessage{json.RawMessage(`{"Type":"auto-reconcile","Label":"automatic","IntervalSeconds":86400}`)}
		initial, err := m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		rows, err := writer.LoadResourcesByStack("scope")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		now := time.Now().UTC()
		driftID := "scheduled-reconcile-drift"
		require.NoError(t, writer.StoreFormaCommand(&forma_command.FormaCommand{ID: driftID, Command: pkgmodel.CommandSync, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess, StartTs: now, ModifiedTs: now}, driftID))
		rows[0].Properties = []byte(`{"foo":"drifted"}`)
		_, err = writer.StoreResource(rows[0], driftID)
		require.NoError(t, err)

		wrapper.blockTargetsNext.Store(true)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.AutoReconciler, Node: m.Node.Name()}, messages.RefreshEffectivePolicies{}))
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.AutoReconciler, Node: m.Node.Name()}, ReconcileStack{StackLabel: "scope"}))
		require.Equal(t, "scope", <-wrapper.activeObserved)
		select {
		case <-wrapper.targetsObserved:
		case <-time.After(5 * time.Second):
			t.Fatal("scheduled reconcile did not reach the held post-busy planning read")
		}

		userDestroy, err := m.DestroyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		select {
		case <-deleteEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("user destroy did not reach the held provider delete")
		}
		releaseTargetsOnce.Do(func() { close(wrapper.targetsRelease) })
		// A queued ordinary retry entering its active-command check proves the
		// stale planned attempt has returned from persister rejection.
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.AutoReconciler, Node: m.Node.Name()}, ReconcileStack{StackLabel: "scope"}))
		require.Equal(t, "scope", <-wrapper.activeObserved)
		commands, err := writer.LoadFormaCommands()
		require.NoError(t, err)
		var scheduled []*forma_command.FormaCommand
		for _, command := range commands {
			if command.Source == forma_command.SourceAutoReconciler {
				scheduled = append(scheduled, command)
			}
		}
		require.Empty(t, scheduled, "stale reconcile plan must leave no accepted intent")
		require.Zero(t, updates.Load(), "stale reconcile must not reach the provider")

		releaseDeleteOnce.Do(func() { close(releaseDelete) })
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, userDestroy.CommandID).State)
	})
}

func TestForceAutoReconcileMapsFinalPersisterConflict(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/force-reconcile-final-conflict.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		wrapper := &expiredStackReadBarrier{
			scopedReadBarrier: withScopedBarrier(ds, nil),
			targetsObserved:   make(chan struct{}, 1),
			targetsRelease:    make(chan struct{}),
		}
		var releaseOnce sync.Once
		m := startScopedActor(t, wrapper, path, &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: request.Properties}}, nil
			},
		})
		t.Cleanup(func() { releaseOnce.Do(func() { close(wrapper.targetsRelease) }) })
		forma := scopedActorForma()
		forma.Stacks[0].Policies = []json.RawMessage{json.RawMessage(`{"Type":"auto-reconcile","Label":"automatic","IntervalSeconds":86400}`)}
		initial, err := m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		rows, err := writer.LoadResourcesByStack("scope")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		now := time.Now().UTC()
		driftID := "force-reconcile-final-conflict-drift"
		require.NoError(t, writer.StoreFormaCommand(&forma_command.FormaCommand{ID: driftID, Command: pkgmodel.CommandSync, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess, StartTs: now, ModifiedTs: now}, driftID))
		rows[0].Properties = []byte(`{"foo":"drifted"}`)
		_, err = writer.StoreResource(rows[0], driftID)
		require.NoError(t, err)
		stack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)

		wrapper.blockTargetsNext.Store(true)
		result := make(chan error, 1)
		go func() {
			_, forceErr := m.ForceAutoReconcile("scope", "subject", "name")
			result <- forceErr
		}()
		select {
		case <-wrapper.targetsObserved:
		case <-time.After(5 * time.Second):
			t.Fatal("force reconcile did not reach the held post-busy planning read")
		}

		busy := forma_command.FormaCommand{
			ID: util.NewID(), Command: pkgmodel.CommandDestroy, Source: forma_command.SourceStackExpirer,
			State: forma_command.CommandStateInProgress, StartTs: now, ModifiedTs: now,
			Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}},
		}
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
			forma_persister.StoreNewFormaCommand{Command: busy},
		)
		require.NoError(t, err)
		releaseOnce.Do(func() { close(wrapper.targetsRelease) })

		forceErr := <-result
		var conflict apimodel.FormaConflictingCommandsError
		require.ErrorAs(t, forceErr, &conflict)
		require.Len(t, conflict.ConflictingCommands, 1)
		require.Equal(t, busy.ID, conflict.ConflictingCommands[0].CommandID)
	})
}

func TestScheduledAdmissionReconcileRejectsPolicyRemovalAfterCertification(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/reconcile-policy-final.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		wrapper := &expiredStackReadBarrier{scopedReadBarrier: withScopedBarrier(ds, nil)}
		var updates atomic.Int64
		m := startScopedActor(t, wrapper, path, &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: request.Properties}}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				updates.Add(1)
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationUpdate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.NativeID, ResourceProperties: request.DesiredProperties}}, nil
			},
		})
		forma := scopedActorForma()
		forma.Stacks[0].Policies = []json.RawMessage{json.RawMessage(`{"Type":"auto-reconcile","Label":"automatic","IntervalSeconds":86400}`)}
		initial, err := m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		rows, err := writer.LoadResourcesByStack("scope")
		require.NoError(t, err)
		now := time.Now().UTC()
		driftID := "policy-final-drift"
		require.NoError(t, writer.StoreFormaCommand(&forma_command.FormaCommand{ID: driftID, Command: pkgmodel.CommandSync, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess, StartTs: now, ModifiedTs: now}, driftID))
		rows[0].Properties = []byte(`{"foo":"drifted"}`)
		_, err = writer.StoreResource(rows[0], driftID)
		require.NoError(t, err)
		storedStack, err := writer.GetStackByLabel("scope")
		require.NoError(t, err)
		require.Len(t, storedStack.Policies, 1)
		var storedPolicy struct{ Label string }
		require.NoError(t, json.Unmarshal(storedStack.Policies[0], &storedPolicy))
		require.NotEmpty(t, storedPolicy.Label)
		wrapper.admissionResult = make(chan error, 1)
		wrapper.beforeAdmission = func() error {
			_, err := writer.DeleteInlinePolicy(storedStack.ID, storedPolicy.Label, "remove-after-certificate")
			return err
		}
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.AutoReconciler, Node: m.Node.Name()}, messages.RefreshEffectivePolicies{}))
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.AutoReconciler, Node: m.Node.Name()}, ReconcileStack{StackLabel: "scope"}))
		require.ErrorIs(t, <-wrapper.admissionResult, datastore.ErrStaleAdmission)
		require.Zero(t, updates.Load())
		commands, err := writer.LoadFormaCommands()
		require.NoError(t, err)
		for _, command := range commands {
			require.NotEqual(t, forma_command.SourceAutoReconciler, command.Source)
		}
	})
}

// Generator rotation checks the generator-owner stack early, while its
// destination closure can include a consumer in another stack. Final command
// membership exclusion must catch that busy consumer and let a later sweep
// rotate after the user command finishes.
func TestScheduledAdmissionGeneratorRotationWaitsForBusyCrossStackConsumer(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/rotation-consumer.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		wrapper := &expiredStackReadBarrier{scopedReadBarrier: withScopedBarrier(ds, nil), rotationObserved: make(chan struct{}, 4)}
		wrapper.observeRotations.Store(true)
		updateEntered := make(chan struct{}, 1)
		releaseUpdate := make(chan struct{})
		var releaseOnce sync.Once
		var rotationWrites atomic.Int64
		var providerMu sync.Mutex
		providerState := make(map[string]json.RawMessage)
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				providerMu.Lock()
				providerState[request.Label] = append(json.RawMessage(nil), request.Properties...)
				providerMu.Unlock()
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: request.Properties}}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				if request.ResourceType == "FakeAWS::S3::Bucket" {
					updateEntered <- struct{}{}
					<-releaseUpdate
				} else {
					rotationWrites.Add(1)
				}
				providerMu.Lock()
				providerState[request.NativeID] = append(json.RawMessage(nil), request.DesiredProperties...)
				providerMu.Unlock()
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationUpdate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.NativeID, ResourceProperties: request.DesiredProperties}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				providerMu.Lock()
				properties := append(json.RawMessage(nil), providerState[request.NativeID]...)
				providerMu.Unlock()
				return &resource.ReadResult{ResourceType: request.ResourceType, Properties: string(properties)}, nil
			},
		}
		m := startScopedActor(t, wrapper, path, overrides)
		t.Cleanup(func() { releaseOnce.Do(func() { close(releaseUpdate) }) })
		generatorJSON, err := json.Marshal(&pkgmodel.PasswordGenerator{
			Label: "db-password", Stack: "owner", Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
			Rotation: &pkgmodel.RotationSpec{EverySeconds: 1},
		})
		require.NoError(t, err)
		secret := pkgmodel.Resource{
			Label: "secret", Type: "FakeAWS::SecretsManager::Secret", Stack: "owner", Target: "target",
			Schema:     scheduledSecretSchema(),
			Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$gen":true,"$label":"db-password","$stack":"owner","$output":"value","$visibility":"Opaque"}}`),
		}
		consumer := pkgmodel.Resource{
			Label: "consumer", Type: "FakeAWS::S3::Bucket", Stack: "consumer", Target: "target",
			Schema: pkgmodel.Schema{
				Identifier: "Id",
				Fields:     []string{"Name", "AccessControl", "DbPassword"},
				Hints:      map[string]pkgmodel.FieldHint{"DbPassword": {Opaque: true}},
			},
			Properties: json.RawMessage(`{"Name":"consumer","AccessControl":"Private","DbPassword":{"$res":true,"$label":"secret","$type":"FakeAWS::SecretsManager::Secret","$stack":"owner","$property":"SecretString","$visibility":"Opaque"}}`),
		}
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "owner"}, {Label: "consumer"}}, Targets: []pkgmodel.Target{{Label: "target", Namespace: "FakeAWS", Config: []byte(`{}`)}},
			Generators: []json.RawMessage{generatorJSON}, Resources: []pkgmodel.Resource{secret, consumer},
		}
		initial, err := m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		infos, err := writer.GetGeneratorsWithRotation()
		require.NoError(t, err)
		require.Len(t, infos, 1)
		sqliteWriter := writer.(dssqlite.DatastoreSQLite)
		_, err = sqliteWriter.Conn().Exec(`UPDATE forma_commands SET timestamp=? WHERE command_id IN (SELECT command_id FROM generators WHERE id=? AND generation_id!='')`, time.Now().UTC().Add(-time.Hour), infos[0].GeneratorID)
		require.NoError(t, err)

		consumer.Properties = json.RawMessage(`{"Name":"consumer","AccessControl":"PublicRead","DbPassword":{"$res":true,"$label":"secret","$type":"FakeAWS::SecretsManager::Secret","$stack":"owner","$property":"SecretString","$visibility":"Opaque"}}`)
		busy, err := m.ApplyForma(&pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "consumer"}}, Targets: forma.Targets, Resources: []pkgmodel.Resource{consumer}}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Force: true}, "client", "subject", "")
		require.NoError(t, err)
		select {
		case <-updateEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("consumer update did not reach the held provider write")
		}

		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.GeneratorRotator, Node: m.Node.Name()}, CheckGeneratorRotations{}))
		<-wrapper.rotationObserved
		wrapper.observeRotations.Store(false)
		rotator, err := m.Node.ProcessPID(gen.Atom(actornames.GeneratorRotator))
		require.NoError(t, err)
		_, err = m.Node.Inspect(rotator)
		require.NoError(t, err, "inspection must run after the current rotation sweep completes")
		commands, err := writer.LoadFormaCommands()
		require.NoError(t, err)
		for _, command := range commands {
			require.NotEqual(t, forma_command.SourceGeneratorRotator, command.Source)
		}
		require.Zero(t, rotationWrites.Load())

		releaseOnce.Do(func() { close(releaseUpdate) })
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, busy.CommandID).State)
		require.Eventually(t, func() bool {
			_ = m.Node.Send(gen.ProcessID{Name: actornames.GeneratorRotator, Node: m.Node.Name()}, CheckGeneratorRotations{})
			commands, loadErr := writer.LoadFormaCommands()
			if loadErr != nil {
				return false
			}
			for _, command := range commands {
				if command.Source == forma_command.SourceGeneratorRotator && command.State == forma_command.CommandStateSuccess {
					return true
				}
			}
			return false
		}, 5*time.Second, 50*time.Millisecond)
		require.EqualValues(t, 1, rotationWrites.Load(), "the admitted retry must perform one provider rotation write")
	})
}

func TestScheduledAdmissionGeneratorRotationRejectsCadenceChangeAfterCertification(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/rotation-cadence-final.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		wrapper := &expiredStackReadBarrier{scopedReadBarrier: withScopedBarrier(ds, nil)}
		var updates atomic.Int64
		m := startScopedActor(t, wrapper, path, &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: request.Properties}}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				updates.Add(1)
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationUpdate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.NativeID, ResourceProperties: request.DesiredProperties}}, nil
			},
		})
		generator := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "owner", Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true, Rotation: &pkgmodel.RotationSpec{EverySeconds: 1}}
		generatorJSON, err := json.Marshal(generator)
		require.NoError(t, err)
		secret := pkgmodel.Resource{Label: "secret", Type: "FakeAWS::SecretsManager::Secret", Stack: "owner", Target: "target", Schema: scheduledSecretSchema(), Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$gen":true,"$label":"db-password","$stack":"owner","$output":"value","$visibility":"Opaque"}}`)}
		forma := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "owner"}}, Targets: []pkgmodel.Target{{Label: "target", Namespace: "FakeAWS", Config: []byte(`{}`)}}, Generators: []json.RawMessage{generatorJSON}, Resources: []pkgmodel.Resource{secret}}
		initial, err := m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		infos, err := writer.GetGeneratorsWithRotation()
		require.NoError(t, err)
		require.Len(t, infos, 1)
		sqliteWriter := writer.(dssqlite.DatastoreSQLite)
		_, err = sqliteWriter.Conn().Exec(`UPDATE forma_commands SET timestamp=? WHERE command_id IN (SELECT command_id FROM generators WHERE id=? AND generation_id!='')`, time.Now().UTC().Add(-time.Hour), infos[0].GeneratorID)
		require.NoError(t, err)
		stack, err := writer.GetStackByLabel("owner")
		require.NoError(t, err)
		wrapper.admissionResult = make(chan error, 1)
		wrapper.beforeAdmission = func() error {
			widened := *generator
			widened.StackID = stack.ID
			widened.Rotation = &pkgmodel.RotationSpec{EverySeconds: 86400}
			_, err := writer.UpdateGenerator(&widened, "widen-after-certificate")
			return err
		}

		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.GeneratorRotator, Node: m.Node.Name()}, CheckGeneratorRotations{}))
		require.ErrorIs(t, <-wrapper.admissionResult, datastore.ErrStaleAdmission)
		require.Zero(t, updates.Load())
		commands, err := writer.LoadFormaCommands()
		require.NoError(t, err)
		for _, command := range commands {
			require.NotEqual(t, forma_command.SourceGeneratorRotator, command.Source)
		}
		current := rotationInfoForTest(t, writer, "owner", "db-password")
		require.Equal(t, 86400, current.IntervalSeconds)
	})
}

func TestScheduledAdmissionGeneratorRotationRefreshesCompletedDrawAfterSweep(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/rotation-anchor-refresh.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
		require.NoError(t, err)
		t.Cleanup(func() { writer.Close() })
		wrapper := &expiredStackReadBarrier{scopedReadBarrier: withScopedBarrier(ds, nil), rotationObserved: make(chan struct{}, 4), rotationRelease: make(chan struct{})}
		wrapper.observeRotations.Store(true)
		var releaseOnce sync.Once
		m := startScopedActor(t, wrapper, path, &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: request.Label, ResourceProperties: request.Properties}}, nil
			},
		})
		t.Cleanup(func() { releaseOnce.Do(func() { close(wrapper.rotationRelease) }) })
		generator := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "owner", Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true, Rotation: &pkgmodel.RotationSpec{EverySeconds: 3600}}
		generatorJSON, err := json.Marshal(generator)
		require.NoError(t, err)
		secret := pkgmodel.Resource{Label: "secret", Type: "FakeAWS::SecretsManager::Secret", Stack: "owner", Target: "target", Schema: scheduledSecretSchema(), Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$gen":true,"$label":"db-password","$stack":"owner","$output":"value","$visibility":"Opaque"}}`)}
		initial, err := m.ApplyForma(&pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "owner"}}, Targets: []pkgmodel.Target{{Label: "target", Namespace: "FakeAWS", Config: []byte(`{}`)}}, Generators: []json.RawMessage{generatorJSON}, Resources: []pkgmodel.Resource{secret}}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, waitForAdmissionBoundaryCommand(t, writer, initial.CommandID).State)
		info := rotationInfoForTest(t, writer, "owner", "db-password")
		sqliteWriter := writer.(dssqlite.DatastoreSQLite)
		_, err = sqliteWriter.Conn().Exec(`UPDATE forma_commands SET timestamp=? WHERE command_id IN (SELECT command_id FROM generators WHERE id=? AND generation_id!='')`, time.Now().UTC().Add(-2*time.Hour), info.GeneratorID)
		require.NoError(t, err)
		wrapper.blockRotationNext.Store(true)
		require.NoError(t, m.Node.Send(gen.ProcessID{Name: actornames.GeneratorRotator, Node: m.Node.Name()}, CheckGeneratorRotations{}))
		<-wrapper.rotationObserved

		drawID := util.NewID()
		now := time.Now().UTC()
		require.NoError(t, writer.StoreFormaCommand(&forma_command.FormaCommand{ID: drawID, Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: forma_command.CommandStateSuccess, StartTs: now, ModifiedTs: now}, drawID))
		require.NoError(t, writer.AdvanceGeneration(info.GeneratorID, util.NewID(), drawID, generatorJSON))
		wrapper.observeRotations.Store(false)
		releaseOnce.Do(func() { close(wrapper.rotationRelease) })
		rotator, err := m.Node.ProcessPID(gen.Atom(actornames.GeneratorRotator))
		require.NoError(t, err)
		_, err = m.Node.Inspect(rotator)
		require.NoError(t, err, "inspection must run after the current rotation sweep completes")
		commands, err := writer.LoadFormaCommands()
		require.NoError(t, err)
		for _, command := range commands {
			require.NotEqual(t, forma_command.SourceGeneratorRotator, command.Source)
		}
		refreshed := rotationInfoForTest(t, writer, "owner", "db-password")
		require.WithinDuration(t, now, refreshed.LastRotationAt, time.Second)
	})
}

func rotationInfoForTest(t *testing.T, ds datastore.Datastore, stack, label string) datastore.GeneratorRotationInfo {
	t.Helper()
	infos, err := ds.GetGeneratorsWithRotation()
	require.NoError(t, err)
	for _, info := range infos {
		if info.StackLabel == stack && info.Label == label {
			return info
		}
	}
	t.Fatalf("missing generator rotation info for %s/%s", stack, label)
	return datastore.GeneratorRotationInfo{}
}
