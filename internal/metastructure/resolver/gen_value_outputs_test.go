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

// A multi-output draw delivers each occurrence the output it names: the
// private half to the destination bound to privateKey, the public half to the
// one bound to publicKey. Fanning one string to both is exactly the failure
// mode output-aware delivery exists to prevent: both destinations would apply
// cleanly holding the wrong key material and nothing would error.
func TestSetGenValues_SelectsTheNamedOutputPerPath(t *testing.T) {
	properties := json.RawMessage(`{
		"PrivatePem": {"$gen": true, "$generator": "kp", "$output": "privateKey", "$visibility": "Opaque"},
		"PublicPem":  {"$gen": true, "$generator": "kp", "$output": "publicKey", "$visibility": "Opaque"}
	}`)

	updated, err := SetGenValues(properties, "kp", []string{"PrivatePem", "PublicPem"},
		map[string]string{"privateKey": "PRIVATE-PEM", "publicKey": "PUBLIC-PEM"})
	require.NoError(t, err)

	assert.Equal(t, "PRIVATE-PEM", gjson.GetBytes(updated, "PrivatePem.$value").String())
	assert.Equal(t, "PUBLIC-PEM", gjson.GetBytes(updated, "PublicPem.$value").String())
}

// An envelope naming no $output means the single-output arm's "value": the
// shape every pre-multi-output forma renders, which must keep working.
func TestSetGenValues_AbsentOutputMeansValue(t *testing.T) {
	properties := json.RawMessage(`{
		"SecretString": {"$gen": true, "$generator": "gen-ksuid", "$visibility": "Opaque"}
	}`)

	updated, err := SetGenValues(properties, "gen-ksuid", []string{"SecretString"},
		map[string]string{"value": "drawn"})
	require.NoError(t, err)
	assert.Equal(t, "drawn", gjson.GetBytes(updated, "SecretString.$value").String())
}

// An occurrence naming an output the draw did not produce is a hard error
// naming the path and the output, never a silent skip and never a fallback to
// some other output's value. This is the delivery-time half of output
// validation: translation checks names against the union across kinds, so a
// password-bound destination asking for privateKey is only caught here.
func TestSetGenValues_MissingOutputIsARefusalNamingPathAndOutput(t *testing.T) {
	properties := json.RawMessage(`{
		"Ok":    {"$gen": true, "$generator": "pw", "$output": "value", "$visibility": "Opaque"},
		"Wrong": {"$gen": true, "$generator": "pw", "$output": "privateKey", "$visibility": "Opaque"}
	}`)

	_, err := SetGenValues(properties, "pw", []string{"Ok", "Wrong"},
		map[string]string{"value": "drawn"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Wrong", "the error must name the path")
	assert.Contains(t, err.Error(), "privateKey", "the error must name the output")
	assert.NotContains(t, err.Error(), "drawn", "the error must never carry a value")
}
