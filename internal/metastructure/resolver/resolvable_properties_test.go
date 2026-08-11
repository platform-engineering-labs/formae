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

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const sourceKsuid = "2abcdefghijklmnopqrstuvwxyz"

// consumerOf builds a resource whose Password property references property on
// the source resource.
func consumerOf(property string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:      "consumer",
		Type:       "Test::Consumer",
		Stack:      "default",
		Properties: json.RawMessage(`{"Password":{"$ref":"formae://` + sourceKsuid + `#/` + property + `"}}`),
	}
}

// stacksWith puts source in the shape LoadResolvablePropertiesFromStacks expects.
func stacksWith(source *pkgmodel.Resource) map[string][]*pkgmodel.Resource {
	return map[string][]*pkgmodel.Resource{"default": {source}}
}

// TestLoadResolvableProperties_SkipsHashedValue asserts that a value stored
// hashed at rest is not offered as a resolution: the digest is not what the
// source holds, so the reference must be left for execution-time resolution
// against the live source instead.
func TestLoadResolvableProperties_SkipsHashedValue(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "the-secret",
		Type:  "Test::Secret",
		Stack: "default",
		Properties: json.RawMessage(`{"SecretString":{` +
			`"$value":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",` +
			`"$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("SecretString"), stacksWith(source))
	require.NoError(t, err)

	_, found := props.Get(sourceKsuid, "SecretString")
	assert.False(t, found, "a hashed-at-rest value must not be offered as a resolution")
}

// TestLoadResolvableProperties_SkipsHashedReadOnlyValue is the same rule for a
// value that lives in ReadOnlyProperties, which takes precedence over
// Properties in the lookup.
func TestLoadResolvableProperties_SkipsHashedReadOnlyValue(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "the-secret",
		Type:  "Test::Secret",
		Stack: "default",
		ReadOnlyProperties: json.RawMessage(`{"GeneratedToken":{` +
			`"$value":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",` +
			`"$visibility":"Opaque","$hashed":true}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("GeneratedToken"), stacksWith(source))
	require.NoError(t, err)

	_, found := props.Get(sourceKsuid, "GeneratedToken")
	assert.False(t, found, "a hashed-at-rest read-only value must not be offered as a resolution")
}

// TestLoadResolvableProperties_UsesOpaqueValueThatIsNotHashed asserts the rule
// keys on the value being a digest, not on the field being a credential: an
// opaque value still holding its plaintext resolves normally.
func TestLoadResolvableProperties_UsesOpaqueValueThatIsNotHashed(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid:      sourceKsuid,
		Label:      "the-secret",
		Type:       "Test::Secret",
		Stack:      "default",
		Properties: json.RawMessage(`{"SecretString":{"$value":"live-value","$visibility":"Opaque"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("SecretString"), stacksWith(source))
	require.NoError(t, err)

	value, found := props.Get(sourceKsuid, "SecretString")
	require.True(t, found, "an opaque value that is not a digest must still resolve")
	assert.Equal(t, "live-value", value)
}

// TestLoadResolvableProperties_SkipsStructureHoldingAHashedValue asserts that a
// digest is refused wherever it sits: a reference naming a whole structure must
// not resolve to text with a hashed value buried inside it.
func TestLoadResolvableProperties_SkipsStructureHoldingAHashedValue(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "the-config",
		Type:  "Test::Config",
		Stack: "default",
		Properties: json.RawMessage(`{"Connection":{"Host":"db.internal","Password":{` +
			`"$value":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",` +
			`"$visibility":"Opaque","$hashed":true}}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("Connection"), stacksWith(source))
	require.NoError(t, err)

	_, found := props.Get(sourceKsuid, "Connection")
	assert.False(t, found, "a structure holding a hashed value must not be offered as a resolution")
}

// TestLoadResolvableProperties_SkipsValuelessReferenceEnvelope asserts that a
// source property which is itself an unresolved reference — what
// reference-don't-store leaves at rest — is not offered: its raw envelope text
// is not a value.
func TestLoadResolvableProperties_SkipsValuelessReferenceEnvelope(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid:      sourceKsuid,
		Label:      "the-consumer",
		Type:       "Test::Consumer",
		Stack:      "default",
		Properties: json.RawMessage(`{"Token":{"$ref":"formae://someotherksuid#/Value","$visibility":"Opaque"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("Token"), stacksWith(source))
	require.NoError(t, err)

	_, found := props.Get(sourceKsuid, "Token")
	assert.False(t, found, "a reference envelope with no value must not be offered as a resolution")
}

// TestLoadResolvableProperties_UsesResolvedReferenceEnvelope is the chained
// reference: a source property that is itself a reference which has already
// been resolved still resolves, through the value it carries.
func TestLoadResolvableProperties_UsesResolvedReferenceEnvelope(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid:      sourceKsuid,
		Label:      "the-subnet",
		Type:       "Test::Subnet",
		Stack:      "default",
		Properties: json.RawMessage(`{"VpcId":{"$ref":"formae://someotherksuid#/VpcId","$value":"vpc-123"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("VpcId"), stacksWith(source))
	require.NoError(t, err)

	value, found := props.Get(sourceKsuid, "VpcId")
	require.True(t, found, "a resolved reference must still resolve through its value")
	assert.Equal(t, "vpc-123", value)
}

// TestLoadResolvableProperties_UsesPlainValue is the ordinary cross-resource
// reference: a plain persisted property resolves at planning time.
func TestLoadResolvableProperties_UsesPlainValue(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid:              sourceKsuid,
		Label:              "the-vpc",
		Type:               "Test::VPC",
		Stack:              "default",
		ReadOnlyProperties: json.RawMessage(`{"VpcId":"vpc-123"}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("VpcId"), stacksWith(source))
	require.NoError(t, err)

	value, found := props.Get(sourceKsuid, "VpcId")
	require.True(t, found)
	assert.Equal(t, "vpc-123", value)
}
