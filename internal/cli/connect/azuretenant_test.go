// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ARM names the subscription's tenant in the challenge it returns to an
// unauthenticated request, which is what lets the credential-less path stop
// asking the operator to copy it. The deployment outputs it too, but a value
// the operator has to transport by hand is a value they can transport wrongly,
// and it is one of only two things that path asks of them.
func TestTenantFromChallenge(t *testing.T) {
	const tenant = "416727bd-1d5d-4540-81d7-d391007ed660"

	for _, tc := range []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "the shape ARM actually returns",
			header: `Bearer authorization_uri="https://login.windows.net/` + tenant + `", error="invalid_token", error_description="The authentication failed because of missing 'Authorization' header."`,
			want:   tenant,
		},
		{
			// The login host is not fixed: sovereign clouds use their own, and
			// the tenant is the last path segment whichever it is.
			name:   "a sovereign login host",
			header: `Bearer authorization_uri="https://login.microsoftonline.us/` + tenant + `"`,
			want:   tenant,
		},
		{
			name:   "a trailing slash on the uri",
			header: `Bearer authorization_uri="https://login.windows.net/` + tenant + `/"`,
			want:   tenant,
		},
		{
			name:   "oauth2 path segments after the tenant",
			header: `Bearer authorization_uri="https://login.windows.net/` + tenant + `/oauth2/authorize"`,
			want:   tenant,
		},
		{
			// Anything that is not a tenant guid must yield nothing rather than
			// a guess: registering the wrong tenant fails later and further
			// away, where it is harder to attribute.
			name:   "a common tenant alias rather than a guid",
			header: `Bearer authorization_uri="https://login.windows.net/common"`,
			want:   "",
		},
		{
			name:   "a challenge with no authorization_uri",
			header: `Bearer error="invalid_token"`,
			want:   "",
		},
		{
			name:   "no challenge at all",
			header: "",
			want:   "",
		},
		{
			name:   "an authorization_uri that is not a url",
			header: `Bearer authorization_uri="not a url at all"`,
			want:   "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tenantFromChallenge(tc.header))
		})
	}
}
