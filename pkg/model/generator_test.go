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

func TestParseGenerator_Password(t *testing.T) {
	raw := json.RawMessage(`{
		"Type": "password",
		"Label": "db-password",
		"Stack": "default",
		"Length": 24,
		"Uppercase": true,
		"Lowercase": true,
		"Digits": true,
		"Symbols": false,
		"ExcludeCharacters": "oO0",
		"RequireEachIncludedType": true
	}`)

	generator, err := ParseGenerator(raw)
	require.NoError(t, err)

	password, ok := generator.(*PasswordGenerator)
	require.True(t, ok, "expected *PasswordGenerator")

	assert.Equal(t, "password", password.GetType())
	assert.Equal(t, "db-password", password.GetLabel())
	assert.Equal(t, "default", password.GetStack())
	assert.Equal(t, 24, password.Length)
	assert.True(t, password.Uppercase)
	assert.True(t, password.Lowercase)
	assert.True(t, password.Digits)
	assert.False(t, password.Symbols)
	assert.Equal(t, "oO0", password.ExcludeCharacters)
	assert.True(t, password.RequireEachIncludedType)
}

// TestParseGenerator_Password_RoundTrip checks that marshaling a parsed
// PasswordGenerator back to JSON and re-parsing it produces an identical
// value, so the same shape written by the PKL schema and read by this parser
// survives a full round trip (as it does when a generator is stored, then
// reloaded to be re-applied).
func TestParseGenerator_Password_RoundTrip(t *testing.T) {
	raw := json.RawMessage(`{
		"Type": "password",
		"Label": "api-key-seed",
		"Stack": "secrets-stack",
		"Length": 40,
		"Uppercase": true,
		"Lowercase": true,
		"Digits": true,
		"Symbols": true,
		"ExcludeCharacters": "",
		"RequireEachIncludedType": false
	}`)

	generator, err := ParseGenerator(raw)
	require.NoError(t, err)

	marshaled, err := json.Marshal(generator)
	require.NoError(t, err)

	roundTripped, err := ParseGenerator(marshaled)
	require.NoError(t, err)

	assert.Equal(t, generator, roundTripped)
}

// TestParseGenerator_Password_MarshalInjectsType verifies that Type is not a
// second source of truth: a PasswordGenerator built without ever setting a
// Type field still marshals "Type": "password", matching GetType().
func TestParseGenerator_Password_MarshalInjectsType(t *testing.T) {
	password := &PasswordGenerator{Label: "unset-type", Length: 16}

	marshaled, err := json.Marshal(password)
	require.NoError(t, err)

	var decoded struct {
		Type string `json:"Type"`
	}
	require.NoError(t, json.Unmarshal(marshaled, &decoded))
	assert.Equal(t, password.GetType(), decoded.Type)
}

func TestParseGenerator_UnknownType(t *testing.T) {
	raw := json.RawMessage(`{"Type": "unknown-generator", "Label": "x"}`)

	_, err := ParseGenerator(raw)
	require.Error(t, err)
	assert.ErrorContains(t, err, "unknown generator type")
}

func TestParseGenerators_Multiple(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{"Type": "password", "Label": "one", "Length": 16, "Uppercase": true, "Lowercase": true, "Digits": true, "Symbols": false, "RequireEachIncludedType": true}`),
		json.RawMessage(`{"Type": "password", "Label": "two", "Length": 32, "Uppercase": true, "Lowercase": true, "Digits": true, "Symbols": false, "RequireEachIncludedType": true}`),
	}

	generators, err := ParseGenerators(raw)
	require.NoError(t, err)
	require.Len(t, generators, 2)
	assert.Equal(t, "one", generators[0].GetLabel())
	assert.Equal(t, "two", generators[1].GetLabel())
}

// A generator's rotation cadence round-trips through the JSON the PKL schema
// renders: the nested Rotation object carries the interval in seconds, and a
// generator that declares no cadence carries no rotation at all.
func TestParseGenerator_Rotation(t *testing.T) {
	raw := json.RawMessage(`{
		"Type": "password",
		"Label": "db-password",
		"Stack": "default",
		"Rotation": {"EverySeconds": 86400},
		"Length": 24,
		"Uppercase": true,
		"Lowercase": true,
		"Digits": true,
		"Symbols": false,
		"RequireEachIncludedType": true
	}`)

	generator, err := ParseGenerator(raw)
	require.NoError(t, err)

	rotation := generator.GetRotation()
	require.NotNil(t, rotation, "a declared cadence must reach the model")
	assert.Equal(t, 86400, rotation.EverySeconds)

	roundTripped, err := json.Marshal(generator)
	require.NoError(t, err)
	reparsed, err := ParseGenerator(roundTripped)
	require.NoError(t, err)
	require.NotNil(t, reparsed.GetRotation())
	assert.Equal(t, 86400, reparsed.GetRotation().EverySeconds)
}

func TestParseGenerator_NoRotation(t *testing.T) {
	raw := json.RawMessage(`{
		"Type": "password",
		"Label": "db-password",
		"Stack": "default",
		"Rotation": null,
		"Length": 24,
		"RequireEachIncludedType": true
	}`)

	generator, err := ParseGenerator(raw)
	require.NoError(t, err)
	assert.Nil(t, generator.GetRotation(),
		"a generator that declares no cadence must hold none")

	// The absent cadence must also stay out of the marshalled spec: the spec
	// is what a drawn generation is recorded under, and a key that only
	// appears as null is one more thing two specs can differ by.
	roundTripped, err := json.Marshal(generator)
	require.NoError(t, err)
	assert.NotContains(t, string(roundTripped), "Rotation")
}
