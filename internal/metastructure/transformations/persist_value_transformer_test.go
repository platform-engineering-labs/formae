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

	result, _, err := transformer.ApplyToResource(input)
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

	result, _, err := transformer.ApplyToResource(input)
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

	result, _, err := transformer.ApplyToResource(input)
	require.NoError(t, err)
	require.NotNil(t, result)

	parsed := gjson.Parse(string(result.Properties))
	config := parsed.Get("Config")
	assert.Equal(t, "Update", config.Get("$strategy").String())
	assert.Equal(t, "some-value", config.Get("$value").String())
}

func TestPersistValueTransformer_NilResource(t *testing.T) {
	transformer := NewPersistValueTransformer()
	result, _, err := transformer.ApplyToResource(nil)
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

	result1, _, err1 := transformer.ApplyToResource(resource1)
	require.NoError(t, err1)

	result2, _, err2 := transformer.ApplyToResource(resource2)
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

	result1, _, err1 := transformer.ApplyToResource(resource1)
	require.NoError(t, err1)

	result2, _, err2 := transformer.ApplyToResource(resource2)
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

	result, _, err := transformer.ApplyToResource(input)
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
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	assert.Equal(t, "n", props["Name"], "non-secret field untouched")

	sv := props["SecretString"].(map[string]any)
	assert.Equal(t, "Opaque", sv["$visibility"])
	assert.Equal(t, true, sv["$hashed"])
	assert.Len(t, sv["$value"].(string), 64)
	assert.NotEqual(t, "super-secret-password", sv["$value"])
	// A bare-string literal on an opaque field must be wrapped into a CANONICAL
	// formae.Value carrying $strategy (the shape a `formae.value(x).opaque` literal
	// produces). Without it the extract PKL generator does not recognize the value
	// as opaque and, on a field whose type union includes formae.Resolvable, emits
	// a label-less {$res,$visibility:Opaque} that fails to evaluate.
	assert.Equal(t, "Update", sv["$strategy"], "hashed bare-scalar opaque value must carry the default $strategy")
}

