// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connection

import "regexp"

// bearerToken matches a bearer credential as it appears in resolved output.
var bearerToken = regexp.MustCompile(`Bearer [A-Za-z0-9._~+/=-]+`)

// redactCredentials removes bearer tokens from text that is about to be shown.
//
// It exists for failure messages. A test asserting that resolution fails will,
// on the day resolution unexpectedly succeeds, print the successful result -
// and a successful hosted resolution carries a live credential. The assertion
// guarding against exactly that sits below the one that prints it, and is
// unreachable on the path where a credential exists, so the guard alone was
// never enough.
func redactCredentials(s string) string {
	return bearerToken.ReplaceAllString(s, "Bearer [redacted]")
}
