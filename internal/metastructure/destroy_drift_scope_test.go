// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

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

func TestDestroyedDriftCandidatesRemainGuarded(t *testing.T) {
	for _, mutation := range []string{"resource-identity", "stack-label", "command-history"} {
		t.Run(mutation, func(t *testing.T) {
			m, writer, f, _ := scopedFixture(t)
			resource, err := m.Datastore.LoadResourceById("a")
			require.NoError(t, err)
			storeDesired(t, m.Datastore, *resource, resource_update.OperationCreate, forma_command.CommandStateSuccess)
			stack, err := m.Datastore.GetStackByLabel("a")
			require.NoError(t, err)
			destroy := &forma_command.FormaCommand{ID: util.NewID(), Command: pkgmodel.CommandDestroy, State: forma_command.CommandStateSuccess, Source: forma_command.SourceUser, StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: "a"}}, ResourceUpdates: []resource_update.ResourceUpdate{{Operation: resource_update.OperationDelete, Source: resource_update.FormaCommandSourceUser, StackLabel: "a", DesiredState: *resource}}}
			require.NoError(t, m.Datastore.StoreFormaCommand(destroy, destroy.ID))
			_, err = m.Datastore.DeleteResource(resource, destroy.ID)
			require.NoError(t, err)
			_, err = m.Datastore.DeleteStack("a", destroy.ID)
			require.NoError(t, err)
			plan, err := m.prepareGuardedApply(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
			require.NoError(t, err)
			switch mutation {
			case "resource-identity":
				moved := *resource
				moved.Stack = "b"
				_, err = writer.StoreResource(&moved, "concurrent-move")
			case "stack-label":
				_, err = writer.CreateStack(&pkgmodel.Stack{Label: "a"}, "concurrent-recreate")
			case "command-history":
				destroy.State = forma_command.CommandStateCanceled
				err = writer.StoreFormaCommand(destroy, destroy.ID)
			}
			require.NoError(t, err)
			require.ErrorIs(t, admitScopedPlan(t, m, plan), datastore.ErrStaleAdmission, "filtered historical drift still contributes guarded dependencies")
		})
	}
}
