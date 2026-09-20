// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSuccessfulSynchronizerCommandsHaveDurableEvents(t *testing.T) {
	const (
		commandID = "sync-command"
		ksuid     = "resource-ksuid"
		version   = "physical-version"
		uri       = "formae://resource"
	)

	command := historyCommandRow{Command: "sync", Source: "synchronizer", State: "Success"}
	physical := historyResourceRow{
		URI:       uri,
		Version:   version,
		CommandID: "earlier-command",
		Operation: "update",
		Ksuid:     ksuid,
	}
	tombstone := physical
	tombstone.Operation = "delete"
	tombstone.CommandID = "earlier-delete"

	tests := []struct {
		name           string
		beforeVersions map[string]historyResourceRow
		afterVersions  map[string]historyResourceRow
		afterCommands  map[string]historyCommandRow
		update         historyUpdateRow
		wantError      bool
	}{
		{
			name:           "new physical version",
			beforeVersions: map[string]historyResourceRow{},
			afterVersions: map[string]historyResourceRow{
				uri + "\x00" + version: {
					URI: uri, Version: version, CommandID: commandID, Operation: "update", Ksuid: ksuid,
				},
			},
		},
		{
			name:           "successful delete receipt for user destroy tombstone created in window",
			beforeVersions: map[string]historyResourceRow{},
			afterVersions: map[string]historyResourceRow{
				uri + "\x00" + version: {
					URI: uri, Version: version, CommandID: "user-destroy", Operation: "delete", Ksuid: ksuid,
				},
			},
			afterCommands: map[string]historyCommandRow{
				"user-destroy": {Command: "destroy", Source: "user", State: "Success"},
			},
			update: historyUpdateRow{
				CommandID: commandID, Ksuid: ksuid, Operation: "delete", State: "Success", Version: ksuid + "_" + version,
			},
		},
		{
			name:           "successful delete receipt for TTL tombstone created in window",
			beforeVersions: map[string]historyResourceRow{},
			afterVersions: map[string]historyResourceRow{
				uri + "\x00" + version: {
					URI: uri, Version: version, CommandID: "ttl-destroy", Operation: "delete", Ksuid: ksuid,
				},
			},
			afterCommands: map[string]historyCommandRow{
				"ttl-destroy": {Command: "destroy", Source: "stack_expirer", State: "Success"},
			},
			update: historyUpdateRow{
				CommandID: commandID, Ksuid: ksuid, Operation: "delete", State: "Success", Version: ksuid + "_" + version,
			},
		},
		{
			name:           "successful delete receipt for existing tombstone",
			beforeVersions: map[string]historyResourceRow{uri + "\x00" + version: tombstone},
			afterVersions:  map[string]historyResourceRow{uri + "\x00" + version: tombstone},
			update: historyUpdateRow{
				CommandID: commandID, Ksuid: ksuid, Operation: "delete", State: "Success", Version: ksuid + "_" + version,
			},
		},
		{
			name:           "read receipt for existing live version",
			beforeVersions: map[string]historyResourceRow{uri + "\x00" + version: physical},
			afterVersions:  map[string]historyResourceRow{uri + "\x00" + version: physical},
			update: historyUpdateRow{
				CommandID: commandID, Ksuid: ksuid, Operation: "read", State: "Success", Version: ksuid + "_" + version,
			},
			wantError: true,
		},
		{
			name:           "delete receipt for existing live version",
			beforeVersions: map[string]historyResourceRow{uri + "\x00" + version: physical},
			afterVersions:  map[string]historyResourceRow{uri + "\x00" + version: physical},
			update: historyUpdateRow{
				CommandID: commandID, Ksuid: ksuid, Operation: "delete", State: "Success", Version: ksuid + "_" + version,
			},
			wantError: true,
		},
		{
			name:           "unknown delete receipt",
			beforeVersions: map[string]historyResourceRow{},
			afterVersions:  map[string]historyResourceRow{},
			update: historyUpdateRow{
				CommandID: commandID, Ksuid: ksuid, Operation: "delete", State: "Success", Version: ksuid + "_unknown",
			},
			wantError: true,
		},
		{
			name:           "delete receipt with wrong identity",
			beforeVersions: map[string]historyResourceRow{uri + "\x00" + version: tombstone},
			afterVersions:  map[string]historyResourceRow{uri + "\x00" + version: tombstone},
			update: historyUpdateRow{
				CommandID: commandID, Ksuid: "other-ksuid", Operation: "delete", State: "Success", Version: "other-ksuid_" + version,
			},
			wantError: true,
		},
		{
			name:           "failed delete receipt",
			beforeVersions: map[string]historyResourceRow{uri + "\x00" + version: tombstone},
			afterVersions:  map[string]historyResourceRow{uri + "\x00" + version: tombstone},
			update: historyUpdateRow{
				CommandID: commandID, Ksuid: ksuid, Operation: "delete", State: "Failed", Version: ksuid + "_" + version,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := historySnapshot{
				Commands: map[string]historyCommandRow{},
				Updates:  map[string]historyUpdateRow{},
				Versions: tt.beforeVersions,
			}
			after := historySnapshot{
				Commands: map[string]historyCommandRow{commandID: command},
				Updates:  map[string]historyUpdateRow{},
				Versions: tt.afterVersions,
			}
			for id, owner := range tt.afterCommands {
				after.Commands[id] = owner
			}
			if tt.update.CommandID != "" {
				after.Updates[commandID+"\x00"+tt.update.Ksuid+"\x00"+tt.update.Operation] = tt.update
			}

			err := newSuccessfulSynchronizerCommandsHaveDurableEvents(before, after)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCommandHasSuccessfulDeleteReceipt_InWindowTombstones(t *testing.T) {
	const (
		commandID = "sync-command"
		ksuid     = "resource-ksuid"
		version   = "physical-version"
		uri       = "formae://resource"
	)

	tests := []struct {
		name              string
		ownerID           string
		ownerCommand      string
		ownerSource       string
		includeOwner      bool
		includeVersion    bool
		beforeHasKey      bool
		updateOperation   string
		updateState       string
		updateKsuid       string
		updateVersion     string
		resourceOperation string
		resourceKsuid     string
		want              bool
	}{
		{
			name: "user destroy owner", ownerID: "user-destroy", ownerCommand: "destroy", ownerSource: "user",
			includeOwner: true, includeVersion: true, updateOperation: "delete", updateState: "Success",
			updateKsuid: ksuid, updateVersion: ksuid + "_" + version, resourceOperation: "delete", resourceKsuid: ksuid, want: true,
		},
		{
			name: "TTL destroy owner", ownerID: "ttl-destroy", ownerCommand: "destroy", ownerSource: "stack_expirer",
			includeOwner: true, includeVersion: true, updateOperation: "delete", updateState: "Success",
			updateKsuid: ksuid, updateVersion: ksuid + "_" + version, resourceOperation: "delete", resourceKsuid: ksuid, want: true,
		},
		{
			name: "unknown receipt", ownerID: "user-destroy", ownerCommand: "destroy", ownerSource: "user",
			includeOwner: true, updateOperation: "delete", updateState: "Success",
			updateKsuid: ksuid, updateVersion: ksuid + "_" + version, resourceOperation: "delete", resourceKsuid: ksuid,
		},
		{
			name: "missing owner", ownerID: "missing-owner", includeVersion: true,
			updateOperation: "delete", updateState: "Success", updateKsuid: ksuid, updateVersion: ksuid + "_" + version,
			resourceOperation: "delete", resourceKsuid: ksuid,
		},
		{
			name: "sync owner", ownerID: "other-sync", ownerCommand: "sync", ownerSource: "synchronizer",
			includeOwner: true, includeVersion: true, updateOperation: "delete", updateState: "Success",
			updateKsuid: ksuid, updateVersion: ksuid + "_" + version, resourceOperation: "delete", resourceKsuid: ksuid,
		},
		{
			name: "same command owner", ownerID: commandID, ownerCommand: "sync", ownerSource: "synchronizer",
			includeOwner: true, includeVersion: true, updateOperation: "delete", updateState: "Success",
			updateKsuid: ksuid, updateVersion: ksuid + "_" + version, resourceOperation: "delete", resourceKsuid: ksuid,
		},
		{
			name: "read receipt", ownerID: "user-destroy", ownerCommand: "destroy", ownerSource: "user",
			includeOwner: true, includeVersion: true, updateOperation: "read", updateState: "Success",
			updateKsuid: ksuid, updateVersion: ksuid + "_" + version, resourceOperation: "delete", resourceKsuid: ksuid,
		},
		{
			name: "live physical row", ownerID: "user-write", ownerCommand: "apply", ownerSource: "user",
			includeOwner: true, includeVersion: true, updateOperation: "delete", updateState: "Success",
			updateKsuid: ksuid, updateVersion: ksuid + "_" + version, resourceOperation: "update", resourceKsuid: ksuid,
		},
		{
			name: "wrong identity", ownerID: "user-destroy", ownerCommand: "destroy", ownerSource: "user",
			includeOwner: true, includeVersion: true, updateOperation: "delete", updateState: "Success",
			updateKsuid: "other-ksuid", updateVersion: "other-ksuid_" + version, resourceOperation: "delete", resourceKsuid: ksuid,
		},
		{
			name: "version mismatch", ownerID: "user-destroy", ownerCommand: "destroy", ownerSource: "user",
			includeOwner: true, includeVersion: true, updateOperation: "delete", updateState: "Success",
			updateKsuid: ksuid, updateVersion: ksuid + "_other-version", resourceOperation: "delete", resourceKsuid: ksuid,
		},
		{
			name: "failed deletion", ownerID: "user-destroy", ownerCommand: "destroy", ownerSource: "user",
			includeOwner: true, includeVersion: true, updateOperation: "delete", updateState: "Failed",
			updateKsuid: ksuid, updateVersion: ksuid + "_" + version, resourceOperation: "delete", resourceKsuid: ksuid,
		},
		{
			name: "physical key already existed", ownerID: "user-destroy", ownerCommand: "destroy", ownerSource: "user",
			includeOwner: true, includeVersion: true, beforeHasKey: true, updateOperation: "delete", updateState: "Success",
			updateKsuid: ksuid, updateVersion: ksuid + "_" + version, resourceOperation: "delete", resourceKsuid: ksuid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := uri + "\x00" + version
			before := historySnapshot{Versions: map[string]historyResourceRow{}}
			if tt.beforeHasKey {
				before.Versions[key] = historyResourceRow{
					URI: uri, Version: version, CommandID: "earlier-write", Operation: "update", Ksuid: ksuid,
				}
			}
			update := historyUpdateRow{
				CommandID: commandID, Ksuid: tt.updateKsuid, Operation: tt.updateOperation,
				State: tt.updateState, Version: tt.updateVersion,
			}
			after := historySnapshot{
				Commands: map[string]historyCommandRow{
					commandID: {Command: "sync", Source: "synchronizer", State: "Success"},
				},
				Updates: map[string]historyUpdateRow{
					commandID + "\x00" + tt.updateKsuid + "\x00" + tt.updateOperation: update,
				},
				Versions: map[string]historyResourceRow{},
			}
			if tt.includeOwner {
				after.Commands[tt.ownerID] = historyCommandRow{
					Command: tt.ownerCommand, Source: tt.ownerSource, State: "Success",
				}
			}
			if tt.includeVersion {
				after.Versions[key] = historyResourceRow{
					URI: uri, Version: version, CommandID: tt.ownerID,
					Operation: tt.resourceOperation, Ksuid: tt.resourceKsuid,
				}
			}

			require.Equal(t, tt.want, commandHasSuccessfulDeleteReceipt(commandID, before, after))
		})
	}
}
