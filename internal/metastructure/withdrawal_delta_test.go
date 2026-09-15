//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
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

func TestWithdrawalDeltaRemovesIntentWithoutClaimingObservedDeletion(t *testing.T) {
	m, _, f, _ := scopedFixture(t)
	stack, err := m.Datastore.GetStackByLabel("a")
	require.NoError(t, err)
	r := f.Resources[0]
	r.Ksuid = "withdrawn"
	command := &forma_command.FormaCommand{ID: util.NewID(), Setup: &forma_command.SetupBoundary{Version: 1}, Resolution: &pkgmodel.DriftReview{ObservationID: "observed", ReviewID: "reviewed"}, StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: forma_command.CommandStateSuccess, Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: "a"}}, ResourceUpdates: []resource_update.ResourceUpdate{{DesiredState: r, StackLabel: "a", Operation: resource_update.OperationWithdraw, Source: resource_update.FormaCommandSourceUser, State: resource_update.ResourceUpdateStateSuccess}}}
	require.NoError(t, m.Datastore.StoreFormaCommand(command, command.ID))
	delta, err := m.ExtractCommandDesiredDelta(command.ID)
	require.NoError(t, err)
	require.Empty(t, delta.Forma.Resources)
	require.Len(t, delta.DeletedResources, 1)
	require.Equal(t, "withdrawn", delta.DeletedResources[0].ResourceID)
	require.Equal(t, "withdraw", delta.DeletedResources[0].Kind)
	require.Empty(t, delta.DeletedResources[0].ObservedVersion)
}
