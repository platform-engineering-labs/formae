// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package authmsg

import (
	"testing"

	"github.com/stretchr/testify/assert"

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// TestDescribeAuthError pins the exact copy for every known error code, and
// verifies that a code the mapper does not recognise degrades to the
// caller-supplied fallback text instead of erroring or going blank — the
// behavior a newer plugin talking to an older CLI depends on.
func TestDescribeAuthError(t *testing.T) {
	tests := []struct {
		name     string
		code     pkgauth.ErrorCode
		fallback string
		want     string
	}{
		{
			name:     "unsupported",
			code:     pkgauth.ErrorCodeUnsupported,
			fallback: "irrelevant",
			want:     "the active profile's auth plugin does not support this operation",
		},
		{
			name:     "not logged in",
			code:     pkgauth.ErrorCodeNotLoggedIn,
			fallback: "irrelevant",
			want:     "not signed in — run 'formae login'",
		},
		{
			name:     "session expired",
			code:     pkgauth.ErrorCodeSessionExpired,
			fallback: "irrelevant",
			want:     "your session expired — run 'formae login'",
		},
		{
			name:     "issuer unreachable",
			code:     pkgauth.ErrorCodeIssuerUnreachable,
			fallback: "irrelevant",
			want:     "the identity provider is unreachable — try again shortly",
		},
		{
			name:     "unknown code degrades to fallback",
			code:     pkgauth.ErrorCode("wat"),
			fallback: "the plugin's own error text",
			want:     "the plugin's own error text",
		},
		{
			name:     "empty code degrades to fallback",
			code:     "",
			fallback: "a generic message",
			want:     "a generic message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DescribeAuthError(tt.code, tt.fallback)
			assert.Equal(t, tt.want, got)
		})
	}
}
