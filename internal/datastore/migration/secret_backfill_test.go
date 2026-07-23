// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package migration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func testSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "test-type",
		Hints: map[string]pkgmodel.FieldHint{
			"SecretString": {Opaque: true},
		},
	}
}

// seedCommand stores a FormaCommand with a single ResourceUpdate carrying a
// plaintext schema-opaque secret in DesiredState, PriorState (the read-back
// snapshot) and PreviousProperties (also a read-back snapshot). Writes go
// straight through the datastore, bypassing any of the live hashing hooks,
// to simulate data written before PLA-320's hashing landed.
func seedCommand(t *testing.T, ds datastore.Datastore, state forma_command.CommandState, secretSuffix string) *forma_command.FormaCommand {
	t.Helper()

	cmd := &forma_command.FormaCommand{
		ID:      "cmd-" + secretSuffix,
		Command: pkgmodel.CommandApply,
		State:   state,
		Config: config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		},
		StartTs:    util.TimeNow(),
		ModifiedTs: util.TimeNow(),
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				DesiredState: pkgmodel.Resource{
					Label:      "test-resource-" + secretSuffix,
					Type:       "test-type",
					Stack:      "test-stack",
					Schema:     testSchema(),
					Properties: json.RawMessage(`{"SecretString":"plaintext-desired-` + secretSuffix + `"}`),
					Ksuid:      util.NewID(),
				},
				PriorState: pkgmodel.Resource{
					Properties: json.RawMessage(`{"SecretString":"plaintext-prior-` + secretSuffix + `"}`),
				},
				PreviousProperties: json.RawMessage(`{"SecretString":"plaintext-previous-` + secretSuffix + `"}`),
				ResourceTarget: pkgmodel.Target{
					Label: "test-target",
				},
				Operation:  resource_update.OperationUpdate,
				State:      resource_update.ResourceUpdateStateSuccess,
				StackLabel: "test-stack",
			},
		},
	}

	require.NoError(t, ds.StoreFormaCommand(cmd, cmd.ID))
	return cmd
}

func findCommand(t *testing.T, cmds []*forma_command.FormaCommand, id string) *forma_command.FormaCommand {
	t.Helper()
	for _, c := range cmds {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("command %s not found", id)
	return nil
}

func isHashed(t *testing.T, props json.RawMessage, field string) bool {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(props, &m))
	envelope, ok := m[field].(map[string]any)
	if !ok {
		return false
	}
	hashed, _ := envelope["$hashed"].(bool)
	return hashed
}

func newTestDatastore(t *testing.T) datastore.Datastore {
	t.Helper()
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite: pkgmodel.SqliteConfig{
			FilePath: ":memory:",
		},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	t.Cleanup(ds.Close)
	return ds
}

func TestBackfillHashedSecrets_HashesFinalButSkipsInflightDesiredState(t *testing.T) {
	ds := newTestDatastore(t)

	finalCmd := seedCommand(t, ds, forma_command.CommandStateSuccess, "final")
	inflightCmd := seedCommand(t, ds, forma_command.CommandStateInProgress, "inflight")

	require.NoError(t, BackfillHashedSecrets(ds))

	cmds, err := ds.LoadFormaCommands()
	require.NoError(t, err)

	final := findCommand(t, cmds, finalCmd.ID)
	inflight := findCommand(t, cmds, inflightCmd.ID)

	require.Len(t, final.ResourceUpdates, 1)
	require.Len(t, inflight.ResourceUpdates, 1)

	assert.True(t, isHashed(t, final.ResourceUpdates[0].DesiredState.Properties, "SecretString"),
		"final command's DesiredState secret must be hashed")
	assert.False(t, isHashed(t, inflight.ResourceUpdates[0].DesiredState.Properties, "SecretString"),
		"in-flight DesiredState input must stay plaintext for resume")

	// PriorState.Properties is live plugin input on resume (fed into Read/Update
	// calls via convertResourceForPlugin), so it must only be hashed once the
	// owning command is final — same gate as DesiredState.
	assert.True(t, isHashed(t, final.ResourceUpdates[0].PriorState.Properties, "SecretString"),
		"final command's PriorState secret must be hashed")
	assert.False(t, isHashed(t, inflight.ResourceUpdates[0].PriorState.Properties, "SecretString"),
		"in-flight PriorState input must stay plaintext for resume")

	// PreviousProperties is only ever used for logging and API diff display,
	// never fed back into a plugin call, so it's always safe to hash regardless
	// of the owning command's state.
	assert.True(t, isHashed(t, final.ResourceUpdates[0].PreviousProperties, "SecretString"))
	assert.True(t, isHashed(t, inflight.ResourceUpdates[0].PreviousProperties, "SecretString"))

	// Idempotency: a second sweep must be a no-op.
	require.NoError(t, BackfillHashedSecrets(ds))
	cmds2, err := ds.LoadFormaCommands()
	require.NoError(t, err)
	final2 := findCommand(t, cmds2, finalCmd.ID)
	inflight2 := findCommand(t, cmds2, inflightCmd.ID)

	assert.JSONEq(t,
		string(final.ResourceUpdates[0].DesiredState.Properties),
		string(final2.ResourceUpdates[0].DesiredState.Properties),
		"re-running the sweep must not change already-hashed values")
	assert.False(t, isHashed(t, inflight2.ResourceUpdates[0].DesiredState.Properties, "SecretString"),
		"in-flight DesiredState must still be plaintext after a second sweep")
	assert.False(t, isHashed(t, inflight2.ResourceUpdates[0].PriorState.Properties, "SecretString"),
		"in-flight PriorState must still be plaintext after a second sweep")
}

func TestBackfillHashedSecrets_HashesResourcesTable(t *testing.T) {
	ds := newTestDatastore(t)

	resource := &pkgmodel.Resource{
		Label:      "test-resource",
		Type:       "test-type",
		Stack:      "test-stack",
		NativeID:   "native-1",
		Managed:    true,
		Schema:     testSchema(),
		Properties: json.RawMessage(`{"SecretString":"plaintext-resource-secret"}`),
	}
	_, err := ds.StoreResource(resource, "seed-command")
	require.NoError(t, err)

	require.NoError(t, BackfillHashedSecrets(ds))

	resources, err := ds.LoadAllResources()
	require.NoError(t, err)
	require.Len(t, resources, 1)
	assert.True(t, isHashed(t, resources[0].Properties, "SecretString"))

	// Idempotency: a second sweep must not append another version / change the value.
	require.NoError(t, BackfillHashedSecrets(ds))
	resourcesAfter, err := ds.LoadAllResources()
	require.NoError(t, err)
	require.Len(t, resourcesAfter, 1)
	assert.JSONEq(t, string(resources[0].Properties), string(resourcesAfter[0].Properties))
}
