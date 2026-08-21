// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package authmsg maps an auth plugin's ErrorCode to the copy formae shows
// the user. It depends on nothing beyond pkg/auth, so both
// internal/cli/login and internal/cli/app can import it: app cannot import
// login (login imports internal/cli/cmd, which app also sits behind), so the
// mapper lives in this shared, dependency-light package instead.
package authmsg

import (
	"fmt"

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// DescribeAuthError maps an auth plugin ErrorCode to the copy formae shows
// the user. An empty or unrecognised code — including one from a plugin
// built against a newer SDK than this CLI knows about — degrades to
// fallback, the plugin's own error text, rather than failing or going blank.
// When the code is unrecognised AND fallback is itself empty — a plugin may
// legitimately set only ErrorCode and leave Error blank — that degradation
// would otherwise produce an empty message, defeating the point of a
// version-skew fallback; in that case a generic message naming the unknown
// code (or, if even the code is empty, a generic message with no code) is
// returned instead.
func DescribeAuthError(code pkgauth.ErrorCode, fallback string) string {
	switch code {
	case pkgauth.ErrorCodeUnsupported:
		return "the active profile's auth plugin does not support this operation"
	case pkgauth.ErrorCodeNotLoggedIn:
		return "not signed in — run 'formae login'"
	case pkgauth.ErrorCodeSessionExpired:
		return "your session expired — run 'formae login'"
	case pkgauth.ErrorCodeIssuerUnreachable:
		return "the identity provider is unreachable — try again shortly"
	default:
		if fallback != "" {
			return fallback
		}
		if code == "" {
			return "the active profile's auth plugin reported an error with no further detail"
		}
		return fmt.Sprintf("the active profile's auth plugin reported an unrecognized error (code: %s)", code)
	}
}
