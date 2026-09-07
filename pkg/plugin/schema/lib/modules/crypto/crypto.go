// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"strconv"

	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/extension"
	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/registry"
)

var Crypto = crypto{}

type crypto struct{}

var _ extension.Library = crypto{}

func init() {
	registry.Register("crypto", func() extension.Library {
		return Crypto
	})
}

func (crypto) Invoke(uri *url.URL) *extension.Result {
	call, args := extension.NameArgsFrom(uri)

	switch call {
	case "keyPair":
		return keyPair(args)
	default:
		return &extension.Result{
			Error: fmt.Sprintf("unknown function name: %s", call),
		}
	}
}

func keyPair(args url.Values) *extension.Result {
	encoding := args.Get("encoding")
	if encoding == "" {
		return &extension.Result{
			Error: "failed to decode encoding",
		}
	}

	bits := args.Get("bits")
	if bits == "" {
		return &extension.Result{
			Error: "failed to decode bits",
		}
	}

	bitsVal, err := strconv.Atoi(bits)
	if err != nil {
		return &extension.Result{
			Error: "failed to decode bits",
		}
	}

	if bitsVal < 2048 {
		return &extension.Result{
			Error: "keypair bits must be at least 2048",
		}
	}

	params := args.Get("params")
	if params == "" {
		return &extension.Result{
			Error: "failed to decode params",
		}
	}

	var private []byte
	var public []byte

	switch encoding {
	case "pkcs1", "pkcs8":
		pk, err := rsa.GenerateKey(rand.Reader, bitsVal)
		if err != nil {
			return &extension.Result{
				Error: fmt.Errorf("failed to generate signing key: %w", err).Error(),
			}
		}

		if encoding == "pkcs1" {
			private = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(pk)})
		} else {
			xp, err := x509.MarshalPKCS8PrivateKey(pk)
			if err != nil {
				return &extension.Result{
					Error: fmt.Errorf("failed to marshal PKCS8 private key: %w", err).Error(),
				}
			}

			private = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: xp})
		}

		xpu, err := x509.MarshalPKIXPublicKey(&pk.PublicKey)
		if err != nil {
			return &extension.Result{
				Error: fmt.Errorf("failed to marshal PKIX public key: %w", err).Error(),
			}
		}

		public = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: xpu})
	case "sec1":
		var curve elliptic.Curve
		switch params {
		default:
		case "256":
			curve = elliptic.P256()
		case "384":
			curve = elliptic.P384()
		case "521":
			curve = elliptic.P521()
		}

		pk, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			return &extension.Result{
				Error: fmt.Errorf("failed to generate private key: %w", err).Error(),
			}
		}

		xp, err := x509.MarshalECPrivateKey(pk)
		if err != nil {
			return &extension.Result{
				Error: fmt.Errorf("failed to marshal SEC1 private key: %w", err).Error(),
			}
		}

		private = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: xp})

		xpu, err := x509.MarshalPKIXPublicKey(&pk.PublicKey)
		if err != nil {
			return &extension.Result{
				Error: fmt.Errorf("failed to marshal PKIX public key: %w", err).Error(),
			}
		}

		public = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: xpu})
	default:
		return &extension.Result{
			Error: fmt.Sprintf("unsupported encoding: %s", encoding),
		}
	}

	body, err := json.Marshal(map[string]any{
		"private": string(private),
		"public":  string(public),
	})
	if err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("failed to serialize result: private %s, public %s", string(private), string(public)),
		}
	}

	return &extension.Result{
		Body: body,
	}
}
