// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"fmt"
	"net/url"
	"strings"
)

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
