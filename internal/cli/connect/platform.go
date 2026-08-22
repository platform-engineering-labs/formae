// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"errors"
	"fmt"
	"os"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
)

// ProductionIssuer is the root outbound issuer the trust artifacts pin — the
// same literal the published templates carry. It names the token issuer for
// agents, a different trust domain from the login issuer (auth.formae.ai),
// which is why the override pair below is distinct from FORMAE_CLOUD_*.
const ProductionIssuer = "https://oidc.cloud.formae.ai"

// defaultTemplateBase is where the two published templates live. One canonical
// region: stack identity is per account+region while the provider is global.
const defaultTemplateBase = "https://formae-connect-templates.s3.us-east-1.amazonaws.com"

var errConnectPairHalfSet = errors.New(
	"FORMAE_CONNECT_ISSUER and FORMAE_CONNECT_TEMPLATE_BASE must be set together, or neither")

// connectPlatform is the AWS-side trust pair: the issuer the artifacts pin
// and the base the pinned templates are fetched from. Resolved together so
// one can never be swapped in without the other.
type connectPlatform struct {
	Issuer       string // canonical https origin
	TemplateBase string
}

// resolveConnectPlatform resolves the pair, mirroring the login platform's
// half-set refusal exactly (LookupEnv so empty-but-present counts as set).
func resolveConnectPlatform() (connectPlatform, error) {
	issuerRaw, issuerSet := os.LookupEnv("FORMAE_CONNECT_ISSUER")
	baseRaw, baseSet := os.LookupEnv("FORMAE_CONNECT_TEMPLATE_BASE")
	switch {
	case issuerSet && !baseSet, baseSet && !issuerSet:
		return connectPlatform{}, errConnectPairHalfSet
	case !issuerSet:
		issuerRaw, baseRaw = ProductionIssuer, defaultTemplateBase
	}
	issuer, err := cloudapi.CanonicalOrigin(issuerRaw)
	if err != nil {
		return connectPlatform{}, fmt.Errorf("connect issuer: %w", err)
	}
	base, err := cloudapi.CanonicalOrigin(baseRaw)
	if err != nil {
		return connectPlatform{}, fmt.Errorf("connect template base: %w", err)
	}
	return connectPlatform{Issuer: issuer, TemplateBase: base}, nil
}
