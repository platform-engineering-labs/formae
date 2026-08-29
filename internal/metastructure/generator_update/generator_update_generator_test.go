// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package generator_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stubDatastore is a minimal in-test GeneratorDatastore. generatorsByStack is
// keyed by stack label and returned verbatim by LoadGeneratorsByStack.
type stubDatastore struct {
	generatorsByStack map[string][]pkgmodel.Generator
}

func (s *stubDatastore) LoadGeneratorsByStack(stackLabel string) ([]pkgmodel.Generator, error) {
	if s.generatorsByStack == nil {
		return nil, nil
	}
	return s.generatorsByStack[stackLabel], nil
}

func rawGenerator(t *testing.T, g map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(g)
	require.NoError(t, err)
	return data
}

func passwordSpec(label, stack string, length int) map[string]any {
	return map[string]any{
		"Type":                    "password",
		"Label":                   label,
		"Stack":                   stack,
		"Length":                  length,
		"Uppercase":               true,
		"Lowercase":               true,
		"Digits":                  true,
		"RequireEachIncludedType": true,
	}
}

func TestGenerateGeneratorUpdates_NewGeneratorProducesCreate(t *testing.T) {
	gg := NewGeneratorUpdateGenerator(&stubDatastore{})

	forma := &pkgmodel.Forma{
		Stacks:     []pkgmodel.Stack{{Label: "my-stack"}},
		Generators: []json.RawMessage{rawGenerator(t, passwordSpec("db-password", "my-stack", 24))},
	}

	updates, err := gg.GenerateGeneratorUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	assert.Equal(t, GeneratorOperationCreate, updates[0].Operation)
	assert.Equal(t, "db-password", updates[0].Generator.GetLabel())
	assert.Equal(t, "my-stack", updates[0].StackLabel)
}

func TestGenerateGeneratorUpdates_UnchangedGeneratorProducesNoOperation(t *testing.T) {
	stored := &pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "my-stack", StackID: "stack-1",
		Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
	}
	gg := NewGeneratorUpdateGenerator(&stubDatastore{
		generatorsByStack: map[string][]pkgmodel.Generator{"my-stack": {stored}},
	})

	forma := &pkgmodel.Forma{
		Stacks:     []pkgmodel.Stack{{Label: "my-stack"}},
		Generators: []json.RawMessage{rawGenerator(t, passwordSpec("db-password", "my-stack", 24))},
	}

	updates, err := gg.GenerateGeneratorUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, updates, "re-applying an unchanged generator should produce no operation")
}

func TestGenerateGeneratorUpdates_ChangedLengthProducesUpdate(t *testing.T) {
	stored := &pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "my-stack", StackID: "stack-1",
		Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
	}
	gg := NewGeneratorUpdateGenerator(&stubDatastore{
		generatorsByStack: map[string][]pkgmodel.Generator{"my-stack": {stored}},
	})

	forma := &pkgmodel.Forma{
		Stacks:     []pkgmodel.Stack{{Label: "my-stack"}},
		Generators: []json.RawMessage{rawGenerator(t, passwordSpec("db-password", "my-stack", 32))},
	}

	updates, err := gg.GenerateGeneratorUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	assert.Equal(t, GeneratorOperationUpdate, updates[0].Operation)
	assert.Same(t, stored, updates[0].ExistingGenerator)
	pw, ok := updates[0].Generator.(*pkgmodel.PasswordGenerator)
	require.True(t, ok)
	assert.Equal(t, 32, pw.Length)
}

func TestGenerateGeneratorUpdates_ChangedSymbolsProducesUpdate(t *testing.T) {
	stored := &pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "my-stack", StackID: "stack-1",
		Length: 24, Uppercase: true, Lowercase: true, Digits: true, Symbols: false, RequireEachIncludedType: true,
	}
	gg := NewGeneratorUpdateGenerator(&stubDatastore{
		generatorsByStack: map[string][]pkgmodel.Generator{"my-stack": {stored}},
	})

	declared := passwordSpec("db-password", "my-stack", 24)
	declared["Symbols"] = true

	forma := &pkgmodel.Forma{
		Stacks:     []pkgmodel.Stack{{Label: "my-stack"}},
		Generators: []json.RawMessage{rawGenerator(t, declared)},
	}

	updates, err := gg.GenerateGeneratorUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, GeneratorOperationUpdate, updates[0].Operation)
}

