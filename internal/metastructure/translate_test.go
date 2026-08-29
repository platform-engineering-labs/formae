// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

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
		pkgmodel.CommandSync, nil, nil, nil, nil, "", "", "",
		forma_command.SourceSynchronizer,
	)
	api := translateToAPICommand(fa)
	assert.Equal(t, "synchronizer", api.Source)
}

// translateToAPICommand copies the authenticated Subject and its display-name
// hint from the FormaCommand into the API-facing Command.
func TestTranslateToAPICommand_Subject(t *testing.T) {
	fa := forma_command.NewFormaCommand(
		&pkgmodel.Forma{}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		pkgmodel.CommandApply, nil, nil, nil, nil, "client-abc",
		"11111111-1111-4111-8111-111111111111", "dpanders",
		forma_command.SourceUser,
	)
	api := translateToAPICommand(fa)
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", api.Subject)
	assert.Equal(t, "dpanders", api.SubjectName)
}

// translateToAPICommand carries the command's suppressed-drift notes into the
// API projection so status consumers read them from a typed field.
func TestTranslateToAPICommand_SuppressedDriftNotes(t *testing.T) {
	fa := &forma_command.FormaCommand{
		ID:      "cmd-test-2",
		Command: pkgmodel.CommandApply,
		Config:  config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		SuppressedDriftNotes: []forma_command.SuppressedDriftNote{{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
			Path: "EnableKeyRotation",
			From: []byte(`false`), To: []byte(`true`),
			Disposition: forma_command.SuppressedDriftAbsorbed,
		}},
	}

	api := translateToAPICommand(fa)

	if assert.Len(t, api.SuppressedDrift, 1) {
		note := api.SuppressedDrift[0]
		assert.Equal(t, "prod", note.Stack)
		assert.Equal(t, "EnableKeyRotation", note.Path)
		assert.JSONEq(t, `false`, string(note.From))
		assert.JSONEq(t, `true`, string(note.To))
		assert.Equal(t, "absorbed", note.Disposition)
	}
}
