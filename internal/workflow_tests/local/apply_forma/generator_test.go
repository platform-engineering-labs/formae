// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func rawPasswordGenerator(t *testing.T, gen *pkgmodel.PasswordGenerator) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(gen)
	require.NoError(t, err)
	return data
}

// TestMetastructure_ApplyFormaPersistsGeneratorThenReapplyIsANoop is the
// generator lifecycle's end-to-end case: applying a forma that declares a
// generator actually persists it, and re-applying the identical forma
// persists nothing new — connecting Forma.Generators to the datastore is
// exactly what this slice adds; nothing read it before.
func TestMetastructure_ApplyFormaPersistsGeneratorThenReapplyIsANoop(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "generator-e2e-stack"}},
			Generators: []json.RawMessage{
				rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
					Label: "db-password", Stack: "generator-e2e-stack",
					Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
				}),
			},
		}

		_, err = m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)

		var commands []*forma_command.FormaCommand
		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			incompleteCommands, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(commands) == 1 && len(incompleteCommands) == 0
		}, 5*time.Second, 100*time.Millisecond)

		require.Len(t, commands, 1)

		stored, err := m.Datastore.GetGenerator("db-password", "generator-e2e-stack")
		require.NoError(t, err)
		require.NotNil(t, stored, "the declared generator must be persisted")
		pw, ok := stored.(*pkgmodel.PasswordGenerator)
		require.True(t, ok)
		assert.Equal(t, 24, pw.Length)

		// Re-applying the identical forma must be a no-op: no new command
		// changes anything about the generator.
		_, err = m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			return len(commands) == 1
		}, 5*time.Second, 100*time.Millisecond, "an unchanged generator must not produce a second command")
	})
}
