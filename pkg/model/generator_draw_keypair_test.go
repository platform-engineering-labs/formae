// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package model_test

import (
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A password draw is the single-output arm expressed in the multi-output
// contract: exactly one entry, named "value".
func TestDraw_PasswordReturnsTheValueOutput(t *testing.T) {
	spec := &pkgmodel.PasswordGenerator{
		Label: "pw", Length: 24,
		Uppercase: true, Lowercase: true, Digits: true,
	}
	values, err := pkgmodel.Draw(spec, realSource)
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.Len(t, values["value"], 24)
}

func TestDraw_KeyPairReturnsBothHalves(t *testing.T) {
	spec := &pkgmodel.KeyPairGenerator{Label: "id-key", Bits: 2048}
	values, err := pkgmodel.Draw(spec, realSource)
	require.NoError(t, err)
	require.Len(t, values, 2)
	require.NotEmpty(t, values["privateKey"])
	require.NotEmpty(t, values["publicKey"])
}

// decodeConsumerPrivateKey mirrors the parse sequence the installation
// identity issuer runs on the private half: a PEM block parsed as PKCS#1
// first, then PKCS#8. A drawn key that fails this sequence would apply
// successfully and then fail at mint time, which is the deployment failure
// the fixture exists to prevent.
func decodeConsumerPrivateKey(t *testing.T, raw string) *rsa.PrivateKey {
	t.Helper()
	block, _ := pem.Decode([]byte(raw))
	require.NotNil(t, block, "the private half must be PEM")
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	require.NoError(t, err, "the private half must parse as PKCS#1 or PKCS#8")
	key, ok := parsed.(*rsa.PrivateKey)
	require.True(t, ok, "the private half must be an RSA key")
	return key
}

func TestDraw_KeyPairHalvesParseWithTheConsumersSequence(t *testing.T) {
	spec := &pkgmodel.KeyPairGenerator{Label: "id-key", Bits: 2048}
	values, err := pkgmodel.Draw(spec, realSource)
	require.NoError(t, err)

	private := decodeConsumerPrivateKey(t, values["privateKey"])
	assert.Equal(t, 2048, private.N.BitLen(), "the modulus must carry the declared bits")

	// The public half must parse as PKIX, which is the only encoding the
	// issuer's public-key decoder accepts.
	block, _ := pem.Decode([]byte(values["publicKey"]))
	require.NotNil(t, block, "the public half must be PEM")
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err, "the public half must parse as PKIX")
	public, ok := parsed.(*rsa.PublicKey)
	require.True(t, ok, "the public half must be an RSA key")

	assert.Equal(t, 0, private.PublicKey.N.Cmp(public.N),
		"the two halves must be one pair, not two draws")
}

// Entropy flows from the supplied source, never from an ambient reader: a
// failing source must fail the draw, which is what keeps fault injection and
// deterministic tests possible for this arm too.
func TestDraw_KeyPairEntropyComesFromTheSource(t *testing.T) {
	broken := func(b []byte) (int, error) { return 0, errors.New("entropy exhausted") }
	_, err := pkgmodel.Draw(&pkgmodel.KeyPairGenerator{Label: "id-key", Bits: 2048}, broken)
	require.Error(t, err)
	assert.ErrorContains(t, err, "entropy exhausted")
}

// The schema constrains bits to 2048/3072/4096; a spec that arrives outside
// that set did not come through the schema and is refused rather than drawn.
func TestDraw_KeyPairRefusesUnknownBits(t *testing.T) {
	_, err := pkgmodel.Draw(&pkgmodel.KeyPairGenerator{Label: "id-key", Bits: 1024}, realSource)
	require.Error(t, err)
}

func TestGeneratorOutputs_PerKind(t *testing.T) {
	assert.Equal(t, []string{"value"},
		pkgmodel.GeneratorOutputNames(&pkgmodel.PasswordGenerator{}))
	assert.Equal(t, []string{"privateKey", "publicKey"},
		pkgmodel.GeneratorOutputNames(&pkgmodel.KeyPairGenerator{}))
}

// The keypair generator round-trips through the discriminated JSON the
// datastore persists, exactly as the password arm does.
func TestKeyPairGenerator_MarshalRoundTrip(t *testing.T) {
	g := &pkgmodel.KeyPairGenerator{
		Label: "id-key", Stack: "secrets", Bits: 3072,
		Rotation: &pkgmodel.RotationSpec{EverySeconds: 86400},
	}
	raw, err := json.Marshal(g)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"Type":"keypair"`)

	parsed, err := pkgmodel.ParseGenerator(raw)
	require.NoError(t, err)
	back, ok := parsed.(*pkgmodel.KeyPairGenerator)
	require.True(t, ok)
	assert.Equal(t, 3072, back.Bits)
	assert.Equal(t, "secrets", back.GetStack())
	require.NotNil(t, back.GetRotation())
	assert.Equal(t, 86400, back.GetRotation().EverySeconds)
}

// Two keypair draws must differ: the source is consulted, not a fixture.
func TestDraw_KeyPairDrawsAreIndependent(t *testing.T) {
	spec := &pkgmodel.KeyPairGenerator{Label: "id-key", Bits: 2048}
	a, err := pkgmodel.Draw(spec, func(b []byte) (int, error) { return cryptorand.Read(b) })
	require.NoError(t, err)
	b, err := pkgmodel.Draw(spec, func(b []byte) (int, error) { return cryptorand.Read(b) })
	require.NoError(t, err)
	assert.NotEqual(t, a["privateKey"], b["privateKey"])
}
