// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package transformations

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestPersistValueTransformer_HashesOpaqueValuesInProperties(t *testing.T) {
	transformer := NewPersistValueTransformer()
	input := &pkgmodel.Resource{
		Label: "test-resource",
		Type:  "secret.secretsmanager.aws",
		Properties: json.RawMessage(`{
            "Description": "my best secret ever",
            "Name": "my-secret-stable",
            "SecretString": {
                "$strategy": "SetOnce",
                "$value": "R4fvlOhyDila",
                "$visibility": "Opaque"
            },
            "ClearValue": {
                "$visibility": "Clear",
                "$value": "clear-data"
            }
        }`),
	}

	result, err := transformer.ApplyToResource(input)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.NotSame(t, input, result)
	assert.Equal(t, "test-resource", result.Label)
	assert.Equal(t, "secret.secretsmanager.aws", result.Type)

	parsed := gjson.Parse(string(result.Properties))
	assert.Equal(t, "my best secret ever", parsed.Get("Description").String())
	assert.Equal(t, "my-secret-stable", parsed.Get("Name").String())

	clearValue := parsed.Get("ClearValue")
	assert.Equal(t, "Clear", clearValue.Get("$visibility").String())
	assert.Equal(t, "clear-data", clearValue.Get("$value").String())

	secretString := parsed.Get("SecretString")
	assert.Equal(t, "SetOnce", secretString.Get("$strategy").String())
	assert.Equal(t, "Opaque", secretString.Get("$visibility").String())
	assert.True(t, secretString.Get("$value").Exists())
	assert.NotEqual(t, "R4fvlOhyDila", secretString.Get("$value").String())
	assert.Len(t, secretString.Get("$value").String(), 64)
}

func TestPersistValueTransformer_EmptyResource(t *testing.T) {
	transformer := NewPersistValueTransformer()
	input := &pkgmodel.Resource{
		Label: "empty-resource",
		Type:  "test.resource",
	}

	result, err := transformer.ApplyToResource(input)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "empty-resource", result.Label)
	assert.Equal(t, "test.resource", result.Type)
	assert.Empty(t, result.Properties)
	assert.Empty(t, result.ReadOnlyProperties)
}

func TestPersistValueTransformer_NonOpaqueValues(t *testing.T) {
	transformer := NewPersistValueTransformer()
	input := &pkgmodel.Resource{
		Label: "test-resource",
		Type:  "test.resource",
		Properties: json.RawMessage(`{
            "Config": {
                "$strategy": "Update",
                "$value": "some-value"
            }
        }`),
	}

	result, err := transformer.ApplyToResource(input)
	require.NoError(t, err)
	require.NotNil(t, result)

	parsed := gjson.Parse(string(result.Properties))
	config := parsed.Get("Config")
	assert.Equal(t, "Update", config.Get("$strategy").String())
	assert.Equal(t, "some-value", config.Get("$value").String())
}

func TestPersistValueTransformer_NilResource(t *testing.T) {
	transformer := NewPersistValueTransformer()
	result, err := transformer.ApplyToResource(nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "resource cannot be nil")
}

func TestPersistValueTransformer_ConsistentHashing(t *testing.T) {
	transformer := NewPersistValueTransformer()

	resource1 := &pkgmodel.Resource{
		Properties: json.RawMessage(`{
            "Secret": {
                "$visibility": "Opaque",
                "$value": "consistent-secret"
            }
        }`),
	}

	resource2 := &pkgmodel.Resource{
		Properties: json.RawMessage(`{
            "Secret": {
                "$visibility": "Opaque",
                "$value": "consistent-secret"
            }
        }`),
	}

	result1, err1 := transformer.ApplyToResource(resource1)
	require.NoError(t, err1)

	result2, err2 := transformer.ApplyToResource(resource2)
	require.NoError(t, err2)

	parsed1 := gjson.Parse(string(result1.Properties))
	parsed2 := gjson.Parse(string(result2.Properties))

	hash1 := parsed1.Get("Secret.$value").String()
	hash2 := parsed2.Get("Secret.$value").String()

	assert.Equal(t, hash1, hash2)
	assert.NotEmpty(t, hash1)
}

func TestPersistValueTransformer_DifferentValuesProduceDifferentHashes(t *testing.T) {
	transformer := NewPersistValueTransformer()

	resource1 := &pkgmodel.Resource{
		Properties: json.RawMessage(`{
            "Secret": {
                "$visibility": "Opaque",
                "$value": "secret-one"
            }
        }`),
	}

	resource2 := &pkgmodel.Resource{
		Properties: json.RawMessage(`{
            "Secret": {
                "$visibility": "Opaque",
                "$value": "secret-two"
            }
        }`),
	}

	result1, err1 := transformer.ApplyToResource(resource1)
	require.NoError(t, err1)

	result2, err2 := transformer.ApplyToResource(resource2)
	require.NoError(t, err2)

	parsed1 := gjson.Parse(string(result1.Properties))
	parsed2 := gjson.Parse(string(result2.Properties))

	hash1 := parsed1.Get("Secret.$value").String()
	hash2 := parsed2.Get("Secret.$value").String()

	assert.NotEqual(t, hash1, hash2)
	assert.NotEmpty(t, hash1)
	assert.NotEmpty(t, hash2)
}

func TestComputeValueHash_SimpleString(t *testing.T) {
	result := pkgmodel.ComputeValueHash("test")
	result2 := pkgmodel.ComputeValueHash("test")

	assert.Equal(t, result, result2)
	assert.Len(t, result, 64)
	assert.Regexp(t, "^[a-f0-9]+$", result)
}

