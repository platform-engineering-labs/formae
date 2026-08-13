// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	cascadeSecret = "sup3rs3cret-plaintext"
	cascadeDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// secretHolder is the resource being deleted or replaced: it declares an
// opaque property, held in one of the shapes a value takes over its lifetime.
func secretHolder(ksuid, heldAs string) *pkgmodel.Resource {
	return &pkgmodel.Resource{
		Label: "db",
		Type:  "Test::Database",
		Ksuid: ksuid,
		Schema: pkgmodel.Schema{
			Fields: []string{"Name", "GeneratedPassword"},
			Hints:  map[string]pkgmodel.FieldHint{"GeneratedPassword": {Opaque: true}},
		},
		Properties: json.RawMessage(`{"Name":"db","GeneratedPassword":` + heldAs + `}`),
	}
}

// secretConsumer references the holder's opaque property, carrying the cached
// resolution the consumer last saw at that path.
func secretConsumer(ksuid, cachedValue string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label: "app",
		Type:  "Test::App",
		Properties: json.RawMessage(`{"AppName":"app","DbPassword":{` +
			`"$ref":"formae://` + ksuid + `#/GeneratedPassword","$value":"` + cachedValue + `"}}`),
	}
}

// TestSynthesizeCascadeUpdatePatch_DoesNotEmitSecretMaterial asserts that the
// patch synthesized for a cascade update never carries secret material.
//
// That document is presentation data: it reaches simulate output, the CLI
// renderer, the stored changeset and the logs. It is built from the incoming
// forma, which has not been through the persist-time hashing, so a secret
// written as a literal arrives here in cleartext.
//
// The change must still be named, so the source label survives redaction; only
// the value is withheld.
func TestSynthesizeCascadeUpdatePatch_DoesNotEmitSecretMaterial(t *testing.T) {
	const parentKsuid = "dbksuid00000000000000000000"

	cases := []struct {
		name        string
		heldAs      string
		cachedValue string
	}{
		{
			// A literal in the forma, before any persist-time hashing.
			name:        "source held as a bare value",
			heldAs:      `"` + cascadeSecret + `"`,
			cachedValue: cascadeSecret,
		},
		{
			name:        "source held as an opaque envelope",
			heldAs:      `{"$value":"` + cascadeSecret + `","$visibility":"Opaque"}`,
			cachedValue: cascadeSecret,
		},
		{
			// Already hashed at rest: the digest is not cleartext, but it is
			// still the stored form of a secret and has no business in a plan.
			name:        "source held as a hashed envelope",
			heldAs:      `{"$value":"` + cascadeDigest + `","$visibility":"Opaque","$hashed":true}`,
			cachedValue: cascadeDigest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patchDoc, err := synthesizeCascadeUpdatePatch(
				secretConsumer(parentKsuid, tc.cachedValue),
				map[string]bool{parentKsuid: true},
				nil,
				map[string]string{parentKsuid: "db"},
				map[string]*pkgmodel.Resource{parentKsuid: secretHolder(parentKsuid, tc.heldAs)},
			)
			require.NoError(t, err)
			require.NotEmpty(t, patchDoc, "the cascade op must still be planned")

			patch := string(patchDoc)
			assert.NotContains(t, patch, cascadeSecret,
				"the synthesized patch is presentation data and must not carry the secret: %s", patch)
			assert.NotContains(t, patch, cascadeDigest,
				"the stored digest of a secret must not reach the plan either: %s", patch)

			assert.Contains(t, patch, `"path":"/DbPassword"`,
				"the change must still be named so the operator sees it")
			assert.Contains(t, patch, `"db"`,
				"the source label must survive redaction so the renderer can name the parent")
		})
	}
}

// TestSynthesizeCascadeUpdatePatch_NonSecretValueSurvives asserts the guard
// stays narrow: an ordinary value is still carried, so the operator keeps
// seeing what a cascade is about to set.
func TestSynthesizeCascadeUpdatePatch_NonSecretValueSurvives(t *testing.T) {
	const parentKsuid = "vpcksuid0000000000000000000"

	parent := &pkgmodel.Resource{
		Label:      "vpc",
		Type:       "Test::VPC",
		Ksuid:      parentKsuid,
		Schema:     pkgmodel.Schema{Fields: []string{"VpcId"}},
		Properties: json.RawMessage(`{"VpcId":"vpc-12345"}`),
	}
	consumer := pkgmodel.Resource{
		Label: "subnet",
		Type:  "Test::Subnet",
		Properties: json.RawMessage(`{"VpcRef":{"$ref":"formae://` + parentKsuid +
			`#/VpcId","$value":"vpc-old"}}`),
	}

	patchDoc, err := synthesizeCascadeUpdatePatch(
		consumer,
		map[string]bool{parentKsuid: true},
		nil,
		map[string]string{parentKsuid: "vpc"},
		map[string]*pkgmodel.Resource{parentKsuid: parent},
	)
	require.NoError(t, err)
	assert.Contains(t, string(patchDoc), "vpc-12345",
		"a non-secret cascade value must still be shown")
}
