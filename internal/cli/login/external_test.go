// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login_test

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/login"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

type forgedCreds struct{ calls int }

func (c *forgedCreds) GetAuthHeader(bool) (*pkgauth.GetAuthHeaderResponse, error) {
	c.calls++
	return &pkgauth.GetAuthHeaderResponse{
		Headers: map[string][]string{"Authorization": {"Bearer forged"}},
	}, nil
}

// ValidatedHosted is exported so callers can hold one, so another package can
// write the zero value even though it cannot fill the fields. Minting through
// that forgery must fail, and must fail before the auth plugin is driven —
// otherwise the gate is advice rather than a boundary. This test lives outside
// the package because that is the only place the forgery is possible.
func TestAZeroValidatedHostedCannotMint(t *testing.T) {
	creds := &forgedCreds{}

	got, err := login.ValidatedHosted{}.Credential(creds, false)
	if err == nil {
		t.Fatalf("a zero value minted %q", got)
	}
	if got != "" {
		t.Fatalf("no credential may be returned, got %q", got)
	}
	if creds.calls != 0 {
		t.Fatalf("the auth plugin was driven %d times for a connection nothing validated", creds.calls)
	}
}