func TestComputeValueHash_EmptyString(t *testing.T) {
	result := pkgmodel.ComputeValueHash("")
	result2 := pkgmodel.ComputeValueHash("")

	assert.Equal(t, result, result2)
	assert.Len(t, result, 64)
	assert.Regexp(t, "^[a-f0-9]+$", result)
}

func TestComputeValueHash_ComplexString(t *testing.T) {
	result := pkgmodel.ComputeValueHash("ExNlUX9SF9dV")
	result2 := pkgmodel.ComputeValueHash("ExNlUX9SF9dV")

	assert.Equal(t, result, result2)
	assert.Len(t, result, 64)
	assert.Regexp(t, "^[a-f0-9]+$", result)
}

func TestPersistValueTransformer_HashesHexShapedValueWithoutMarker(t *testing.T) {
	transformer := NewPersistValueTransformer()

	knownHash := "5c76fcf4400da3b4804d70b91af20703d483f2c5860cc2f8d59592a1da8d2121"

	input := &pkgmodel.Resource{
		Label: "test-resource",
		Type:  "AWS::RDS::DBInstance",
		Properties: json.RawMessage(fmt.Sprintf(`{
            "MasterUserPassword": {
                "$visibility": "Opaque",
                "$value": "%s"
            }
        }`, knownHash)),
	}

	result, err := transformer.ApplyToResource(input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Guards the false-positive fix: a $value that merely looks like a 64-hex
	// hash but carries no $hashed marker is treated as plaintext and hashed,
	// not trusted by shape alone.
	parsed := gjson.Parse(string(result.Properties))
	masterUserPassword := parsed.Get("MasterUserPassword")
	resultHash := masterUserPassword.Get("$value").String()

	assert.NotEqual(t, knownHash, resultHash)
	assert.Len(t, resultHash, 64)
	assert.True(t, masterUserPassword.Get("$hashed").Bool())
}

func schemaWithOpaque(field string) pkgmodel.Schema {
	return pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{field: {Opaque: true}}}
}

func TestApplyToResource_HashesBareStringAtSchemaOpaquePath(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaque("SecretString"),
		Properties: json.RawMessage(`{"Name":"n","SecretString":"super-secret-password"}`),
	}
	out, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	assert.Equal(t, "n", props["Name"], "non-secret field untouched")

	sv := props["SecretString"].(map[string]any)
	assert.Equal(t, "Opaque", sv["$visibility"])
	assert.Equal(t, true, sv["$hashed"])
	assert.Len(t, sv["$value"].(string), 64)
	assert.NotEqual(t, "super-secret-password", sv["$value"])
}

func TestApplyToResource_HashesEnvelopedOpaque(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     pkgmodel.Schema{},
		Properties: json.RawMessage(`{"SecretString":{"$value":"s","$visibility":"Opaque","$strategy":"Update"}}`),
	}
	out, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)
	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	sv := props["SecretString"].(map[string]any)
	assert.Equal(t, true, sv["$hashed"])
	assert.Len(t, sv["$value"].(string), 64)
}

func TestApplyToResource_IdempotentAndSkipsClear(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaque("SecretString"),
		Properties: json.RawMessage(`{"Public":"64charsofhexbutclear0000000000000000000000000000000000000000","SecretString":{"$value":"abc","$visibility":"Opaque","$hashed":true}}`),
	}
	out, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)
	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	assert.Equal(t, "64charsofhexbutclear0000000000000000000000000000000000000000", props["Public"], "clear 64-hex not hashed")
	assert.Equal(t, "abc", props["SecretString"].(map[string]any)["$value"], "already-hashed left as-is")
}

func TestApplyToResource_HashesTopLevelScalarPatchOpValue(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:        schemaWithOpaque("SecretString"),
		PatchDocument: json.RawMessage(`[{"op":"replace","path":"/SecretString","value":"super-secret-password"}]`),
	}
	out, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)
	var ops []map[string]any
	require.NoError(t, json.Unmarshal(out.PatchDocument, &ops))

	value, ok := ops[0]["value"].(map[string]any)
	require.True(t, ok, "hashed patch-op value must be a typed envelope, not a bare string")
	assert.Equal(t, true, value["$hashed"])
	assert.Equal(t, "Opaque", value["$visibility"])
	assert.NotEqual(t, "super-secret-password", value["$value"])
	assert.Len(t, value["$value"].(string), 64)
}

// TestApplyToResource_PatchOpHashingIsIdempotent guards against hash-of-hash: both
// hashSensitiveDataIfComplete (on FormaCommand completion) and BackfillHashedSecrets
// (on agent boot) re-run ApplyToResource against an already-hashed patch document.
// Re-running the transform on its own output must be a no-op.
func TestApplyToResource_PatchOpHashingIsIdempotent(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:        schemaWithOpaque("SecretString"),
		PatchDocument: json.RawMessage(`[{"op":"replace","path":"/SecretString","value":"super-secret-password"}]`),
	}

	firstRun, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	var ops []map[string]any
	require.NoError(t, json.Unmarshal(firstRun.PatchDocument, &ops))
	value, ok := ops[0]["value"].(map[string]any)
	require.True(t, ok, "hashed patch-op value must be a typed envelope")
	require.Equal(t, true, value["$hashed"])
	require.NotEqual(t, "super-secret-password", value["$value"])
	require.Len(t, value["$value"].(string), 64)

	// Re-run against the already-hashed output (as boot-time BackfillHashedSecrets
	// or a resumed completion pass would).
	secondRun, err := NewPersistValueTransformer().ApplyToResource(firstRun)
	require.NoError(t, err)

	assert.Equal(t, string(firstRun.PatchDocument), string(secondRun.PatchDocument),
		"re-hashing an already-hashed patch document must be a byte-identical no-op")
}
