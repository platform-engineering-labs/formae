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
