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
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
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
		pkgmodel.CommandSync, nil, nil, nil, nil, nil, "", "", "",
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
		pkgmodel.CommandApply, nil, nil, nil, nil, nil, "client-abc",
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

// A command carrying only generator operations must project them, so a
// generator-only change is never an empty plan.
func TestTranslateToAPICommand_ProjectsGeneratorUpdates(t *testing.T) {
	fa := &forma_command.FormaCommand{
		ID:      "cmd-test-gen-1",
		Command: pkgmodel.CommandApply,
		Config:  config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		GeneratorUpdates: []generator_update.GeneratorUpdate{{
			Generator: &pkgmodel.PasswordGenerator{
				Label: "db-password", Stack: "prod",
				Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
			},
			Operation:  generator_update.GeneratorOperationCreate,
			State:      generator_update.GeneratorUpdateStateNotStarted,
			StackLabel: "prod",
		}},
	}

	api := translateToAPICommand(fa)

	if assert.Len(t, api.GeneratorUpdates, 1) {
		gu := api.GeneratorUpdates[0]
		assert.Equal(t, "db-password", gu.GeneratorLabel)
		assert.Equal(t, "password", gu.GeneratorType)
		assert.Equal(t, "prod", gu.StackName)
		assert.Equal(t, "create", gu.Operation)
		assert.Equal(t, "NotStarted", gu.State)
		assert.NotEmpty(t, gu.GeneratorConfig)
	}
}

// The projection is metadata only: a generator's spec may be shown, a
// generated value never exists here to show.
func TestTranslateToAPICommand_GeneratorProjectionCarriesNoMaterial(t *testing.T) {
	fa := &forma_command.FormaCommand{
		ID:      "cmd-test-gen-2",
		Command: pkgmodel.CommandApply,
		Config:  config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		GeneratorUpdates: []generator_update.GeneratorUpdate{{
			Generator: &pkgmodel.PasswordGenerator{
				Label: "db-password", Stack: "prod", ID: "should-never-appear-in-projection",
				Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
			},
			Operation:  generator_update.GeneratorOperationCreate,
			State:      generator_update.GeneratorUpdateStateNotStarted,
			StackLabel: "prod",
		}},
	}

	api := translateToAPICommand(fa)
	if assert.Len(t, api.GeneratorUpdates, 1) {
		raw := string(api.GeneratorUpdates[0].GeneratorConfig)

		// The generator's own KSUID identity is controller state assigned
		// during translation, never declared configuration.
		assert.NotContains(t, raw, "should-never-appear-in-projection")
		// The drawing spec (pkgmodel.GeneratorIdentity) is agent-internal
		// controller state, never part of what a Generator marshals.
		assert.NotContains(t, raw, "GenerationID")
		assert.NotContains(t, raw, "GenerationSpec")
		// A generated value does not exist at projection time at all.
		assert.NotContains(t, raw, "\"Value\"")
	}
}
