// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RunGetPropertiesAtLastWrite verifies the write witness: the latest resource
// version persisted under an apply command that actually wrote (a create, or
// an update with a non-empty patch) is returned even after sync commands
// persist newer versions; a resource formae never wrote (sync versions only)
// has no witness; and a metadata-only apply (an import or rename whose
// update carries no patch and whose result is synthesized from observed
// state) does not forge one.
func RunGetPropertiesAtLastWrite(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetPropertiesAtLastWrite", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		writtenKsuid := util.NewID()
		importedKsuid := util.NewID()

		applyCmd := &forma_command.FormaCommand{
			ID:         util.NewID(),
			Command:    pkgmodel.CommandApply,
			Config:     config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			State:      forma_command.CommandStateSuccess,
			StartTs:    util.TimeNow().Add(-10 * time.Minute),
			ModifiedTs: util.TimeNow().Add(-10 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					Operation: types.OperationCreate,
					State:     resource_update.ResourceUpdateStateSuccess,
					DesiredState: pkgmodel.Resource{
						Ksuid:      writtenKsuid,
						Label:      "written",
						Properties: json.RawMessage(`{"foo":"written"}`),
					},
				},
				{
					// The metadata-only shape: an update with no patch, the
					// synthetic import/rename result.
					Operation: types.OperationUpdate,
					State:     resource_update.ResourceUpdateStateSuccess,
					DesiredState: pkgmodel.Resource{
						Ksuid:      importedKsuid,
						Label:      "imported",
						Properties: json.RawMessage(`{"foo":"observed","runtime":"stuff"}`),
					},
				},
			},
		}
		require.NoError(t, ds.StoreFormaCommand(applyCmd, applyCmd.ID))

		syncCmd := &forma_command.FormaCommand{
			ID:         util.NewID(),
			Command:    pkgmodel.CommandSync,
			State:      forma_command.CommandStateSuccess,
			StartTs:    util.TimeNow().Add(-1 * time.Minute),
			ModifiedTs: util.TimeNow().Add(-1 * time.Minute),
		}
		require.NoError(t, ds.StoreFormaCommand(syncCmd, syncCmd.ID))

		written := &pkgmodel.Resource{
			Ksuid:      writtenKsuid,
			NativeID:   "native-w",
			Stack:      "stack-w",
			Type:       "type-w",
			Label:      "written",
			Target:     "target-w",
			Properties: json.RawMessage(`{"foo":"written","rotation":"off"}`),
			Managed:    true,
		}
		_, err := ds.StoreResource(written, applyCmd.ID)
		require.NoError(t, err)

		// Sync absorbs an out-of-band change: a newer version under the sync
		// command must not move the witness.
		absorbed := *written
		absorbed.Properties = json.RawMessage(`{"foo":"written","rotation":"on"}`)
		_, err = ds.StoreResource(&absorbed, syncCmd.ID)
		require.NoError(t, err)

		witness, err := ds.GetPropertiesAtLastWrite(written.Ksuid)
		require.NoError(t, err)
		assert.JSONEq(t, `{"foo":"written","rotation":"off"}`, string(witness),
			"the witness is the apply-command version, not the sync-absorbed one")

		// A resource with only sync versions has no witness.
		syncedOnly := &pkgmodel.Resource{
			Ksuid:      util.NewID(),
			NativeID:   "native-s",
			Stack:      "stack-w",
			Type:       "type-w",
			Label:      "synced-only",
			Target:     "target-w",
			Properties: json.RawMessage(`{"foo":"observed"}`),
			Managed:    true,
		}
		_, err = ds.StoreResource(syncedOnly, syncCmd.ID)
		require.NoError(t, err)

		witness, err = ds.GetPropertiesAtLastWrite(syncedOnly.Ksuid)
		require.NoError(t, err)
		assert.Nil(t, witness, "formae never wrote the resource, so nothing is witnessed")

		// The metadata-only apply persisted a version, but its update carried
		// no patch: the synthesized echo is observed state, not a write.
		imported := &pkgmodel.Resource{
			Ksuid:      importedKsuid,
			NativeID:   "native-i",
			Stack:      "stack-w",
			Type:       "type-w",
			Label:      "imported",
			Target:     "target-w",
			Properties: json.RawMessage(`{"foo":"observed","runtime":"stuff"}`),
			Managed:    true,
		}
		_, err = ds.StoreResource(imported, applyCmd.ID)
		require.NoError(t, err)

		witness, err = ds.GetPropertiesAtLastWrite(imported.Ksuid)
		require.NoError(t, err)
		assert.Nil(t, witness, "a metadata-only apply must not forge a write witness from observed state")
	})
}
