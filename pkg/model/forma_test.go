// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSplitByStack_GeneratorLandsOnNamedStack verifies that a generator
// belonging to a stack that also has resources is carried into that stack's
// split Forma, alongside the resources already routed there.
func TestSplitByStack_GeneratorLandsOnNamedStack(t *testing.T) {
	generator := json.RawMessage(`{
		"Type": "password",
		"Label": "db-password",
		"Stack": "app",
		"Length": 24,
		"Uppercase": true,
		"Lowercase": true,
		"Digits": true,
		"Symbols": false,
		"RequireEachIncludedType": true
	}`)

	forma := Forma{
		Stacks: []Stack{{Label: "app"}},
		Resources: []Resource{
			{Label: "bucket", Type: "FakeAWS::S3::Bucket", Stack: "app", Target: "aws"},
		},
		Generators: []json.RawMessage{generator},
	}

	split := forma.SplitByStack()
	require.Len(t, split, 1)

	appForma := split[0]
	require.Len(t, appForma.Resources, 1)
	require.Len(t, appForma.Generators, 1)
	assert.JSONEq(t, string(generator), string(appForma.Generators[0]))
}

// TestSplitByStack_GeneratorOnlyStack verifies that a generator whose stack
// carries no resources still produces a split Forma for that stack — a
// generator-only stack is a legitimate shape, not just an appendage to a
// resource-bearing one.
func TestSplitByStack_GeneratorOnlyStack(t *testing.T) {
	generator := json.RawMessage(`{
		"Type": "password",
		"Label": "seed",
		"Stack": "secrets",
		"Length": 16,
		"Uppercase": true,
		"Lowercase": true,
		"Digits": true,
		"Symbols": false,
		"RequireEachIncludedType": true
	}`)

	forma := Forma{
		Stacks:     []Stack{{Label: "secrets", Description: "generator-only stack"}},
		Generators: []json.RawMessage{generator},
	}

	split := forma.SplitByStack()
	require.Len(t, split, 1)

	secretsForma := split[0]
	assert.Empty(t, secretsForma.Resources)
	require.Len(t, secretsForma.Generators, 1)
	assert.JSONEq(t, string(generator), string(secretsForma.Generators[0]))
	require.Len(t, secretsForma.Stacks, 1)
	assert.Equal(t, "secrets", secretsForma.Stacks[0].Label)
	assert.Equal(t, "generator-only stack", secretsForma.Stacks[0].Description)
}

// TestSplitByStack_TwoGeneratorsDifferentStacks verifies that generators
// naming different stacks are routed to their own split Forma, not merged.
func TestSplitByStack_TwoGeneratorsDifferentStacks(t *testing.T) {
	genA := json.RawMessage(`{"Type": "password", "Label": "a", "Stack": "stack-a", "Length": 16, "Uppercase": true, "Lowercase": true, "Digits": true, "Symbols": false, "RequireEachIncludedType": true}`)
	genB := json.RawMessage(`{"Type": "password", "Label": "b", "Stack": "stack-b", "Length": 16, "Uppercase": true, "Lowercase": true, "Digits": true, "Symbols": false, "RequireEachIncludedType": true}`)

	forma := Forma{
		Stacks:     []Stack{{Label: "stack-a"}, {Label: "stack-b"}},
		Generators: []json.RawMessage{genA, genB},
	}

	split := forma.SplitByStack()
	require.Len(t, split, 2)

	byStack := map[string]Forma{}
	for _, f := range split {
		require.Len(t, f.Stacks, 1)
		byStack[f.Stacks[0].Label] = f
	}

	require.Contains(t, byStack, "stack-a")
	require.Contains(t, byStack, "stack-b")
	require.Len(t, byStack["stack-a"].Generators, 1)
	require.Len(t, byStack["stack-b"].Generators, 1)
	assert.JSONEq(t, string(genA), string(byStack["stack-a"].Generators[0]))
	assert.JSONEq(t, string(genB), string(byStack["stack-b"].Generators[0]))
}
