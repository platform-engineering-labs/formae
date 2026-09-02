// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RunGetOwnedMembers verifies GetOwnedMembers reads the ownership record off
// the LATEST resource row — a plain current-state read, unlike
// GetPropertiesAtLastWrite's write-only witness: a version stored with no
// record, and a resource never stored at all, both read back as (nil, nil).
func RunGetOwnedMembers(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetOwnedMembers", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		applyCmd := &forma_command.FormaCommand{
			ID:      util.NewID(),
			Command: pkgmodel.CommandApply,
			Config:  config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			State:   forma_command.CommandStateSuccess,
			ResourceUpdates: []resource_update.ResourceUpdate{{
				Operation: types.OperationCreate,
				State:     resource_update.ResourceUpdateStateSuccess,
				DesiredState: pkgmodel.Resource{
					Ksuid:      util.NewID(),
					Label:      "owned",
					Properties: json.RawMessage(`{"labels":{"mine":"1"}}`),
				},
			}},
		}
		require.NoError(t, ds.StoreFormaCommand(applyCmd, applyCmd.ID))

		ksuid := util.NewID()
		owned := &pkgmodel.Resource{
			Ksuid:      ksuid,
			NativeID:   "native-owned",
			Stack:      "stack",
			Type:       "type",
			Label:      "owned",
			Target:     "target",
			Properties: json.RawMessage(`{"labels":{"mine":"1"}}`),
			Managed:    true,
			OwnedMembers: pkgmodel.OwnedMembers{
				"labels": {Rule: "Mapping", Members: []string{"mine"}},
			},
		}
		_, err := ds.StoreResource(owned, applyCmd.ID)
		require.NoError(t, err)

		record, err := ds.GetOwnedMembers(ksuid)
		require.NoError(t, err)
		require.NotNil(t, record)
		assert.Equal(t, pkgmodel.OwnedMembers{"labels": {Rule: "Mapping", Members: []string{"mine"}}}, record)

		// A newer version dropping the record entirely reads back nil: the
		// latest row is the truth, not the first one written.
		cleared := *owned
		cleared.OwnedMembers = nil
		_, err = ds.StoreResource(&cleared, applyCmd.ID)
		require.NoError(t, err)

		record, err = ds.GetOwnedMembers(ksuid)
		require.NoError(t, err)
		assert.Nil(t, record, "the latest version carries no record")

		// A resource with no stored version at all has no record.
		record, err = ds.GetOwnedMembers(util.NewID())
		require.NoError(t, err)
		assert.Nil(t, record)
	})
}
