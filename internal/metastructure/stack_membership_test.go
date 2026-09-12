// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestFormaCommandMembershipUsesAuthoritativeEmptyStackIdentity(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	stack := &pkgmodel.Stack{Label: "empty", ID: "authoritative"}
	_, err := ds.CreateStack(stack, "initial")
	require.NoError(t, err)
	forma := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "empty", ID: "forged"}}}
	command, err := FormaCommandFromForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, pkgmodel.CommandApply, ds, "", "", "", resource_update.FormaCommandSourceUser, time.Minute)
	require.NoError(t, err)
	require.Equal(t, []forma_command.CommandStack{{ID: stack.ID, Label: "empty"}}, command.Stacks)
	require.Empty(t, command.StackUpdates)
}

func TestFormaCommandMembershipUsesPlannedNewStackIdentity(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	forma := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "new", ID: "forged"}}}
	command, err := FormaCommandFromForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, pkgmodel.CommandApply, ds, "", "", "", resource_update.FormaCommandSourceUser, time.Minute)
	require.NoError(t, err)
	require.Len(t, command.StackUpdates, 1)
	require.NotEqual(t, "forged", command.StackUpdates[0].Stack.ID)
	require.Equal(t, []forma_command.CommandStack{{ID: command.StackUpdates[0].Stack.ID, Label: "new"}}, command.Stacks)
}

func TestFormaCommandMembershipPreservesProducerSource(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	for _, tc := range []struct {
		source resource_update.FormaCommandSource
		want   forma_command.Source
	}{
		{resource_update.FormaCommandSourceUser, forma_command.SourceUser},
		{resource_update.FormaCommandSourceSynchronize, forma_command.SourceSynchronizer},
		{resource_update.FormaCommandSourceDiscovery, forma_command.SourceDiscovery},
		{resource_update.FormaCommandSourcePolicyAutoReconcile, forma_command.SourceAutoReconciler},
		{resource_update.FormaCommandSourceGeneratorRotation, forma_command.SourceGeneratorRotator},
	} {
		command, err := FormaCommandFromForma(&pkgmodel.Forma{}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}, pkgmodel.CommandApply, ds, "", "", "", tc.source, time.Minute)
		require.NoError(t, err)
		require.Equal(t, tc.want, command.Source)
	}
}
