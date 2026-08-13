// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import "strings"

// maxSlugRunes bounds each slugged component so no single organisation,
// tenant, or installation name can dominate the derived profile name.
const maxSlugRunes = 24

// suffixHexLen is the fixed width of the installation-id suffix. It never
// widens: a profile name must be derivable from its own installation alone,
// never from which other installations happen to be visible at the time.
const suffixHexLen = 12

// deriveProfileName returns the profile name for an installation. The caller
// must have validated installationID as a canonical lowercase UUID first.
//
// The name is built entirely from characters store.ValidateName accepts
// ([a-zA-Z0-9_-]), so it is always a valid profile name by construction: the
// slugged components and the hex suffix can never combine into something
// that escapes the profiles/ directory or collides with a reserved
// filesystem basename. The installation UUID, not this name, is the stable
// identity; the name is cosmetic.
func deriveProfileName(orgName, tenantName, installationName, installationID string) string {
	var parts []string
	for _, s := range []string{orgName, tenantName, installationName} {
		if slugged := slug(s); slugged != "" {
			parts = append(parts, slugged)
		}
	}

	prefix := strings.Join(parts, "-")
	if prefix == "" {
		prefix = "formae"
	}

	return prefix + "-" + suffix(installationID)
}

// slug lowercases s, replaces every rune outside [a-z0-9] with a hyphen,
// collapses runs of hyphens, and trims leading/trailing hyphens. The result
// is truncated to maxSlugRunes runes and re-trimmed, since truncation can
// land exactly on a hyphen the collapsing step left behind. Because every
// character that survives is plain ASCII, truncating by rune count can never
// split a multi-byte character.
func slug(s string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(s) {
		if isSlugRune(r) {
			b.WriteRune(r)
			prevHyphen = false
			continue
		}
		if !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
	}

	trimmed := strings.Trim(b.String(), "-")

	runes := []rune(trimmed)
	if len(runes) > maxSlugRunes {
		runes = runes[:maxSlugRunes]
	}

	return strings.Trim(string(runes), "-")
}

// isSlugRune reports whether r survives slugging unchanged: a lowercase
// ASCII letter or digit.
func isSlugRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// suffix returns the first suffixHexLen hex characters of installationID
// with its dashes stripped.
func suffix(installationID string) string {
	hex := strings.ReplaceAll(installationID, "-", "")
	if len(hex) > suffixHexLen {
		hex = hex[:suffixHexLen]
	}
	return hex
}
