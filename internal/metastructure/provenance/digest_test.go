// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package provenance

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A scalar secret's canonical digest must be comparable with the digest the
// source row already stores at rest, or every undeclared-source comparison
// would degrade to unknown.
func TestDigest_ScalarStringMatchesAtRestDomain(t *testing.T) {
	legacy := pkgmodel.ComputeValueHash("hunter2")
	assert.Equal(t, FromStored(legacy), DigestOfString("hunter2"))
	assert.Equal(t, FromStored(legacy), DigestOfJSON(`"hunter2"`))
}

// A structured value's canonical digest must equal the digest the real
// persistence path produces for the same value, including float-decoded
// numbers: the canonical domain IS the at-rest domain.
func TestDigest_StructuredValueMatchesPersistTransformer(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"SecretDoc"},
		Hints:  map[string]pkgmodel.FieldHint{"SecretDoc": {Opaque: true}},
	}
	raw := `{"username":"app","threshold":1e3,"big":1234567890123,"nested":{"b":"x","a":1}}`
	res := &pkgmodel.Resource{
		Type:       "Test::Secret",
		Schema:     schema,
		Properties: []byte(`{"SecretDoc":` + raw + `}`),
	}
	hashed, _, err := transformations.NewPersistValueTransformer().ApplyToResource(res)
	require.NoError(t, err)
	stored := gjson.GetBytes(hashed.Properties, "SecretDoc.$value").String()
	require.NotEmpty(t, stored)
	require.True(t, gjson.GetBytes(hashed.Properties, "SecretDoc.$hashed").Bool())

	assert.Equal(t, FromStored(stored), DigestOfJSON(raw))
}

// fmt rendering of nested maps is deterministic (recursively sorted keys), so
// two key orders of the same document digest identically.
func TestDigest_StructuredValuesAreOrderInsensitive(t *testing.T) {
	assert.Equal(t,
		DigestOfJSON(`{"a":1,"b":"x","c":{"z":1,"y":2}}`),
		DigestOfJSON(`{ "c": {"y":2,"z":1}, "b": "x", "a": 1 }`))
}

// A string that happens to look like JSON must digest as the string it is,
// not as the document it resembles: type is decided by the caller, never
// guessed from content.
func TestDigest_TypeKnownAPIDoesNotGuess(t *testing.T) {
	looksLikeJSON := `{"a":1}`
	assert.NotEqual(t, DigestOfString(looksLikeJSON), DigestOfJSON(looksLikeJSON))
}

// The empty value digests to a real, distinct digest: an unchanged empty
// secret must classify stable, never as permanent movement.
func TestDigest_EmptyValueIsARealDigest(t *testing.T) {
	assert.True(t, Valid(DigestOfString("")))
	assert.NotEqual(t, DigestOfString(""), DigestOfString("x"))
}

func TestValid(t *testing.T) {
	assert.True(t, Valid(DigestOfString("v")))
	assert.False(t, Valid(""))
	assert.False(t, Valid(pkgmodel.ComputeValueHash("v")), "bare legacy digest is not current-domain")
	assert.False(t, Valid("v1:"), "empty payload")
	assert.False(t, Valid("v1:zz"), "non-hex")
	assert.False(t, Valid("v1:"+pkgmodel.ComputeValueHash("v")+"ff"), "wrong length")
	upper := "v1:ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
	assert.False(t, Valid(upper), "uppercase hex rejected")
}

func TestFromStored(t *testing.T) {
	good := pkgmodel.ComputeValueHash("v")
	assert.Equal(t, "v1:"+good, FromStored(good))
	assert.Empty(t, FromStored(""))
	assert.Empty(t, FromStored("not-hex"))
	assert.Empty(t, FromStored(good+"00"), "wrong length")
}

// UnwrapEffectiveValue: a value envelope yields its $value; reference
// envelopes and plain nodes yield themselves.
func TestUnwrapEffectiveValue(t *testing.T) {
	env := gjson.Parse(`{"$value":"hunter2","$visibility":"Opaque","$strategy":"Update"}`)
	assert.Equal(t, "hunter2", UnwrapEffectiveValue(env).String())

	hashedEnv := gjson.Parse(`{"$value":"abc","$hashed":true}`)
	assert.Equal(t, "abc", UnwrapEffectiveValue(hashedEnv).String())

	ref := gjson.Parse(`{"$ref":"formae://k#/P","$value":"cached"}`)
	assert.Equal(t, ref.Raw, UnwrapEffectiveValue(ref).Raw, "reference envelopes are not value envelopes")

	res := gjson.Parse(`{"$res":true,"$label":"l","$value":"cached"}`)
	assert.Equal(t, res.Raw, UnwrapEffectiveValue(res).Raw)

	plain := gjson.Parse(`{"user":"u"}`)
	assert.Equal(t, plain.Raw, UnwrapEffectiveValue(plain).Raw)

	scalar := gjson.Parse(`"just-a-string"`)
	assert.Equal(t, "just-a-string", UnwrapEffectiveValue(scalar).String())
}

// The pass-4 cross-site pin: a declared opaque envelope's planning-side
// digest (unwrap, then digest) equals the persisted source digest of the same
// value.
func TestDigest_DeclaredEnvelopeUnwrapMatchesAtRest(t *testing.T) {
	env := gjson.Parse(`{"$value":"hunter2","$visibility":"Opaque","$strategy":"Update"}`)
	unwrapped := UnwrapEffectiveValue(env)
	var got string
	if unwrapped.Type == gjson.String {
		got = DigestOfString(unwrapped.String())
	} else {
		got = DigestOfJSON(unwrapped.Raw)
	}
	assert.Equal(t, FromStored(pkgmodel.ComputeValueHash("hunter2")), got)
}
