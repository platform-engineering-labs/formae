// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Command oidc-credential-stub is an oidc-credential broker for the e2e
// suite. It mints a deterministic, unsigned token derived from the requested
// audience: nothing in the hermetic pipeline verifies a signature, and what
// the tests assert is that the token the broker returns reaches the resource
// plugin that asked for it.
package main

import (
	"context"
	"log"
	"time"

	"github.com/platform-engineering-labs/formae/pkg/credential"
)

// tokenPrefix marks a token as coming from this stub. Tests assert on
// prefix + audience, so the value must stay in step with them.
const tokenPrefix = "e2e-stub-jwt."

type stub struct{}

func (s *stub) IdentityToken(_ context.Context, req *credential.OidcIdentityTokenRequest) (*credential.OidcIdentityTokenResult, error) {
	return &credential.OidcIdentityTokenResult{
		Token:     tokenPrefix + req.Audience,
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

func main() {
	if err := credential.Run(&stub{}); err != nil {
		log.Fatal(err)
	}
}
