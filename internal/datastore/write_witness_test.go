// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package datastore

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeWriteWitness_CreateOnly_IsTheEcho(t *testing.T) {
	w := ComposeWriteWitness([]WriteVersion{{
		Operation:  "create",
		Properties: json.RawMessage(`{"a":1,"rotation":"off"}`),
	}})
	assert.JSONEq(t, `{"a":1,"rotation":"off"}`, string(w))
}

func TestComposeWriteWitness_UpdateOverlaysOnlyWrittenFields(t *testing.T) {
	w := ComposeWriteWitness([]WriteVersion{
		{
			Operation:  "update",
			Patch:      json.RawMessage(`[{"op":"replace","path":"/healthCheckPath","value":"/live"}]`),
			Properties: json.RawMessage(`{"healthCheckPath":"/live","rotation":"off","targets":[{"Id":"10.0.0.5"}]}`),
		},
		{
			Operation:  "create",
			Properties: json.RawMessage(`{"healthCheckPath":"/","rotation":"off","targets":[]}`),
		},
	})
	var m map[string]any
	require.NoError(t, json.Unmarshal(w, &m))
	assert.Equal(t, "/live", m["healthCheckPath"])
	assert.Equal(t, "off", m["rotation"])
	assert.Empty(t, m["targets"], "the update did not write targets; its echo must not witness them")
}

func TestComposeWriteWitness_NewerUpdateWins(t *testing.T) {
	w := ComposeWriteWitness([]WriteVersion{
		{Operation: "update", Patch: json.RawMessage(`[{"op":"replace","path":"/a","value":3}]`), Properties: json.RawMessage(`{"a":3}`)},
		{Operation: "update", Patch: json.RawMessage(`[{"op":"replace","path":"/a","value":2}]`), Properties: json.RawMessage(`{"a":2}`)},
		{Operation: "create", Properties: json.RawMessage(`{"a":1}`)},
	})
	var m map[string]any
	require.NoError(t, json.Unmarshal(w, &m))
	assert.Equal(t, float64(3), m["a"])
}

func TestComposeWriteWitness_UpdateRemovingField_RemovesWitness(t *testing.T) {
	w := ComposeWriteWitness([]WriteVersion{
		{Operation: "update", Patch: json.RawMessage(`[{"op":"remove","path":"/b"}]`), Properties: json.RawMessage(`{"a":1}`)},
		{Operation: "create", Properties: json.RawMessage(`{"a":1,"b":2}`)},
	})
	var m map[string]any
	require.NoError(t, json.Unmarshal(w, &m))
	_, has := m["b"]
	assert.False(t, has)
}

func TestComposeWriteWitness_NoCreateInHistory_NoWitness(t *testing.T) {
	w := ComposeWriteWitness([]WriteVersion{
		{Operation: "update", Patch: json.RawMessage(`[{"op":"replace","path":"/a","value":2}]`), Properties: json.RawMessage(`{"a":2}`)},
	})
	assert.Nil(t, w, "without the create echo the base is unknown; fail toward no witness")
}

func TestComposeWriteWitness_NestedPathsWitnessTopLevelField(t *testing.T) {
	w := ComposeWriteWitness([]WriteVersion{
		{
			Operation:  "update",
			Patch:      json.RawMessage(`[{"op":"replace","path":"/BucketEncryption/ServerSideEncryptionConfiguration/0","value":{}}]`),
			Properties: json.RawMessage(`{"BucketEncryption":{"x":1},"targets":[{"Id":"i"}]}`),
		},
		{Operation: "create", Properties: json.RawMessage(`{"BucketEncryption":{"y":2},"targets":[]}`)},
	})
	var m map[string]any
	require.NoError(t, json.Unmarshal(w, &m))
	assert.Equal(t, map[string]any{"x": float64(1)}, m["BucketEncryption"])
	assert.Empty(t, m["targets"])
}
