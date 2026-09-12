// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func RunExternalChangeHistory(t *testing.T, ds datastore.Datastore) {
	for _, tc := range []struct {
		name                           string
		patch, failed, accept, unknown bool
		want                           bool
	}{
		{name: "sync", want: true},
		{name: "successful-patch", patch: true},
		{name: "failed-patch", patch: true, failed: true},
		{name: "accepted-successful-patch", patch: true, accept: true, want: true},
		{name: "accepted-failed-patch", patch: true, failed: true, accept: true, want: true},
		{name: "unknown-history", unknown: true},
	} {
		t.Run("ExternalChangeHistory/"+tc.name, func(t *testing.T) {
			id := util.NewID()
			stack := &pkgmodel.Stack{Label: "external-" + id}
			_, err := ds.CreateStack(stack, "seed")
			require.NoError(t, err)
			ru := resourceUpdate(stack.Label, id, "resource-"+id, `{"foo":"baseline"}`, resource_update.OperationUpdate, resource_update.FormaCommandSourceUser)
			r := ru.DesiredState
			r.Managed = true
			now := time.Now().UTC().Add(-time.Minute)
			command := func(offset time.Duration, mode pkgmodel.FormaApplyMode) *forma_command.FormaCommand {
				return &forma_command.FormaCommand{ID: util.NewID(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, Config: config.FormaCommandConfig{Mode: mode}, State: forma_command.CommandStateSuccess, StartTs: now.Add(offset), ModifiedTs: now.Add(offset), Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}}}
			}
			base := command(0, pkgmodel.FormaApplyModeReconcile)
			version, err := ds.StoreResource(&r, base.ID)
			require.NoError(t, err)
			ru.DesiredState = r
			ru.Version = version
			base.ResourceUpdates = []resource_update.ResourceUpdate{ru}
			require.NoError(t, ds.StoreFormaCommand(base, base.ID))
			baselineID := base.ID
			if tc.patch {
				patch := command(time.Second, pkgmodel.FormaApplyModePatch)
				change := ru
				change.DesiredState.Properties = []byte(`{"foo":"patch"}`)
				change.Version = ""
				if tc.failed {
					patch.State = forma_command.CommandStateFailed
					change.State = resource_update.ResourceUpdateStateFailed
				} else {
					r = change.DesiredState
					change.Version, err = ds.StoreResource(&r, patch.ID)
					require.NoError(t, err)
				}
				patch.ResourceUpdates = []resource_update.ResourceUpdate{change}
				require.NoError(t, ds.StoreFormaCommand(patch, patch.ID))
			}
			if tc.accept {
				observed, err := ds.(datastore.ResourceObservationReader).GetResourceObservation(id)
				require.NoError(t, err)
				require.NotNil(t, observed)
				accepted := command(2*time.Second, pkgmodel.FormaApplyModeReconcile)
				kept := ru
				kept.DesiredState = r
				kept.Version = observed.Version
				kept.Operation = resource_update.OperationAccept
				accepted.ResourceUpdates = []resource_update.ResourceUpdate{kept}
				require.NoError(t, ds.StoreFormaCommand(accepted, accepted.ID))
				baselineID = accepted.ID
			}
			if tc.unknown {
				r.Properties = []byte(`{"foo":"unknown"}`)
				_, err = ds.StoreResource(&r, "missing-"+id)
				require.NoError(t, err)
			}
			sync := command(3*time.Second, pkgmodel.FormaApplyModePatch)
			sync.Command = pkgmodel.CommandSync
			sync.Source = forma_command.SourceSynchronizer
			require.NoError(t, ds.StoreFormaCommand(sync, sync.ID))
			r.Properties = []byte(`{"foo":"external"}`)
			_, err = ds.StoreResource(&r, sync.ID)
			require.NoError(t, err)
			observed, err := ds.(datastore.ResourceObservationReader).GetResourceObservation(id)
			require.NoError(t, err)
			require.NotNil(t, observed)
			got, err := ds.(datastore.ExternalChangeReader).HasOnlyExternalChanges(id, baselineID, observed.Version)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
