// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type historyCommandRow struct {
	Command string
	State   string
	Source  string
}

type historyUpdateRow struct {
	CommandID string
	Ksuid     string
	Operation string
	State     string
	Version   string
}

type historyResourceRow struct {
	URI       string
	Version   string
	CommandID string
	Operation string
	NativeID  string
	Target    string
	Managed   bool
	Ksuid     string
}

type historySnapshot struct {
	Commands         map[string]historyCommandRow
	Updates          map[string]historyUpdateRow
	Versions         map[string]historyResourceRow
	LatestByNativeID map[string]historyResourceRow
}

// captureHistorySnapshot reads commands, updates, and physical resource
// versions through one SQLite read transaction. Generated chaos operations can
// overlap unrelated work, so separate autocommit queries would permit a
// command and its justifying version to land on opposite sides of the sample.
func (h *TestHarness) captureHistorySnapshot(t *testing.T) historySnapshot {
	t.Helper()
	db, err := h.openAgentDB()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck

	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback() //nolint:errcheck

	snapshot := historySnapshot{
		Commands:         make(map[string]historyCommandRow),
		Updates:          make(map[string]historyUpdateRow),
		Versions:         make(map[string]historyResourceRow),
		LatestByNativeID: make(map[string]historyResourceRow),
	}

	commandRows, err := tx.Query(`SELECT command_id, command, state, source FROM forma_commands`)
	require.NoError(t, err)
	for commandRows.Next() {
		var id string
		var row historyCommandRow
		require.NoError(t, commandRows.Scan(&id, &row.Command, &row.State, &row.Source))
		snapshot.Commands[id] = row
	}
	require.NoError(t, commandRows.Err())
	require.NoError(t, commandRows.Close())

	updateRows, err := tx.Query(`
		SELECT command_id, ksuid, operation, state, COALESCE(version, '')
		FROM resource_updates`)
	require.NoError(t, err)
	for updateRows.Next() {
		var row historyUpdateRow
		require.NoError(t, updateRows.Scan(&row.CommandID, &row.Ksuid, &row.Operation, &row.State, &row.Version))
		key := row.CommandID + "\x00" + row.Ksuid + "\x00" + row.Operation
		snapshot.Updates[key] = row
	}
	require.NoError(t, updateRows.Err())
	require.NoError(t, updateRows.Close())

	resourceRows, err := tx.Query(`
		SELECT uri, version, COALESCE(command_id, ''), operation,
		       COALESCE(native_id, ''), COALESCE(target, ''), managed, COALESCE(ksuid, '')
		FROM resources`)
	require.NoError(t, err)
	for resourceRows.Next() {
		var row historyResourceRow
		var managed int
		require.NoError(t, resourceRows.Scan(
			&row.URI, &row.Version, &row.CommandID, &row.Operation,
			&row.NativeID, &row.Target, &managed, &row.Ksuid))
		row.Managed = managed != 0
		key := row.URI + "\x00" + row.Version
		snapshot.Versions[key] = row
		if current, ok := snapshot.LatestByNativeID[row.NativeID]; row.NativeID != "" && (!ok || row.Version > current.Version) {
			snapshot.LatestByNativeID[row.NativeID] = row
		}
	}
	require.NoError(t, resourceRows.Err())
	require.NoError(t, resourceRows.Close())
	require.NoError(t, tx.Commit())
	return snapshot
}

// newSuccessfulSynchronizerCommandsHaveDurableEvents verifies that every newly
// retained successful synchronizer batch is justified by a physical history
// event. Usually the command owns a version created in the observation window.
// An idempotent delete may instead return a receipt for a tombstone that already
// existed when the window began or that a non-sync command created during it.
func newSuccessfulSynchronizerCommandsHaveDurableEvents(before, after historySnapshot) error {
	for commandID, command := range after.Commands {
		if _, existed := before.Commands[commandID]; existed || command.Command != "sync" ||
			command.Source != "synchronizer" || command.State != "Success" {
			continue
		}
		hasDurableEvent := false
		for key, version := range after.Versions {
			if _, existed := before.Versions[key]; !existed && version.CommandID == commandID {
				hasDurableEvent = true
				break
			}
		}
		if !hasDurableEvent {
			hasDurableEvent = commandHasSuccessfulDeleteReceipt(commandID, before, after)
		}
		if !hasDurableEvent {
			return fmt.Errorf("successful synchronizer command %s has no durable physical resource event", commandID)
		}
	}
	return nil
}