func TestApplyToResource_HashesEnvelopedOpaque(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     pkgmodel.Schema{},
		Properties: json.RawMessage(`{"SecretString":{"$value":"s","$visibility":"Opaque","$strategy":"Update"}}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)
	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	sv := props["SecretString"].(map[string]any)
	assert.Equal(t, true, sv["$hashed"])
	assert.Len(t, sv["$value"].(string), 64)
}

func TestApplyToResource_HashesMapValuedSchemaOpaqueField(t *testing.T) {
	// A map-shaped secret field (e.g. K8S decodedData) is itself the secret value.
	// It must be hashed into ONE opaque envelope, never left with plaintext keys
	// beside a nil $value.
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaque("decodedData"),
		Properties: json.RawMessage(`{"Name":"n","decodedData":{"username":"admin","password":"s3cr3t"}}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	assert.Equal(t, "n", props["Name"], "non-secret field untouched")

	dd := props["decodedData"].(map[string]any)
	assert.Equal(t, "Opaque", dd["$visibility"])
	assert.Equal(t, true, dd["$hashed"])
	assert.Equal(t, "Update", dd["$strategy"])
	assert.Len(t, dd["$value"].(string), 64, "the whole map is hashed into one digest")
	_, hasUser := dd["username"]
	_, hasPass := dd["password"]
	assert.False(t, hasUser, "plaintext username key must not survive at rest")
	assert.False(t, hasPass, "plaintext password key must not survive at rest")
	assert.NotContains(t, dd["$value"].(string), "admin")
	assert.NotContains(t, dd["$value"].(string), "s3cr3t")
}

func TestApplyToResource_MapSecretWithValueKeyIsNotMistakenForEnvelope(t *testing.T) {
	// A map-shaped secret whose keys happen to include one literally named "$value"
	// but NO $visibility is not a formae envelope — it is the secret value. It must
	// be hashed as a whole, not hashed-in-place (which would digest only the
	// "$value" key and leave sibling plaintext at rest).
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaque("decodedData"),
		Properties: json.RawMessage(`{"decodedData":{"password":"s3cr3t","$value":"not-an-envelope"}}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	dd := props["decodedData"].(map[string]any)
	assert.Equal(t, "Opaque", dd["$visibility"])
	assert.Equal(t, true, dd["$hashed"])
	assert.Len(t, dd["$value"].(string), 64, "the whole map is hashed into one digest")
	_, hasPass := dd["password"]
	assert.False(t, hasPass, "sibling plaintext key must not survive at rest")
	assert.NotContains(t, dd["$value"].(string), "s3cr3t")
	assert.NotContains(t, dd["$value"].(string), "not-an-envelope",
		"the coincidental $value key must be hashed, not surfaced as plaintext")
}

func TestApplyToResource_IdempotentAndSkipsClear(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaque("SecretString"),
		Properties: json.RawMessage(`{"Public":"64charsofhexbutclear0000000000000000000000000000000000000000","SecretString":{"$value":"abc","$visibility":"Opaque","$hashed":true}}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
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
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)
	var ops []map[string]any
	require.NoError(t, json.Unmarshal(out.PatchDocument, &ops))

	value, ok := ops[0]["value"].(map[string]any)
	require.True(t, ok, "hashed patch-op value must be a typed envelope, not a bare string")
	assert.Equal(t, true, value["$hashed"])
	assert.Equal(t, "Opaque", value["$visibility"])
	assert.NotEqual(t, "super-secret-password", value["$value"])
	assert.Len(t, value["$value"].(string), 64)
	assert.Equal(t, "Update", value["$strategy"], "hashed bare-scalar patch-op value must carry the default $strategy")
}

// TestApplyToResource_EnvelopeWithoutStrategyGetsCanonicalStrategy covers
// canonicalization for an opaque envelope that arrives WITHOUT $strategy
// (e.g. an enriched plugin Read that wraps only {$value,$visibility}): hashing it
// at rest must default $strategy to "Update" so the stored value is a canonical
// formae.Value.
func TestApplyToResource_EnvelopeWithoutStrategyGetsCanonicalStrategy(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaque("SecretString"),
		Properties: json.RawMessage(`{"SecretString":{"$value":"plaintext","$visibility":"Opaque"}}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)
	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	sv := props["SecretString"].(map[string]any)
	assert.Equal(t, true, sv["$hashed"])
	assert.Equal(t, "Update", sv["$strategy"], "an opaque envelope missing $strategy must be canonicalized to Update")
}

// TestApplyToResource_EnvelopePreservesExplicitSetOnce guards that
// canonicalization never overrides an explicitly-declared SetOnce strategy.
func TestApplyToResource_EnvelopePreservesExplicitSetOnce(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaque("SecretString"),
		Properties: json.RawMessage(`{"SecretString":{"$value":"plaintext","$visibility":"Opaque","$strategy":"SetOnce"}}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)
	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	sv := props["SecretString"].(map[string]any)
	assert.Equal(t, true, sv["$hashed"])
	assert.Equal(t, "SetOnce", sv["$strategy"], "explicit SetOnce must be preserved, not overridden")
}

// TestApplyToResource_PatchOpNonSecretValueCollidingWithSecretPlaintextIsUntouched guards
// against content-based substitution: a non-secret patch op whose value happens to equal a
// schema-opaque field's plaintext must not be rewritten. The old valueMap-based substitution
// keyed purely on value equality, so it corrupted unrelated fields and left a bare (unmarked)
// digest behind that hashOpaqueField would treat as plaintext and re-hash on the next boot
// backfill.
func TestApplyToResource_PatchOpNonSecretValueCollidingWithSecretPlaintextIsUntouched(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema: schemaWithOpaque("SecretString"),
		Properties: json.RawMessage(`{
            "SecretString": "same"
        }`),
		PatchDocument: json.RawMessage(`[{"op":"replace","path":"/Description","value":"same"}]`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	var ops []map[string]any
	require.NoError(t, json.Unmarshal(out.PatchDocument, &ops))

	value, ok := ops[0]["value"].(string)
	require.True(t, ok, "non-secret patch-op value must remain a bare string")
	assert.Equal(t, "same", value)
}

// TestApplyToResource_HashesOpaqueEnvelopePatchOpValue covers a patch op whose value is an
// explicit opaque envelope not matched by schema path (e.g. a nested/non-top-level field). It
// must be hashed structurally, and a second pass must be a no-op.
func TestApplyToResource_HashesOpaqueEnvelopePatchOpValue(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:        pkgmodel.Schema{},
		PatchDocument: json.RawMessage(`[{"op":"replace","path":"/Whatever","value":{"$value":"s","$visibility":"Opaque"}}]`),
	}

	firstRun, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	var ops []map[string]any
	require.NoError(t, json.Unmarshal(firstRun.PatchDocument, &ops))
	value, ok := ops[0]["value"].(map[string]any)
	require.True(t, ok, "opaque envelope patch-op value must remain a map")
	assert.Equal(t, true, value["$hashed"])
	assert.Equal(t, "Opaque", value["$visibility"])
	assert.NotEqual(t, "s", value["$value"])
	assert.Len(t, value["$value"].(string), 64)

	secondRun, _, err := NewPersistValueTransformer().ApplyToResource(firstRun)
	require.NoError(t, err)
	assert.Equal(t, string(firstRun.PatchDocument), string(secondRun.PatchDocument),
		"re-hashing an already-hashed envelope patch-op value must be a byte-identical no-op")
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

	firstRun, _, err := NewPersistValueTransformer().ApplyToResource(r)
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
	secondRun, _, err := NewPersistValueTransformer().ApplyToResource(firstRun)
	require.NoError(t, err)

	assert.Equal(t, string(firstRun.PatchDocument), string(secondRun.PatchDocument),
		"re-hashing an already-hashed patch document must be a byte-identical no-op")
}

// TestApplyToResource_HashesKnownTypeFieldWithoutSchemaOpaque proves the hard-coded
// known-opaque table (keyed on resource Type) hashes a secret even when the plugin's
// schema does NOT mark the field opaque — the real-plugin case where the plugin was
// built against an SDK predating FieldHint.Opaque, so its runtime schema drops it.
// Without this, discovery/sync of such a resource persists the secret in cleartext.
func TestApplyToResource_HashesKnownTypeFieldWithoutSchemaOpaque(t *testing.T) {
	r := &pkgmodel.Resource{
		Type:       "AWS::SecretsManager::Secret",
		Schema:     pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"SecretString": {Opaque: false}}},
		Properties: json.RawMessage(`{"Name":"n","SecretString":"super-secret-discovered"}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	assert.Equal(t, "n", props["Name"], "non-secret field untouched")

	sv, ok := props["SecretString"].(map[string]any)
	require.True(t, ok, "SecretString must be wrapped into a hashed envelope, got: %v", props["SecretString"])
	assert.Equal(t, true, sv["$hashed"])
	assert.Equal(t, "Opaque", sv["$visibility"])
	assert.Len(t, sv["$value"].(string), 64)
	assert.NotEqual(t, "super-secret-discovered", sv["$value"])
}

// Unknown resource types are unaffected — a bare string on a non-opaque field of an
// unknown type is left as-is (no over-hashing).
func TestApplyToResource_LeavesUnknownTypeBareStringAlone(t *testing.T) {
	r := &pkgmodel.Resource{
		Type:       "AWS::S3::Bucket",
		Schema:     pkgmodel.Schema{},
		Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)
	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	assert.Equal(t, "my-bucket", props["BucketName"])
}

func schemaWithOpaqueFields(fields ...string) pkgmodel.Schema {
	hints := make(map[string]pkgmodel.FieldHint, len(fields))
	for _, f := range fields {
		hints[f] = pkgmodel.FieldHint{Opaque: true}
	}
	return pkgmodel.Schema{Hints: hints}
}

// A SecretValue-typed property inside a SubResource is emitted as a dotted hint
// name. It must be hashed at rest exactly as a top-level one is.
func TestApplyToResource_HashesNestedOpaqueField(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaqueFields("settings.password"),
		Properties: json.RawMessage(`{"name":"cp","settings":{"host":"smtp.example.com","password":"hunter2"}}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	assert.Equal(t, "cp", props["name"])

	settings := props["settings"].(map[string]any)
	assert.Equal(t, "smtp.example.com", settings["host"], "non-secret sibling untouched")

	pw := settings["password"].(map[string]any)
	assert.Equal(t, "Opaque", pw["$visibility"])
	assert.Equal(t, "Update", pw["$strategy"])
	assert.Equal(t, true, pw["$hashed"])
	assert.Len(t, pw["$value"].(string), 64)
	assert.NotContains(t, string(out.Properties), "hunter2")
}

// A provider payload may return a flat key that genuinely contains a dot. It
// must match the hint as itself — a gjson-based implementation would read it as
// a nested path and leave the secret in cleartext.
func TestApplyToResource_HashesFlatDotContainingOpaqueKey(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaqueFields("hmacConfig.secret"),
		Properties: json.RawMessage(`{"url":"https://example.com","hmacConfig.secret":"s3cr3t"}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	var props map[string]any
	require.NoError(t, json.Unmarshal(out.Properties, &props))
	assert.Equal(t, "https://example.com", props["url"])

	secret := props["hmacConfig.secret"].(map[string]any)
	assert.Equal(t, true, secret["$hashed"])
	assert.NotContains(t, string(out.Properties), "s3cr3t")
}

// Hint names for a Listing<SubResource> carry no index, so every element is
// covered by the one hint.
func TestApplyToResource_HashesNestedOpaqueFieldInEveryListElement(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaqueFields("webhooks.password"),
		Properties: json.RawMessage(`{"webhooks":[{"url":"u1","password":"a"},{"url":"u2","password":"b"}]}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	parsed := gjson.ParseBytes(out.Properties)
	for i, plaintext := range []string{"a", "b"} {
		hook := parsed.Get(fmt.Sprintf("webhooks.%d", i))
		assert.True(t, hook.Get("password.$hashed").Bool(), "element %d hashed", i)
		assert.Len(t, hook.Get("password.$value").String(), 64)
		assert.NotEqual(t, plaintext, hook.Get("password.$value").String())
		assert.Equal(t, fmt.Sprintf("u%d", i+1), hook.Get("url").String(), "non-secret sibling untouched")
	}
}

// A read-only payload's structure can differ from the writable one, so it needs
// its own coverage rather than inheriting the Properties test.
func TestApplyToResource_HashesNestedOpaqueFieldInReadOnlyProperties(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:             schemaWithOpaqueFields("status.token"),
		ReadOnlyProperties: json.RawMessage(`{"status":{"phase":"Ready","token":"t0ken"}}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	parsed := gjson.ParseBytes(out.ReadOnlyProperties)
	assert.Equal(t, "Ready", parsed.Get("status.phase").String())
	assert.True(t, parsed.Get("status.token.$hashed").Bool())
	assert.NotContains(t, string(out.ReadOnlyProperties), "t0ken")
}

func TestApplyToResource_IsIdempotentForNestedOpaqueField(t *testing.T) {
	transformer := NewPersistValueTransformer()
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaqueFields("settings.password"),
		Properties: json.RawMessage(`{"settings":{"password":"hunter2"}}`),
	}

	first, _, err := transformer.ApplyToResource(r)
	require.NoError(t, err)
	second, _, err := transformer.ApplyToResource(&pkgmodel.Resource{Schema: r.Schema, Properties: first.Properties})
	require.NoError(t, err)

	assert.JSONEq(t, string(first.Properties), string(second.Properties), "a stored hash is never re-hashed")
}

// An inline $visibility=Opaque envelope is discovered structurally, at any
// depth, with no hint at all — that branch is separate from name matching and
// must survive the move onto the shared walker.
func TestApplyToResource_HashesNestedInlineOpaqueEnvelopeWithoutAnyHint(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     pkgmodel.Schema{},
		Properties: json.RawMessage(`{"a":{"b":{"secret":{"$value":"s","$visibility":"Opaque","$strategy":"Update"}}}}`),
	}
	out, _, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	parsed := gjson.ParseBytes(out.Properties)
	assert.True(t, parsed.Get("a.b.secret.$hashed").Bool())
	assert.Len(t, parsed.Get("a.b.secret.$value").String(), 64)
}

// Over-matching is the accepted cost of prefix concatenation, so it has to be
// observable: a hint that matched under two distinct readings is reported.
func TestApplyToResource_ReportsAmbiguousNestedHint(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaqueFields("a.b.c"),
		Properties: json.RawMessage(`{"a":{"b":{"c":"s1"}},"a.b":{"c":"s2"}}`),
	}
	out, diags, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)

	require.Len(t, diags, 1)
	assert.Equal(t, DiagnosticWarn, diags[0].Severity)
	assert.Equal(t, "a.b.c", diags[0].Hint)
	assert.NotContains(t, string(out.Properties), "s1", "matching is still fail-safe under ambiguity")
	assert.NotContains(t, string(out.Properties), "s2")
}

// An ordinary list produces many concrete matches under ONE reading, which is
// not ambiguous — reporting it would make the diagnostic worthless.
func TestApplyToResource_ListOfSubResourcesReportsNoAmbiguity(t *testing.T) {
	r := &pkgmodel.Resource{
		Schema:     schemaWithOpaqueFields("webhooks.password"),
		Properties: json.RawMessage(`{"webhooks":[{"password":"a"},{"password":"b"},{"password":"c"}]}`),
	}
	_, diags, err := NewPersistValueTransformer().ApplyToResource(r)
	require.NoError(t, err)
	assert.Empty(t, diags)
}

// patchOpsAfterTransform runs the transformer over a patch document and returns
// the decoded ops, so a test can assert on one op's value directly.
func patchOpsAfterTransform(t *testing.T, schema pkgmodel.Schema, patchDoc string) ([]map[string]any, []Diagnostic) {
	t.Helper()

	out, diags, err := NewPersistValueTransformer().ApplyToResource(&pkgmodel.Resource{
		Schema:        schema,
		PatchDocument: json.RawMessage(patchDoc),
	})
	require.NoError(t, err)

	var ops []map[string]any
	require.NoError(t, json.Unmarshal(out.PatchDocument, &ops))
	return ops, diags
}

func assertHashedEnvelope(t *testing.T, v any, plaintext string) {
	t.Helper()
	env, ok := v.(map[string]any)
	require.True(t, ok, "expected a hashed envelope, got %T", v)
	assert.Equal(t, "Opaque", env["$visibility"])
	assert.Equal(t, "Update", env["$strategy"])
	assert.Equal(t, true, env["$hashed"])
	require.Len(t, env["$value"].(string), 64)
	if plaintext != "" {
		assert.NotEqual(t, plaintext, env["$value"])
	}
}

// A rotation of a nested secret writes the new plaintext into the patch
// document, which is persisted — the same leak at a different at-rest location.
func TestTransformPatchDocument_HashesNestedOpaqueLeaf(t *testing.T) {
	ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("settings.password"),
		`[{"op":"replace","path":"/settings/password","value":"hunter2"}]`)

	require.Len(t, ops, 1)
	assertHashedEnvelope(t, ops[0]["value"], "hunter2")
}

func TestTransformPatchDocument_HashesNestedOpaqueLeafUnderAnIndex(t *testing.T) {
	ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("webhooks.password"),
		`[{"op":"replace","path":"/webhooks/0/password","value":"hunter2"}]`)

	require.Len(t, ops, 1)
	assertHashedEnvelope(t, ops[0]["value"], "hunter2")
}

func TestTransformPatchDocument_LeavesNonOpaqueNestedPathAlone(t *testing.T) {
	ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("settings.password"),
		`[{"op":"replace","path":"/settings/host","value":"smtp.example.com"}]`)

	require.Len(t, ops, 1)
	assert.Equal(t, "smtp.example.com", ops[0]["value"])
}

// Atomic and EntitySet update methods emit ops that replace a whole object or
// array element, so leaf matching alone leaves the nested secret in cleartext.
func TestTransformPatchDocument_HashesNestedSecretInWholeContainerOps(t *testing.T) {
	tests := map[string]struct {
		hint  string
		op    string
		probe func(t *testing.T, value any)
	}{
		"replace a whole object": {
			"settings.password",
			`{"op":"replace","path":"/settings","value":{"host":"h","password":"hunter2"}}`,
			func(t *testing.T, value any) {
				m := value.(map[string]any)
				assert.Equal(t, "h", m["host"])
				assertHashedEnvelope(t, m["password"], "hunter2")
			},
		},
		"add a whole array": {
			"webhooks.password",
			`{"op":"add","path":"/webhooks","value":[{"url":"u","password":"hunter2"}]}`,
			func(t *testing.T, value any) {
				elem := value.([]any)[0].(map[string]any)
				assert.Equal(t, "u", elem["url"])
				assertHashedEnvelope(t, elem["password"], "hunter2")
			},
		},
		"replace a whole array element": {
			"webhooks.password",
			`{"op":"replace","path":"/webhooks/0","value":{"url":"u","password":"hunter2"}}`,
			func(t *testing.T, value any) {
				m := value.(map[string]any)
				assert.Equal(t, "u", m["url"])
				assertHashedEnvelope(t, m["password"], "hunter2")
			},
		},
		"append to an array": {
			"webhooks.password",
			`{"op":"add","path":"/webhooks/-","value":{"password":"hunter2"}}`,
			func(t *testing.T, value any) {
				assertHashedEnvelope(t, value.(map[string]any)["password"], "hunter2")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields(tc.hint), "["+tc.op+"]")
			require.Len(t, ops, 1)
			tc.probe(t, ops[0]["value"])
			assert.NotContains(t, name+"", "hunter2")
		})
	}
}

// A nested inline opaque envelope inside a container op is only reached by
// running the full node handler over the value, not by inspecting its top.
func TestTransformPatchDocument_HashesNestedEnvelopeInsideContainerOp(t *testing.T) {
	ops, _ := patchOpsAfterTransform(t, pkgmodel.Schema{},
		`[{"op":"replace","path":"/settings","value":{"password":{"$value":"hunter2","$visibility":"Opaque","$strategy":"Update"}}}]`)

	require.Len(t, ops, 1)
	pw := ops[0]["value"].(map[string]any)["password"].(map[string]any)
	assert.Equal(t, true, pw["$hashed"])
	assert.Len(t, pw["$value"].(string), 64)
	assert.NotEqual(t, "hunter2", pw["$value"])
}

// The reading that matches keeps the first index and drops the second, so
// neither "elide every index" nor "retain every index" alone would find it.
func TestTransformPatchDocument_MatchesMixedRetainAndElideOfIndices(t *testing.T) {
	ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("accounts.0.webhooks.password"),
		`[{"op":"replace","path":"/accounts/0/webhooks/1/password","value":"hunter2"}]`)

	require.Len(t, ops, 1)
	assertHashedEnvelope(t, ops[0]["value"], "hunter2")
}

func TestTransformPatchDocument_HashesEveryValueKind(t *testing.T) {
	tests := map[string]string{
		"string": `"hunter2"`,
		"number": `12345`,
		"bool":   `true`,
		"map":    `{"user":"admin","password":"hunter2"}`,
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("settings.secret"),
				`[{"op":"replace","path":"/settings/secret","value":`+value+`}]`)

			require.Len(t, ops, 1)
			assertHashedEnvelope(t, ops[0]["value"], "")
			assert.NotContains(t, mustJSON(t, ops[0]["value"]), "hunter2",
				"a map-shaped secret is hashed whole, not left with plaintext keys")
		})
	}
}

// null carries no secret material, so hashing it would fabricate a digest for a
// value that is not there.
func TestTransformPatchDocument_LeavesNullAlone(t *testing.T) {
	ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("settings.secret"),
		`[{"op":"replace","path":"/settings/secret","value":null}]`)

	require.Len(t, ops, 1)
	assert.Nil(t, ops[0]["value"])
}

func TestTransformPatchDocument_IsIdempotent(t *testing.T) {
	schema := schemaWithOpaqueFields("settings.password")
	doc := `[{"op":"replace","path":"/settings/password","value":"hunter2"}]`

	first, _, err := NewPersistValueTransformer().ApplyToResource(&pkgmodel.Resource{
		Schema: schema, PatchDocument: json.RawMessage(doc),
	})
	require.NoError(t, err)
	second, _, err := NewPersistValueTransformer().ApplyToResource(&pkgmodel.Resource{
		Schema: schema, PatchDocument: first.PatchDocument,
	})
	require.NoError(t, err)

	assert.JSONEq(t, string(first.PatchDocument), string(second.PatchDocument))
}

// The rule keys on the presence of a value, not on the op name: a test op
// carries plaintext and is persisted exactly like an add or a replace, while
// copy and move carry only a from.
func TestTransformPatchDocument_KeysOnValuePresenceNotOpName(t *testing.T) {
	ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("settings.password"),
		`[{"op":"test","path":"/settings/password","value":"hunter2"},
		  {"op":"move","from":"/settings/password","path":"/settings/old"},
		  {"op":"copy","from":"/settings/password","path":"/settings/copy"}]`)

	require.Len(t, ops, 3)
	assertHashedEnvelope(t, ops[0]["value"], "hunter2")
	_, moveHasValue := ops[1]["value"]
	_, copyHasValue := ops[2]["value"]
	assert.False(t, moveHasValue)
	assert.False(t, copyHasValue)
}

func TestTransformPatchDocument_HonoursPointerEscaping(t *testing.T) {
	ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("a/b.password"),
		`[{"op":"replace","path":"/a~1b/password","value":"hunter2"}]`)

	require.Len(t, ops, 1)
	assertHashedEnvelope(t, ops[0]["value"], "hunter2")
}

// "/a.b/c" and "/a/b.c" concatenate to the same hint name. Both match — the
// enumerated collision that prefix concatenation accepts by design.
func TestTransformPatchDocument_DottedSegmentCollisionMatchesBothReadings(t *testing.T) {
	for _, pointer := range []string{"/a.b/c", "/a/b.c", "/a.b.c"} {
		t.Run(pointer, func(t *testing.T) {
			ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("a.b.c"),
				`[{"op":"replace","path":"`+pointer+`","value":"hunter2"}]`)

			require.Len(t, ops, 1)
			assertHashedEnvelope(t, ops[0]["value"], "hunter2")
		})
	}
}

func TestTransformPatchDocument_HashesWholeDocumentRootOp(t *testing.T) {
	ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("settings.password"),
		`[{"op":"replace","path":"","value":{"settings":{"password":"hunter2"}}}]`)

	require.Len(t, ops, 1)
	settings := ops[0]["value"].(map[string]any)["settings"].(map[string]any)
	assertHashedEnvelope(t, settings["password"], "hunter2")
}

func TestTransformPatchDocument_HandlesValidEmptySegments(t *testing.T) {
	ops, _ := patchOpsAfterTransform(t, schemaWithOpaqueFields("a..password"),
		`[{"op":"replace","path":"/a//password","value":"hunter2"}]`)

	require.Len(t, ops, 1)
	assertHashedEnvelope(t, ops[0]["value"], "hunter2")
}

// An undecodable pointer is an internal defect. Failing persistence of an
// already-completed command would be worse than over-hashing, so the op is
// processed in the most conservative mode instead of being skipped.
func TestTransformPatchDocument_UndecodablePointerOverHashesRatherThanSkipping(t *testing.T) {
	ops, diags := patchOpsAfterTransform(t, schemaWithOpaqueFields("settings.password"),
		`[{"op":"replace","path":"settings/password","value":{"deep":{"settings":{"password":"hunter2"}}}}]`)

	require.Len(t, ops, 1)
	nested := ops[0]["value"].(map[string]any)["deep"].(map[string]any)["settings"].(map[string]any)
	assertHashedEnvelope(t, nested["password"], "hunter2")

	require.NotEmpty(t, diags)
	assert.Equal(t, DiagnosticError, diags[0].Severity)
}

func TestTransformPatchDocument_UndecodablePointerHashesABareScalarValue(t *testing.T) {
	ops, diags := patchOpsAfterTransform(t, schemaWithOpaqueFields("settings.password"),
		`[{"op":"replace","path":"settings/password","value":"hunter2"}]`)

	require.Len(t, ops, 1)
	assertHashedEnvelope(t, ops[0]["value"], "hunter2")
	require.NotEmpty(t, diags)
}

func TestTransformPatchDocument_ExceedingTheCandidateBoundReportsADiagnostic(t *testing.T) {
	ops, diags := patchOpsAfterTransform(t, schemaWithOpaqueFields("a.b.c.d.e.f.g.password"),
		`[{"op":"replace","path":"/a/0/b/1/c/2/d/3/e/4/f/5/g/password","value":"hunter2"}]`)

	require.Len(t, ops, 1)
	assertHashedEnvelope(t, ops[0]["value"], "hunter2")

	require.NotEmpty(t, diags)
	assert.Equal(t, DiagnosticError, diags[0].Severity)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
