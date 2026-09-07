// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cloudapi

import (
	"errors"
	"fmt"
	"os"
)

// DefaultCloudURL and DefaultCloudIssuer are the built-in control-plane
// origin and issuer used when neither is overridden.
//
// The origin is the console host and not the apex, because the apex serves the
// marketing site and no API: measured, https://formae.ai/api/v1/me/installations
// answers 404 with a stock HTML error page, while the console answers 401 with
// {"error":{"code":"unauthorized"}} — a real API asking for the bearer. With the
// apex here, enumerating a caller's grants failed for everyone and no hosted
// profile could ever be written.
//
// Changing it was safe precisely because it never worked. This value is also
// recorded in the managed-profile ledger as the control plane an entry belongs to,
// and entries are matched on it exactly, so moving it would normally orphan every
// profile a previous sign-in had written — but the enumeration 404s before
// anything is written, so no entry against the apex exists to orphan. That window
// closes the moment one sign-in succeeds against a released build.
const (
	DefaultCloudURL    = "https://console.formae.ai"
	DefaultCloudIssuer = "https://auth.formae.ai"
)

// ErrPlatformHalfSet is wrapped into the error ResolvePlatform returns when
// exactly one of the origin/issuer overrides is set. A request built from a
// mismatched pair — a custom control plane paired with the default issuer, or
// the reverse — is exactly the state the login sync gate exists to catch, so
// it must not be constructible.
var ErrPlatformHalfSet = errors.New("the cloud URL and cloud issuer overrides must be set together, or neither")

// ResolvePlatform resolves the control-plane origin and the issuer that must
// have signed a profile's auth block, as a pair. Each half is resolved
// independently with flag beats env var beats built-in default, then both
// halves are canonicalised through CanonicalOrigin. The two halves may come
// from different sources — a flag for one and an env var for the other is a
// complete pair — but exactly one of them being overridden while the other
// is left at its default is refused rather than silently completed, and an
// override that fails to canonicalise is an error rather than a fallback to
// the default.
//
// An env var present but set to the empty string counts as set. os.Getenv
// cannot tell absent from empty, so resolveHalf uses os.LookupEnv instead:
// treating a present override as unset would let it be silently ignored,
// which is the one thing this function must never do to an override the
// caller (or its environment) actually specified.
func ResolvePlatform(cloudFlag, issuerFlag string) (origin, issuer string, err error) {
	cloudRaw, cloudSet := resolveHalf(cloudFlag, "FORMAE_CLOUD_URL")
	issuerRaw, issuerSet := resolveHalf(issuerFlag, "FORMAE_CLOUD_ISSUER")

	switch {
	case cloudSet && !issuerSet:
		return "", "", fmt.Errorf(
			"--cloud (or FORMAE_CLOUD_URL) is set without --cloud-issuer (or FORMAE_CLOUD_ISSUER): %w", ErrPlatformHalfSet)
	case issuerSet && !cloudSet:
		return "", "", fmt.Errorf(
			"--cloud-issuer (or FORMAE_CLOUD_ISSUER) is set without --cloud (or FORMAE_CLOUD_URL): %w", ErrPlatformHalfSet)
	case !cloudSet && !issuerSet:
		cloudRaw = DefaultCloudURL
		issuerRaw = DefaultCloudIssuer
	}

	origin, err = CanonicalOrigin(cloudRaw)
	if err != nil {
		return "", "", fmt.Errorf("cloud URL: %w", err)
	}
	issuer, err = CanonicalOrigin(issuerRaw)
	if err != nil {
		return "", "", fmt.Errorf("cloud issuer: %w", err)
	}
	return origin, issuer, nil
}

// resolveHalf returns the flag-vs-env-vs-default candidate for one half of
// the platform pair, and whether it was set at all (by flag or by env var).
// The default is never returned here; the caller substitutes it only once it
// has confirmed the other half is unset too.
func resolveHalf(flagVal, envName string) (candidate string, set bool) {
	if flagVal != "" {
		return flagVal, true
	}
	if envVal, ok := os.LookupEnv(envName); ok {
		return envVal, true
	}
	return "", false
}
