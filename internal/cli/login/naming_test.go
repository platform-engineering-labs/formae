// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
)

// TestDeriveProfileName_TableDriven covers the documented derivation rules:
// slugging of each of the three components, joining with the fixed 12-hex
// suffix, and the various ways a component can slug away to nothing.
func TestDeriveProfileName_TableDriven(t *testing.T) {
	tests := []struct {
		name             string
		orgName          string
		tenantName       string
		installationName string
		installationID   string
		want             string
	}{
		{
			name:             "simple ascii components",
			orgName:          "acme",
			tenantName:       "default",
			installationName: "prod",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             "acme-default-prod-3f2b8c140000",
		},
		{
			name:             "uppercase is lowercased",
			orgName:          "ACME",
			tenantName:       "Default",
			installationName: "PROD",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             "acme-default-prod-3f2b8c140000",
		},
		{
			name:             "dots and spaces become single hyphens",
			orgName:          "acme corp.",
			tenantName:       "default",
			installationName: "prod",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             "acme-corp-default-prod-3f2b8c140000",
		},
		{
			name:             "non-ascii characters become hyphens and collapse",
			orgName:          "Größe & Co.",
			tenantName:       "default",
			installationName: "prod",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             "gr-e-co-default-prod-3f2b8c140000",
		},
		{
			name:             "leading and trailing punctuation trimmed",
			orgName:          "--acme--",
			tenantName:       "default",
			installationName: "prod",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             "acme-default-prod-3f2b8c140000",
		},
		{
			name:             "one empty component omitted",
			orgName:          "",
			tenantName:       "default",
			installationName: "prod",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             "default-prod-3f2b8c140000",
		},
		{
			name:             "two empty components omitted",
			orgName:          "",
			tenantName:       "",
			installationName: "prod",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             "prod-3f2b8c140000",
		},
		{
			name:             "all components empty falls back to formae",
			orgName:          "",
			tenantName:       "",
			installationName: "",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             "formae-3f2b8c140000",
		},
		{
			name:             "all components non-alphanumeric falls back to formae",
			orgName:          "***",
			tenantName:       "...",
			installationName: "///",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             "formae-3f2b8c140000",
		},
		{
			name:             "over-long component truncates to 24 runes",
			orgName:          strings.Repeat("a", 40),
			tenantName:       "default",
			installationName: "prod",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             strings.Repeat("a", 24) + "-default-prod-3f2b8c140000",
		},
		{
			name:             "truncation landing on a hyphen is re-trimmed",
			orgName:          strings.Repeat("a", 23) + "-bbbb",
			tenantName:       "default",
			installationName: "prod",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			// The 24th rune of "aaa...a(23)-bbbb" is the hyphen itself, so
			// after truncation it must be re-trimmed away rather than kept
			// as a trailing hyphen.
			want: strings.Repeat("a", 23) + "-default-prod-3f2b8c140000",
		},
		{
			name:             "reserved windows basenames are unreachable",
			orgName:          "con",
			tenantName:       "",
			installationName: "",
			installationID:   "3f2b8c14-0000-4000-8000-000000000000",
			want:             "con-3f2b8c140000",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveProfileName(tc.orgName, tc.tenantName, tc.installationName, tc.installationID)
			assert.Equal(t, tc.want, got)
			assert.NoError(t, store.ValidateName(got))
		})
	}
}

// TestDeriveProfileName_Deterministic verifies that repeated calls with the
// same inputs always produce the same output, and that the result depends
// only on the four inputs — never on anything else (e.g. other installations
// that might also be named around the same time).
func TestDeriveProfileName_Deterministic(t *testing.T) {
	first := deriveProfileName("acme", "default", "prod", "3f2b8c14-0000-4000-8000-000000000000")
	for i := 0; i < 5; i++ {
		got := deriveProfileName("acme", "default", "prod", "3f2b8c14-0000-4000-8000-000000000000")
		assert.Equal(t, first, got)
	}
}

// TestDeriveProfileName_SuffixIsFixedWidth verifies the suffix never widens,
// even when the components alone would otherwise slug down to something
// short enough that widening might seem tempting. A profile name must never
// depend on what other installations happen to be visible.
func TestDeriveProfileName_SuffixIsFixedWidth(t *testing.T) {
	got := deriveProfileName("a", "", "", "3f2b8c14-0000-4000-8000-000000000000")
	assert.Equal(t, "a-3f2b8c140000", got)
}

// TestDeriveProfileName_AdversarialCorpusAlwaysProducesValidNames throws a
// corpus of hostile component strings at deriveProfileName and checks that
// every resulting name satisfies store.ValidateName and can never be used to
// escape the profiles/ directory (no path separators, no "..").
func TestDeriveProfileName_AdversarialCorpusAlwaysProducesValidNames(t *testing.T) {
	adversarial := []string{
		"",
		"../etc",
		"../../etc/passwd",
		"..",
		"/etc/passwd",
		"a/b/c",
		"\x00\x01\x02control",
		"NUL\x00adjacent",
		"emoji \U0001F600 party",
		"\u200erlm\u200fmarks",
		strings.Repeat("x", 1000),
		"con",
		"nul",
		"aux",
		"CON.pkl",
		"formae",
		"---",
		"a-b-c",
		"Größe & Co.",
		"\t\n\r whitespace \t\n\r",
	}

	installationID := "3f2b8c14-0000-4000-8000-000000000000"

	for _, org := range adversarial {
		for _, tenant := range adversarial {
			for _, inst := range adversarial {
				got := deriveProfileName(org, tenant, inst, installationID)
				if err := store.ValidateName(got); err != nil {
					t.Fatalf("deriveProfileName(%q, %q, %q, ...) = %q, invalid: %v", org, tenant, inst, got, err)
				}
				assert.NotContains(t, got, "/")
				assert.NotContains(t, got, "..")
			}
		}
	}
}
