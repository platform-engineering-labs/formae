// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connection

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const trustedIssuer = "https://auth.formae.ai"

func hostedConn(issuer string) *pkgmodel.HostedConnection {
	return &pkgmodel.HostedConnection{
		Endpoint:     "https://cloud.formae.ai",
		Installation: "3f2b8c14-0000-4000-8000-000000000000",
		Auth: json.RawMessage(
			`{"type":"oidc","role":"cli","issuer":"` + issuer + `"}`),
	}
}

func classicConn() *pkgmodel.ClassicConnection {
	return &pkgmodel.ClassicConnection{URL: "http://localhost", Port: 49684}
}

type stubCreds struct {
	resp  *pkgauth.GetAuthHeaderResponse
	err   error
	calls int
}

func (c *stubCreds) GetAuthHeader(bool) (*pkgauth.GetAuthHeaderResponse, error) {
	c.calls++
	return c.resp, c.err
}

func bearer() *stubCreds {
	return &stubCreds{resp: &pkgauth.GetAuthHeaderResponse{
		Headers: map[string][]string{"Authorization": {"Bearer abc.def"}},
	}}
}

func codeOf(t *testing.T, err error) printer.Code {
	t.Helper()
	var f *printer.Failure
	if !errors.As(err, &f) {
		t.Fatalf("want a declared failure, got %#v", err)
	}
	return f.Code
}

// A classic connection resolves without any credential: the MCP sends none to a
// self-hosted agent, and an empty credential key would invite one.
func TestResolveClassicCarriesNoCredential(t *testing.T) {
	creds := bearer()
	got, err := resolve(input{Conn: classicConn(), Profile: "dev", Creds: creds})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Credential != "" {
		t.Errorf("classic must carry no credential, got %q", got.Credential)
	}
	if creds.calls != 0 {
		t.Errorf("classic must not drive the auth plugin, got %d calls", creds.calls)
	}
	if got.Connection["mode"] != "classic" {
		t.Errorf("connection = %#v", got.Connection)
	}
	if got.Profile != "dev" {
		t.Errorf("profile = %q", got.Profile)
	}
}

func TestResolveHostedCarriesTheCredential(t *testing.T) {
	got, err := resolve(input{
		Conn: hostedConn(trustedIssuer), Profile: "prod", Explicit: true, Creds: bearer(),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Credential != "Bearer abc.def" {
		t.Errorf("credential = %q", got.Credential)
	}
	if got.Connection["installation"] != "3f2b8c14-0000-4000-8000-000000000000" {
		t.Errorf("connection = %#v", got.Connection)
	}
}

// Ambiguity is decided here, before auth is touched, so the MCP needs no second
// command to count profiles and no credential is minted before the user has
// chosen which installation they meant.
func TestResolveRejectsImplicitHostedSelection(t *testing.T) {
	creds := bearer()
	_, err := resolve(input{
		Conn:     hostedConn(trustedIssuer),
		Profile:  "prod",
		Profiles: []string{"prod", "staging"},
		Creds:    creds,
	})
	if got := codeOf(t, err); got != printer.CodeAmbiguousProfile {
		t.Fatalf("code = %q, want ambiguous_profile", got)
	}
	if creds.calls != 0 {
		t.Fatalf("no credential may be minted before the user has chosen: %d calls", creds.calls)
	}
}

func TestResolveAllowsHostedWhenTheChoiceIsNotAmbiguous(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   input
	}{
		{"the profile was named", input{Explicit: true, Profiles: []string{"prod", "staging"}}},
		{"only one profile exists", input{Profiles: []string{"prod"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.Conn, in.Profile, in.Creds = hostedConn(trustedIssuer), "prod", bearer()
			if _, err := resolve(in); err != nil {
				t.Fatalf("resolve: %v", err)
			}
		})
	}
}

// Classic never prompts, however many profiles exist.
func TestResolveNeverCallsClassicAmbiguous(t *testing.T) {
	if _, err := resolve(input{
		Conn: classicConn(), Profile: "dev", Profiles: []string{"a", "b", "c"}, Creds: bearer(),
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

func TestResolveRefusesAnUntrustedIssuer(t *testing.T) {
	creds := bearer()
	_, err := resolve(input{
		Conn: hostedConn("https://auth.evil.example"), Profile: "prod", Explicit: true, Creds: creds,
	})
	if got := codeOf(t, err); got != printer.CodeUntrustedIssuer {
		t.Fatalf("code = %q, want untrusted_issuer", got)
	}
	if creds.calls != 0 {
		t.Fatalf("the auth plugin must not be driven for an untrusted issuer: %d calls", creds.calls)
	}
}

// A hosted connection that cannot be authenticated is not a usable connection,
// so resolution fails rather than reporting a clean endpoint with a broken
// credential and deferring the failure into an opaque response.
func TestResolveFailsWhenTheAuthPluginRefuses(t *testing.T) {
	_, err := resolve(input{
		Conn: hostedConn(trustedIssuer), Profile: "prod", Explicit: true,
		Creds: &stubCreds{resp: &pkgauth.GetAuthHeaderResponse{
			ErrorCode: "session_expired", Error: "run formae login",
		}},
	})
	var f *printer.Failure
	if !errors.As(err, &f) || f.Code != printer.CodeAuthFailed {
		t.Fatalf("want auth_failed, got %#v", err)
	}
	if f.Details["pluginCode"] != "session_expired" {
		t.Errorf("the plugin's own code is the only thing that can say why: %#v", f.Details)
	}
}

func TestResolveRejectsAConnectionItCannotUse(t *testing.T) {
	_, err := resolve(input{Profile: "dev", Creds: bearer()})
	if err == nil {
		t.Fatal("a profile resolving no usable connection must fail")
	}
	if got := codeOf(t, err); got != printer.CodeNoConnection {
		t.Fatalf("code = %q, want no_connection", got)
	}
}

// The credential never appears in a failure, whatever went wrong.
func TestResolveNeverPutsTheCredentialInAnError(t *testing.T) {
	const secret = "Bearer super.secret.value"
	_, err := resolve(input{
		Conn: hostedConn(trustedIssuer), Profile: "prod",
		Profiles: []string{"prod", "staging"},
		Creds: &stubCreds{resp: &pkgauth.GetAuthHeaderResponse{
			Headers: map[string][]string{"Authorization": {secret}},
		}},
	})
	if err == nil {
		t.Fatal("expected the ambiguity refusal")
	}
	if strings.Contains(err.Error(), "super.secret") {
		t.Fatalf("a failure must never repeat the credential: %v", err)
	}
}
