// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resolver

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A drawn value lands INSIDE the $gen envelope, as its $value. The envelope
// itself survives untouched, $visibility:"Opaque" included: that marker is
// the only reason the persist path hashes the value at rest, so replacing the
// envelope with a bare scalar would persist the credential in cleartext.
func TestSetGenValues_WritesTheValueInsideTheEnvelope(t *testing.T) {
	properties := json.RawMessage(`{
		"Name": "db",
		"SecretString": {"$gen": true, "$generator": "gen-ksuid", "$output": "value", "$visibility": "Opaque"}
	}`)

	updated, err := SetGenValues(properties, []string{"SecretString"}, "drawn-credential")
	require.NoError(t, err)

	envelope := gjson.GetBytes(updated, "SecretString")
	require.True(t, envelope.IsObject(), "the envelope must still be an object, not a bare scalar")
	assert.Equal(t, "drawn-credential", envelope.Get("$value").String())
	assert.True(t, envelope.Get("$gen").Bool(), "the $gen marker must survive")
	assert.Equal(t, "gen-ksuid", envelope.Get("$generator").String())
	assert.Equal(t, "value", envelope.Get("$output").String())
	assert.Equal(t, "Opaque", envelope.Get("$visibility").String(),
		"the opaque marker is what makes the value hash at rest and must survive")
	assert.Equal(t, "db", gjson.GetBytes(updated, "Name").String(), "unrelated fields are untouched")
}

// Only the paths named are written. Another generator's destination in the
// same document keeps its undrawn envelope.
func TestSetGenValues_WritesOnlyTheNamedPaths(t *testing.T) {
	properties := json.RawMessage(`{
		"First": {"$gen": true, "$generator": "a", "$output": "value", "$visibility": "Opaque"},
		"Nested": {"Second": {"$gen": true, "$generator": "b", "$output": "value", "$visibility": "Opaque"}}
	}`)

	updated, err := SetGenValues(properties, []string{"Nested.Second"}, "drawn")
	require.NoError(t, err)

	assert.Equal(t, "drawn", gjson.GetBytes(updated, "Nested.Second.$value").String())
	assert.False(t, gjson.GetBytes(updated, "First.$value").Exists(),
		"a destination that was not named must keep its undrawn envelope")
}

// A path that does not hold a generator envelope is refused rather than
// overwritten: writing a drawn credential over an arbitrary node would put a
// live secret somewhere nothing marked opaque.
func TestSetGenValues_RefusesAPathThatIsNotAGeneratorEnvelope(t *testing.T) {
	properties := json.RawMessage(`{"Name": "db", "Ref": {"$ref": "formae://k#/Token"}}`)

	_, err := SetGenValues(properties, []string{"Name"}, "drawn")
	require.Error(t, err)

	_, err = SetGenValues(properties, []string{"Ref"}, "drawn")
	require.Error(t, err, "a $ref envelope is not a generator envelope")
}

// A path that is not present at all is refused: the caller derived it from
// this same document, so an absent path means the two have diverged.
func TestSetGenValues_RefusesAnAbsentPath(t *testing.T) {
	properties := json.RawMessage(`{"Name": "db"}`)

	_, err := SetGenValues(properties, []string{"SecretString"}, "drawn")
	require.Error(t, err)
}

// The error names the path and never the value.
func TestSetGenValues_ErrorNeverCarriesTheValue(t *testing.T) {
	properties := json.RawMessage(`{"Name": "db"}`)

	_, err := SetGenValues(properties, []string{"SecretString"}, "drawn-credential")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "drawn-credential")
	assert.Contains(t, err.Error(), "SecretString")
}

// An array element addressed by index is written the same way.
func TestSetGenValues_WritesIntoAnArrayElement(t *testing.T) {
	properties := json.RawMessage(`{
		"Entries": [{"$gen": true, "$generator": "a", "$output": "value", "$visibility": "Opaque"}]
	}`)

	updated, err := SetGenValues(properties, []string{"Entries.0"}, "drawn")
	require.NoError(t, err)
	assert.Equal(t, "drawn", gjson.GetBytes(updated, "Entries.0.$value").String())
	assert.Equal(t, "Opaque", gjson.GetBytes(updated, "Entries.0.$visibility").String())
}
