// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Command oidc-credential-stub is an oidc-credential broker for the e2e
// suite. It mints real RS256 identity tokens for a standing static OIDC
// issuer, signing them with an RSA key it fetches from Secrets Manager at
// startup, so the token it hands the resource plugin is one AWS STS will
// verify against the issuer's published JWKS and exchange for credentials.
package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/golang-jwt/jwt/v5"
	"github.com/platform-engineering-labs/formae/pkg/credential"
)

const (
	// issuer is the standing static OIDC issuer this broker mints for. It is
	// the `iss` claim, and it is what the IAM OIDC provider and the assumed
	// role's trust policy name, so it must match them character for
	// character, with no trailing slash.
	issuer = "https://e2e-oidc-issuer-942849037363.s3.us-west-2.amazonaws.com"

	// keyID names the signing key in the issuer's published JWKS. STS reads
	// it from the token header to pick the public key it verifies with.
	keyID = "e2e-oidc-key-1"

	// subjectEnv names the environment variable carrying the `sub` claim.
	// The subject is produced by the control plane, travels through `formae
	// connect` into the trust it provisions, and has to be the same string
	// here or nothing the broker mints is accepted. The test owns both ends,
	// so it supplies it rather than this file pinning a copy.
	subjectEnv = "E2E_OIDC_SUBJECT"

	// signingKeySecretID names the Secrets Manager secret whose SecretString
	// is the PEM-encoded RSA private key matching keyID in the JWKS.
	signingKeySecretID = "e2e-oidc-signing-key"

	// awsRegion is where the issuer bucket and the signing-key secret live.
	awsRegion = "us-west-2"

	// tokenLifetime is how long a minted token stays valid. Long enough for
	// an e2e run, short enough to be a credible identity token.
	tokenLifetime = time.Hour
)

type stub struct {
	signingKey *rsa.PrivateKey
	subject    string
}

// Configure fetches the signing key before the broker starts serving. The
// SDK always runs this step, which makes it the place to fail: a broker that
// cannot sign must not come up and hand out garbage tokens that render as an
// opaque STS rejection later.
func (s *stub) Configure(_ json.RawMessage) error {
	ctx := context.Background()

	// Read before the key: a broker with no subject can sign perfectly well
	// and still mint a token nothing will accept, which surfaces as an opaque
	// rejection at the far end rather than as the missing configuration it is.
	s.subject = os.Getenv(subjectEnv)
	if s.subject == "" {
		return fmt.Errorf("%s is not set, so the broker has no subject to mint for", subjectEnv)
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(awsRegion))
	if err != nil {
		return fmt.Errorf("loading aws config: %w", err)
	}

	secret, err := secretsmanager.NewFromConfig(cfg).GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(signingKeySecretID),
	})
	if err != nil {
		return fmt.Errorf("fetching signing key %q: %w", signingKeySecretID, err)
	}
	if secret.SecretString == nil {
		return fmt.Errorf("signing key %q carries no SecretString", signingKeySecretID)
	}

	key, err := parseRSAPrivateKey(*secret.SecretString)
	if err != nil {
		return fmt.Errorf("parsing signing key %q: %w", signingKeySecretID, err)
	}

	s.signingKey = key
	return nil
}

// parseRSAPrivateKey decodes a PEM-encoded RSA private key. The standing
// secret holds PKCS#8; PKCS#1 is accepted too so a key rotated with a
// different openssl invocation still loads.
func parseRSAPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("no PEM block found")
	}

	parsed, pkcs8Err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if pkcs8Err == nil {
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS#8 key is %T, want *rsa.PrivateKey", parsed)
		}
		return key, nil
	}

	key, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if pkcs1Err != nil {
		return nil, fmt.Errorf("not PKCS#8 (%v) and not PKCS#1 (%w)", pkcs8Err, pkcs1Err)
	}
	return key, nil
}

// IdentityToken mints an RS256 JWT for the requested audience, signed with
// the key Configure loaded.
func (s *stub) IdentityToken(_ context.Context, req *credential.OidcIdentityTokenRequest) (*credential.OidcIdentityTokenResult, error) {
	if s.signingKey == nil {
		return nil, errors.New("no signing key loaded")
	}

	now := time.Now()
	expiresAt := now.Add(tokenLifetime)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": issuer,
		"sub": s.subject,
		"aud": req.Audience,
		"iat": jwt.NewNumericDate(now),
		"exp": jwt.NewNumericDate(expiresAt),
	})
	token.Header["kid"] = keyID

	signed, err := token.SignedString(s.signingKey)
	if err != nil {
		return nil, fmt.Errorf("signing identity token: %w", err)
	}

	return &credential.OidcIdentityTokenResult{
		Token:     signed,
		ExpiresAt: expiresAt,
	}, nil
}

func main() {
	if err := credential.Run(&stub{}); err != nil {
		log.Fatal(err)
	}
}
