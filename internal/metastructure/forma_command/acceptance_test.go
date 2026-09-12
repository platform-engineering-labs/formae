// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package forma_command

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestConstructorAcceptanceIsSuccessfulLogicalWork(t *testing.T) {
	for _, op := range []resource_update.OperationType{resource_update.OperationAccept, resource_update.OperationAcceptDelete, resource_update.OperationWithdraw} {
		command := NewFormaCommand(&pkgmodel.Forma{Stacks: []pkgmodel.Stack{{ID: "forged", Label: "empty"}}}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, pkgmodel.CommandApply,
			[]resource_update.ResourceUpdate{{Operation: op}}, nil, nil, nil, nil, "", "", "", SourceUser)
		require.Equal(t, resource_update.ResourceUpdateStateSuccess, command.ResourceUpdates[0].State)
		require.True(t, command.HasChanges())
		require.False(t, command.HasExecutableChanges())
		require.Equal(t, []CommandStack{{Label: "empty"}}, command.Stacks)
	}
}
