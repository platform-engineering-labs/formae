//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package forma_command

import (
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestSetupPersistsExactGeneratorDrawIntent(t *testing.T) {
	g := &pkgmodel.PasswordGenerator{ID: "g1", StackID: "stack-id", Label: "drawn", Stack: "stack", Length: 24, Lowercase: true}
	command := &FormaCommand{Setup: &SetupBoundary{Version: 1, Committed: true}, DrawGeneratorUpdates: []generator_update.GeneratorUpdate{generator_update.NewDrawGeneratorUpdate(g, "stack")}}
	raw, err := command.MarshalSetupMetadata(true)
	require.NoError(t, err)
	var loaded FormaCommand
	require.NoError(t, loaded.UnmarshalSetupMetadata(raw))
	require.Len(t, loaded.DrawGeneratorUpdates, 1, "restart must retain the exact planned draw set rather than infer it from widened resource updates")
	require.Equal(t, "g1", loaded.DrawGeneratorUpdates[0].Generator.GetID())
	require.Equal(t, "stack-id", loaded.DrawGeneratorUpdates[0].Generator.GetStackID())
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.NotContains(t, string(raw), "$value")
}
