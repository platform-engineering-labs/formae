// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestTranslateToAPICommand_IncludesMode(t *testing.T) {
	fa := &forma_command.FormaCommand{
		ID:      "cmd-test-1",
		Command: pkgmodel.CommandApply,
		State:   forma_command.CommandStateInProgress,
		Config:  config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
	}

	apiCmd := translateToAPICommand(fa)

	assert.Equal(t, "patch", apiCmd.Mode)
}

func TestTranslateToAPICommand_Source(t *testing.T) {
	fa := forma_command.NewFormaCommand(
		&pkgmodel.Forma{}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		pkgmodel.CommandSync, nil, nil, nil, nil, "",
		forma_command.SourceSynchronizer,
	)
	api := translateToAPICommand(fa)
	assert.Equal(t, "synchronizer", api.Source)
}
