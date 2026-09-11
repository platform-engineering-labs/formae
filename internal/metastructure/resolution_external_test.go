//go:build unit

package metastructure

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestResolutionExternalOnlyRequiresCompleteHistory(t *testing.T) {
	for _, kind := range []string{"sync-only", "patch-then-sync", "unknown-then-sync"} {
		t.Run(kind, func(t *testing.T) {
			m, _, f, _ := scopedFixture(t)
			r, err := m.Datastore.LoadResourceById("a")
			require.NoError(t, err)
			baselineID := util.NewID()
			version, err := m.Datastore.StoreResource(r, baselineID)
			require.NoError(t, err)
			stack, err := m.Datastore.GetStackByLabel("a")
			require.NoError(t, err)
			baseline := &forma_command.FormaCommand{ID: baselineID, Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, State: forma_command.CommandStateSuccess, StartTs: time.Now(), ModifiedTs: time.Now(), Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: "a"}}, ResourceUpdates: []resource_update.ResourceUpdate{{DesiredState: *r, Version: version, Source: resource_update.FormaCommandSourceUser, StackLabel: "a", Operation: resource_update.OperationUpdate, State: resource_update.ResourceUpdateStateSuccess}}}
			require.NoError(t, m.Datastore.StoreFormaCommand(baseline, baselineID))
			f.Resources[0].Properties = append([]byte(nil), r.Properties...)
			write := func(command pkgmodel.Command, source forma_command.Source, mode pkgmodel.FormaApplyMode, props string) {
				id := util.NewID()
				c := &forma_command.FormaCommand{ID: id, Command: command, Source: source, Config: config.FormaCommandConfig{Mode: mode}, State: forma_command.CommandStateSuccess, StartTs: time.Now(), ModifiedTs: time.Now()}
				require.NoError(t, m.Datastore.StoreFormaCommand(c, id))
				r.Properties = []byte(props)
				_, err := m.Datastore.StoreResource(r, id)
				require.NoError(t, err)
			}
			if kind == "patch-then-sync" {
				write(pkgmodel.CommandApply, forma_command.SourceUser, pkgmodel.FormaApplyModePatch, `{"name":"patched"}`)
			}
			if kind == "unknown-then-sync" {
				_, err := m.Datastore.StoreResource(r, "missing-command")
				require.NoError(t, err)
			}
			write(pkgmodel.CommandSync, forma_command.SourceSynchronizer, "", `{"name":"external"}`)
			got := observeResolution(t, m, f).ModifiedStacks["a"].ModifiedResources[0]
			require.Equal(t, kind == "sync-only", got.ExternalChangesOnly)
		})
	}
}
