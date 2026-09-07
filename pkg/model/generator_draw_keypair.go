// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// sourceReader adapts a ByteSource to the io.Reader rsa.GenerateKey wants.
//
// Honesty about what this buys: current Go's rsa.GenerateKey draws its key
// material from the platform CSPRNG regardless of the reader passed to it
// (verified here by test: an erroring reader still yielded a key), so unlike
// the password arm this one cannot promise that the drawn key is a function
// of src. What the Draw contract still guarantees, via the explicit probe in
// drawKeyPair, is that a failing source fails the draw, so fault injection
// keeps working and no arm quietly ignores its entropy argument.
type sourceReader struct{ src ByteSource }

func (r sourceReader) Read(b []byte) (int, error) { return r.src(b) }

// drawKeyPair generates one RSA key pair and encodes the private half as
// PKCS#8 PEM and the public half as PKIX PEM: the encodings the installation
// identity issuer's decoders accept (its private-key decoder also takes
// PKCS#1; its public-key decoder takes PKIX only). The two halves land in
// different destinations with different readers, which is the reason a
// keypair is one draw with two named outputs rather than two generators.
func drawKeyPair(g *KeyPairGenerator, src ByteSource) (map[string]string, error) {
	switch g.Bits {
	case 2048, 3072, 4096:
	default:
		// The schema constrains bits to this set, so a spec outside it did
		// not come through the schema.
		return nil, fmt.Errorf("draw: %q has unsupported key size %d (want 2048, 3072 or 4096)", g.Label, g.Bits)
	}

	// Probe the source before generating: rsa.GenerateKey ignores its reader
	// on current Go (see sourceReader), so without this an exhausted or
	// broken entropy source would go unnoticed here alone among the arms.
	probe := make([]byte, 1)
	if n, err := src(probe); err != nil || n != len(probe) {
		if err == nil {
			err = fmt.Errorf("got %d bytes, want %d", n, len(probe))
		}
		return nil, fmt.Errorf("draw: %q entropy source failed: %w", g.Label, err)
	}

	key, err := rsa.GenerateKey(sourceReader{src}, g.Bits)
	if err != nil {
		return nil, fmt.Errorf("draw: %q key generation failed: %w", g.Label, err)
	}

	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("draw: %q private-half encoding failed: %w", g.Label, err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("draw: %q public-half encoding failed: %w", g.Label, err)
	}

	return map[string]string{
		"privateKey": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})),
		"publicKey":  string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})),
	}, nil
}
