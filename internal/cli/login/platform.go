// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
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

// platform is a control-plane origin and the issuer that must have signed a
// profile's auth block before the CLI will sync against that origin. The two
// are resolved together by resolvePlatform so one can never be swapped in
// without the other.
type platform struct {
	Origin string
	Issuer string
}

// errPlatformHalfSet is wrapped into the error resolvePlatform returns when
// exactly one of the origin/issuer overrides is set. A request built from a
// mismatched pair — a custom control plane paired with the default issuer, or
// the reverse — is exactly the state the sync gate exists to catch, so it
// must not be constructible.
var errPlatformHalfSet = errors.New("the cloud URL and cloud issuer overrides must be set together, or neither")

// resolvePlatform resolves the control-plane origin and issuer as a pair.
// Each half is resolved independently with flag beats env var beats built-in
// default (mirroring resolveHubURL in internal/cli/plugin/init.go), then both
// halves are canonicalised through canonicalOrigin. The two halves may come
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
func resolvePlatform(cloudFlag, issuerFlag string) (platform, error) {
	cloudRaw, cloudSet := resolveHalf(cloudFlag, "FORMAE_CLOUD_URL")
	issuerRaw, issuerSet := resolveHalf(issuerFlag, "FORMAE_CLOUD_ISSUER")

	switch {
	case cloudSet && !issuerSet:
		return platform{}, fmt.Errorf(
			"--cloud (or FORMAE_CLOUD_URL) is set without --cloud-issuer (or FORMAE_CLOUD_ISSUER): %w", errPlatformHalfSet)
	case issuerSet && !cloudSet:
		return platform{}, fmt.Errorf(
			"--cloud-issuer (or FORMAE_CLOUD_ISSUER) is set without --cloud (or FORMAE_CLOUD_URL): %w", errPlatformHalfSet)
	case !cloudSet && !issuerSet:
		cloudRaw = DefaultCloudURL
		issuerRaw = DefaultCloudIssuer
	}

	origin, err := canonicalOrigin(cloudRaw)
	if err != nil {
		return platform{}, fmt.Errorf("cloud URL: %w", err)
	}
	issuer, err := canonicalOrigin(issuerRaw)
	if err != nil {
		return platform{}, fmt.Errorf("cloud issuer: %w", err)
	}
	return platform{Origin: origin, Issuer: issuer}, nil
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

// loopbackHosts are the hosts a plain http origin is accepted for. A bearer
// token sent over http can be read by anything on the path, so http is
// confined to the hosts where there is no path off the machine. The set is
// the exact three literals rather than a subnet test, so nothing that merely
// resolves to a loopback address widens it.
var loopbackHosts = map[string]bool{
	"localhost": true,
	"127.0.0.1": true,
	"::1":       true,
}

// canonicalOrigin parses and canonicalises a control-plane origin into a
// scheme and host with no trailing slash, so request URLs can be built by
// joining a path onto it rather than by concatenating strings, and so two
// spellings of the same origin compare equal as strings. The host and scheme
// are lowercased and a redundant :443 is dropped.
//
// An origin says where formae sends a bearer token, and in the ledger it says
// which control plane a record belongs to. Anything carrying more than a
// scheme and host is rejected rather than trimmed into shape.
func canonicalOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("origin %q is not a valid URL: %w", raw, err)
	}
	if u.Host == "" || !allowedOriginScheme(u) {
		return "", fmt.Errorf(
			"origin %q must be an absolute https URL (http is accepted only for localhost, 127.0.0.1, and [::1])", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("origin %q must not embed credentials", raw)
	}
	if u.RawQuery != "" {
		return "", fmt.Errorf("origin %q must not carry a query string", raw)
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("origin %q must not carry a fragment", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("origin %q must not carry a path", raw)
	}

	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	if scheme == "https" {
		// :443 is redundant for https only; for a loopback http origin it is a
		// port like any other and dropping it would change where formae connects.
		host = strings.TrimSuffix(host, ":443")
	}
	return scheme + "://" + host, nil
}

// allowedOriginScheme reports whether u's scheme is usable for a control
// plane: https anywhere, http only for a loopback host.
func allowedOriginScheme(u *url.URL) bool {
	if strings.EqualFold(u.Scheme, "https") {
		return true
	}
	return strings.EqualFold(u.Scheme, "http") && loopbackHosts[strings.ToLower(u.Hostname())]
}