func TestGenerateGeneratorUpdates_Reconcile_RemovedGeneratorProducesDelete(t *testing.T) {
	stored := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "my-stack", StackID: "stack-1", Length: 24}
	gg := NewGeneratorUpdateGenerator(&stubDatastore{
		generatorsByStack: map[string][]pkgmodel.Generator{"my-stack": {stored}},
	})

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "my-stack"}},
	}

	updates, err := gg.GenerateGeneratorUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	assert.Equal(t, GeneratorOperationDelete, updates[0].Operation)
	assert.Same(t, stored, updates[0].Generator)
	assert.Equal(t, "my-stack", updates[0].StackLabel)
}

func TestGenerateGeneratorUpdates_Patch_RemovedGeneratorProducesNoDelete(t *testing.T) {
	stored := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "my-stack", StackID: "stack-1", Length: 24}
	gg := NewGeneratorUpdateGenerator(&stubDatastore{
		generatorsByStack: map[string][]pkgmodel.Generator{"my-stack": {stored}},
	})

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "my-stack"}},
	}

	updates, err := gg.GenerateGeneratorUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.Empty(t, updates, "patch mode must not delete a generator absent from the declaration")
}

func TestGenerateGeneratorUpdates_Destroy_ProducesNoUpdates(t *testing.T) {
	stored := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "my-stack", StackID: "stack-1", Length: 24}
	gg := NewGeneratorUpdateGenerator(&stubDatastore{
		generatorsByStack: map[string][]pkgmodel.Generator{"my-stack": {stored}},
	})

	forma := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "my-stack"}}}

	updates, err := gg.GenerateGeneratorUpdates(forma, pkgmodel.CommandDestroy, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, updates, "a generator has no standalone form; it dies with its stack, implicitly")
}

// TestGenerateGeneratorUpdates_Rename covers the identity-preservation
// decision: a generator declared under a new label, carrying Alias set to
// its previous label, matches the stored row by Alias rather than reading as
// a delete-plus-create. The update it produces carries the new label
// forward and keeps ExistingGenerator pointed at the stored (old-labeled)
// row, so the persister can find and rename that same row rather than
// minting a new one.
func TestGenerateGeneratorUpdates_Rename(t *testing.T) {
	stored := &pkgmodel.PasswordGenerator{
		Label: "old-password", Stack: "my-stack", StackID: "stack-1",
		Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
	}
	gg := NewGeneratorUpdateGenerator(&stubDatastore{
		generatorsByStack: map[string][]pkgmodel.Generator{"my-stack": {stored}},
	})

	declared := passwordSpec("new-password", "my-stack", 24)
	declared["Alias"] = "old-password"

	forma := &pkgmodel.Forma{
		Stacks:     []pkgmodel.Stack{{Label: "my-stack"}},
		Generators: []json.RawMessage{rawGenerator(t, declared)},
	}

	updates, err := gg.GenerateGeneratorUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 1, "a rename must produce a single update, not a delete plus a create")

	assert.Equal(t, GeneratorOperationUpdate, updates[0].Operation)
	assert.Equal(t, "new-password", updates[0].Generator.GetLabel())
	assert.Same(t, stored, updates[0].ExistingGenerator, "the update must carry the stored (old-labeled) row so the persister can rename it in place")
}

func TestGenerateGeneratorUpdates_MultipleGeneratorsPerStackAreIndependentByLabel(t *testing.T) {
	gg := NewGeneratorUpdateGenerator(&stubDatastore{})

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "my-stack"}},
		Generators: []json.RawMessage{
			rawGenerator(t, passwordSpec("db-password", "my-stack", 24)),
			rawGenerator(t, passwordSpec("api-key", "my-stack", 40)),
		},
	}

	updates, err := gg.GenerateGeneratorUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 2, "two same-typed generators in one stack are distinct by label")
	assert.Equal(t, GeneratorOperationCreate, updates[0].Operation)
	assert.Equal(t, GeneratorOperationCreate, updates[1].Operation)
}

func TestGeneratorsEqual_IgnoresLabelStackAndAlias(t *testing.T) {
	a := &pkgmodel.PasswordGenerator{Label: "a", Stack: "s1", Alias: "old-a", Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true}
	b := &pkgmodel.PasswordGenerator{Label: "b", Stack: "s2", Alias: "", Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true}
	assert.True(t, generatorsEqual(a, b), "label, stack and alias are identity fields, not spec")
}

func TestGeneratorsEqual_DiffersOnExcludeCharacters(t *testing.T) {
	a := &pkgmodel.PasswordGenerator{Label: "a", Length: 24, ExcludeCharacters: "oO0"}
	b := &pkgmodel.PasswordGenerator{Label: "a", Length: 24, ExcludeCharacters: ""}
	assert.False(t, generatorsEqual(a, b))
}
