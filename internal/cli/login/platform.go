// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
)

// DefaultCloudURL and DefaultCloudIssuer are the built-in control-plane
// origin and issuer used when neither is overridden. The canonical
// definitions (and the reasoning behind the values) live in cloudapi.
const (
	DefaultCloudURL    = cloudapi.DefaultCloudURL
	DefaultCloudIssuer = cloudapi.DefaultCloudIssuer
)

// platform is a control-plane origin and the issuer that must have signed a
// profile's auth block before the CLI will sync against that origin. The two
// are resolved together by resolvePlatform so one can never be swapped in
// without the other.
type platform struct {
	Origin string
	Issuer string
}

// resolvePlatform resolves the control-plane origin and issuer as a pair,
// delegating to cloudapi.ResolvePlatform (flag beats env var beats built-in
// default; a half-set override pair is refused).
func resolvePlatform(cloudFlag, issuerFlag string) (platform, error) {
	origin, issuer, err := cloudapi.ResolvePlatform(cloudFlag, issuerFlag)
	if err != nil {
		return platform{}, err
	}
	return platform{Origin: origin, Issuer: issuer}, nil
}

// canonicalOrigin parses and canonicalises a control-plane origin. The rule
// itself lives in cloudapi.CanonicalOrigin; login keeps this name because the
// gate and the ledger apply it to every origin they touch.
func canonicalOrigin(raw string) (string, error) {
	return cloudapi.CanonicalOrigin(raw)
}