func commandHasSuccessfulDeleteReceipt(commandID string, before, after historySnapshot) bool {
	for _, update := range after.Updates {
		if update.CommandID != commandID || update.Operation != "delete" || update.State != "Success" {
			continue
		}
		for _, version := range before.Versions {
			if deleteReceiptMatchesTombstone(update, version) {
				return true
			}
		}
		for key, version := range after.Versions {
			if _, existed := before.Versions[key]; existed || !deleteReceiptMatchesTombstone(update, version) ||
				version.CommandID == commandID {
				continue
			}
			owner, knownOwner := after.Commands[version.CommandID]
			if knownOwner && owner.Command != "sync" {
				return true
			}
		}
	}
	return false
}

func deleteReceiptMatchesTombstone(update historyUpdateRow, version historyResourceRow) bool {
	return version.Operation == "delete" && version.Ksuid == update.Ksuid &&
		update.Version == version.Ksuid+"_"+version.Version
}

func (h *TestHarness) observeManagedCloudResource(
	t *testing.T,
	model *StateModel,
	nativeID, resourceType, revision string,
) (historySnapshot, historySnapshot) {
	t.Helper()
	before := h.captureHistorySnapshot(t)
	beforeResource, ok := before.LatestByNativeID[nativeID]
	require.True(t, ok, "observed resource %s has a physical row", nativeID)
	beforeInventory := h.waitForInventoryNativeIDResource(t, "managed:true", nativeID, 10*time.Second)
	require.NotNil(t, beforeInventory)
	readBaseline := len(h.GetOperationLog(t))

	h.putObservedRevisionWithRetry(t, nativeID, resourceType, revision)
	var afterInventory *pkgmodel.Resource
	const maxSyncAttempts = 3
	for attempt := range maxSyncAttempts {
		if !h.forceSyncAndAwait(t, model, 10*time.Second) {
			t.Logf("observeManagedCloudResource: sync command completed without a retained row (attempt %d)", attempt+1)
		}
		afterInventory = h.waitForObservedInventory(t, nativeID, revision, 2*time.Second)
		if afterInventory != nil {
			break
		}
	}
	require.NotNil(t, afterInventory, "public inventory exposes fresh read-only observation")
	require.JSONEq(t, string(beforeInventory.Properties), string(afterInventory.Properties),
		"observation-only change leaves the writable model untouched")
	require.True(t, h.readObservedSince(t, readBaseline, nativeID),
		"observation freshness needs a subsequent provider Read")

	after := h.captureHistorySnapshot(t)
	require.Equal(t, beforeResource, after.LatestByNativeID[nativeID],
		"observation keeps physical version, identity, target, managed state, and command attribution")
	require.NoError(t, newSuccessfulSynchronizerCommandsHaveDurableEvents(before, after))
	delete(h.cloudStateMirror, nativeID)
	return before, after
}

func (h *TestHarness) putObservedRevisionWithRetry(t *testing.T, nativeID, resourceType, revision string) {
	t.Helper()
	properties := cloudPropertiesWith(t, h, nativeID, "ObservedRevision", revision)
	h.putCloudStateWithRetry(t, nativeID, resourceType, properties)
}

func cloudPropertiesWith(t *testing.T, h *TestHarness, nativeID, key string, value any) string {
	t.Helper()
	entry, ok := h.GetCloudStateSnapshot(t)[nativeID]
	require.True(t, ok, "cloud resource %s exists", nativeID)
	var properties map[string]any
	require.NoError(t, json.Unmarshal([]byte(entry.Properties), &properties))
	properties[key] = value
	encoded, err := json.Marshal(properties)
	require.NoError(t, err)
	return string(encoded)
}

func (h *TestHarness) waitForObservedInventory(t *testing.T, nativeID, revision string, timeout time.Duration) *pkgmodel.Resource {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resource := h.waitForInventoryNativeIDResource(t, "managed:true", nativeID, 100*time.Millisecond)
		if resource != nil {
			var readOnly map[string]any
			if json.Unmarshal(resource.ReadOnlyProperties, &readOnly) == nil && readOnly["ObservedRevision"] == revision {
				return resource
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (h *TestHarness) readObservedSince(t *testing.T, baseline int, nativeID string) bool {
	t.Helper()
	log := h.GetOperationLog(t)
	if baseline > len(log) {
		return false
	}
	for _, entry := range log[baseline:] {
		if entry.Operation == "Read" && entry.NativeID == nativeID {
			return true
		}
	}
	return false
}

func (h *TestHarness) waitForInventoryNativeID(t *testing.T, query, nativeID string, present bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if found := h.waitForInventoryNativeIDResource(t, query, nativeID, 100*time.Millisecond); (found != nil) == present {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func (h *TestHarness) waitForInventoryNativeIDResource(t *testing.T, query, nativeID string, timeout time.Duration) *pkgmodel.Resource {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		forma, err := h.client.ExtractResources(query)
		if err == nil && forma != nil {
			for i := range forma.Resources {
				if forma.Resources[i].NativeID == nativeID {
					resource := forma.Resources[i]
					return &resource
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil
}
