// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TestTranslatePropertiesJSON_PreservesJSONKeyOnFlatRewrite verifies that a
// $res envelope carrying $json survives the flat $res→$ref rewrite with the
// $json key present on the output $ref object.
func TestTranslatePropertiesJSON_PreservesJSONKeyOnFlatRewrite(t *testing.T) {
	ds, _ := GetDeps(t)

	secretKsuid := "secretksuid01"
	triplet := pkgmodel.TripletKey{Stack: "default", Label: "mysecret", Type: "AWS::SecretsManager::Secret"}

	_, err := ds.StoreResource(&pkgmodel.Resource{
		Ksuid: secretKsuid,
		Label: triplet.Label,
		Type:  triplet.Type,
		Stack: triplet.Stack,
	}, "cmd-json-1")
	require.NoError(t, err)

	// A $res envelope WITH $json — the user wrote .json("db.password") in PKL.
	properties, err := json.Marshal(map[string]any{
		"Password": map[string]any{
			"$res":      true,
			"$label":    triplet.Label,
			"$type":     triplet.Type,
			"$stack":    triplet.Stack,
			"$property": "SecretString",
			"$json":     "db.password",
		},
	})
	require.NoError(t, err)

	tripletToKsuid := map[pkgmodel.TripletKey]string{triplet: secretKsuid}
	result, _, err := translatePropertiesJSON(json.RawMessage(properties), tripletToKsuid, ds)
	require.NoError(t, err)

	// After rewrite the $ref envelope must still carry $json.
	assert.Equal(t, "db.password", gjson.GetBytes(result, "Password.$json").String(),
		"$json must survive the $res→$ref rewrite; full result: %s", result)
	// And $ref must now be present (not $res).
	assert.True(t, gjson.GetBytes(result, "Password.$ref").Exists(),
		"$ref must be present after rewrite; full result: %s", result)
	assert.False(t, gjson.GetBytes(result, "Password.$res").Bool(),
		"$res must be gone after rewrite; full result: %s", result)
}

// TestTranslatePropertiesJSON_NoJSONKeyUnchanged verifies that a $res envelope
// WITHOUT $json produces a $ref object without $json (no spurious key added).
func TestTranslatePropertiesJSON_NoJSONKeyUnchanged(t *testing.T) {
	ds, _ := GetDeps(t)

	ksuid := "plainsecretksuid"
	triplet := pkgmodel.TripletKey{Stack: "default", Label: "plain", Type: "AWS::SecretsManager::Secret"}

	_, err := ds.StoreResource(&pkgmodel.Resource{
		Ksuid: ksuid,
		Label: triplet.Label,
		Type:  triplet.Type,
		Stack: triplet.Stack,
	}, "cmd-plain-1")
	require.NoError(t, err)

	properties, err := json.Marshal(map[string]any{
		"SecretString": map[string]any{
			"$res":      true,
			"$label":    triplet.Label,
			"$type":     triplet.Type,
			"$stack":    triplet.Stack,
			"$property": "SecretString",
		},
	})
	require.NoError(t, err)

	tripletToKsuid := map[pkgmodel.TripletKey]string{triplet: ksuid}
	result, _, err := translatePropertiesJSON(json.RawMessage(properties), tripletToKsuid, ds)
	require.NoError(t, err)

	assert.False(t, gjson.GetBytes(result, "SecretString.$json").Exists(),
		"$json must not appear when not authored; full result: %s", result)
}
