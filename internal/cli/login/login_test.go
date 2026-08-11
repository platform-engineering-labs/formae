// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// testTheme is the theme every test renders against. Tests assert the plain
// (non-TTY) rendering — loginIsTerminal (like refresh's and plugin's
// analogous seams) returns false for a *bytes.Buffer, since it isn't an
// *os.File, so the theme's actual palette never reaches the output; it only
// needs to be non-nil.
var testTheme = theme.New("formae")

// stubAuthClient is a test double for authClient. It returns canned
// responses and records whether LoginWait was invoked, and the LoginStart
// request it received, so tests can assert on the short-circuit behavior
// and on the flags actually passed through, without a plugin subprocess.
type stubAuthClient struct {
	loginStartResp *pkgauth.LoginStartResponse
	loginStartErr  error
	loginWaitResp  *pkgauth.LoginWaitResponse
	loginWaitErr   error
	logoutResp     *pkgauth.LogoutResponse
	logoutErr      error

	loginStartReq   *pkgauth.LoginStartRequest
	loginWaitCalled bool
	loginWaitReq    *pkgauth.LoginWaitRequest
}

func (s *stubAuthClient) LoginStart(req *pkgauth.LoginStartRequest) (*pkgauth.LoginStartResponse, error) {
	s.loginStartReq = req
	return s.loginStartResp, s.loginStartErr
}

func (s *stubAuthClient) LoginWait(req *pkgauth.LoginWaitRequest) (*pkgauth.LoginWaitResponse, error) {
	s.loginWaitCalled = true
	s.loginWaitReq = req
	return s.loginWaitResp, s.loginWaitErr
}

func (s *stubAuthClient) Logout() (*pkgauth.LogoutResponse, error) {
	return s.logoutResp, s.logoutErr
}

// TestRunLogin_BrowserPath exercises the browser flow: the plain instruction
// line naming the URL to open is rendered before LoginWait is called, then
// the resolved identity is printed as a ✓ completion line.
func TestRunLogin_BrowserPath(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:     "started",
			Method:     "browser",
			BrowserURL: "https://issuer.example/authorize?req=abc",
			SessionID:  "sess-1",
		},
		loginWaitResp: &pkgauth.LoginWaitResponse{
			SubjectName: "jane",
		},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, testTheme, false, false)
	require.NoError(t, err)

	assert.Equal(t, "Open this URL to sign in:\n  https://issuer.example/authorize?req=abc\n✓ signed in as jane\n", out.String())
	assert.True(t, c.loginWaitCalled)
	require.NotNil(t, c.loginWaitReq)
	assert.Equal(t, "sess-1", c.loginWaitReq.SessionID)

	require.NotNil(t, c.loginStartReq)
	assert.Equal(t, "browser", c.loginStartReq.Mode)
	assert.False(t, c.loginStartReq.Force)
}

// TestRunLogin_DevicePath exercises the device-code flow: the plain
// instruction line naming the verification URI and code is rendered before
// LoginWait is called, then the resolved identity is printed as a ✓
// completion line.
func TestRunLogin_DevicePath(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:          "started",
			Method:          "device",
			VerificationURI: "https://issuer.example/device",
			UserCode:        "ABCD-1234",
			SessionID:       "sess-2",
		},
		loginWaitResp: &pkgauth.LoginWaitResponse{
			SubjectName: "jane",
		},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, testTheme, false, true)
	require.NoError(t, err)

	assert.Equal(t, "Visit https://issuer.example/device and enter code: ABCD-1234\n✓ signed in as jane\n", out.String())
	assert.True(t, c.loginWaitCalled)

	require.NotNil(t, c.loginStartReq)
	assert.Equal(t, "device", c.loginStartReq.Mode)
	assert.False(t, c.loginStartReq.Force)
}

// TestRunLogin_ForceFlagPassedThrough verifies that --force is carried
// through to the LoginStart request unchanged, so a silently dropped or
// swapped flag would be caught rather than passing unnoticed.
func TestRunLogin_ForceFlagPassedThrough(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:      "already_authenticated",
			SubjectName: "jane",
		},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, testTheme, true, false)
	require.NoError(t, err)

	require.NotNil(t, c.loginStartReq)
	assert.True(t, c.loginStartReq.Force)
	assert.Equal(t, "browser", c.loginStartReq.Mode)
}

// TestRunLogin_AlreadyAuthenticatedShortCircuits verifies that when
// LoginStart reports an existing session, runLogin prints the identity as a
// ✓ completion line and returns without ever calling LoginWait — the
// property repeated invocation depends on.
func TestRunLogin_AlreadyAuthenticatedShortCircuits(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:      "already_authenticated",
			SubjectName: "jane",
		},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, testTheme, false, false)
	require.NoError(t, err)

	assert.Equal(t, "✓ already signed in as jane\n", out.String())
	assert.False(t, c.loginWaitCalled, "LoginWait must not be called when already authenticated")
}

// TestRunLogin_SignedInFallsBackToSubjectWhenNameEmpty verifies that when a
// plugin leaves SubjectName empty after LoginWait — both it and Subject are
// documented as optional — the success message falls back to the stable
// Subject id rather than printing "signed in as " with nothing after it.
func TestRunLogin_SignedInFallsBackToSubjectWhenNameEmpty(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:     "started",
			Method:     "browser",
			BrowserURL: "https://issuer.example/authorize?req=abc",
			SessionID:  "sess-1",
		},
		loginWaitResp: &pkgauth.LoginWaitResponse{
			Subject: "subj-123",
		},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, testTheme, false, false)
	require.NoError(t, err)

	assert.Equal(t, "Open this URL to sign in:\n  https://issuer.example/authorize?req=abc\n✓ signed in as subj-123\n", out.String())
}

// TestRunLogin_SignedInOmitsIdentityWhenNeitherSet verifies that when a
// plugin sets neither SubjectName nor Subject after LoginWait, the success
// message drops the "as <name>" clause entirely rather than printing a
// blank name.
func TestRunLogin_SignedInOmitsIdentityWhenNeitherSet(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:     "started",
			Method:     "browser",
			BrowserURL: "https://issuer.example/authorize?req=abc",
			SessionID:  "sess-1",
		},
		loginWaitResp: &pkgauth.LoginWaitResponse{},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, testTheme, false, false)
	require.NoError(t, err)

	assert.Equal(t, "Open this URL to sign in:\n  https://issuer.example/authorize?req=abc\n✓ signed in\n", out.String())
}

// TestRunLogin_AlreadyAuthenticatedFallsBackToSubjectWhenNameEmpty verifies
// the same SubjectName-to-Subject fallback on the already-authenticated
// short-circuit path.
func TestRunLogin_AlreadyAuthenticatedFallsBackToSubjectWhenNameEmpty(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:  "already_authenticated",
			Subject: "subj-456",
		},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, testTheme, false, false)
	require.NoError(t, err)

	assert.Equal(t, "✓ already signed in as subj-456\n", out.String())
}

// TestRunLogin_AlreadyAuthenticatedOmitsIdentityWhenNeitherSet verifies the
// same no-identity fallback on the already-authenticated short-circuit path.
func TestRunLogin_AlreadyAuthenticatedOmitsIdentityWhenNeitherSet(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status: "already_authenticated",
		},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, testTheme, false, false)
	require.NoError(t, err)

	assert.Equal(t, "✓ already signed in\n", out.String())
}

// TestRunLogin_UnsupportedOnLoginStart verifies that a plugin declining
// LoginStart with the unsupported code fails the command with the shared
// unsupported copy, without proceeding to LoginWait.
func TestRunLogin_UnsupportedOnLoginStart(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			ErrorCode: pkgauth.ErrorCodeUnsupported,
		},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, testTheme, false, false)
	require.Error(t, err)
	assert.Equal(t, "the active profile's auth plugin does not support this operation", err.Error())
	assert.False(t, c.loginWaitCalled)
}

// TestRunLogin_PipedOutputHasNoANSI verifies that the non-TTY rendering
// path — the one every test above exercises, since loginIsTerminal reports
// false for a *bytes.Buffer — never emits an ANSI escape sequence, so
// redirected/piped output stays plain text.
func TestRunLogin_PipedOutputHasNoANSI(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:     "started",
			Method:     "browser",
			BrowserURL: "https://issuer.example/authorize?req=abc",
			SessionID:  "sess-1",
		},
		loginWaitResp: &pkgauth.LoginWaitResponse{
			SubjectName: "jane",
		},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, testTheme, false, false)
	require.NoError(t, err)

	assert.NotContains(t, out.String(), "\x1b[", "piped output must be ANSI-free")
}

// TestRunLogout_Success verifies the success message renders as a ✓
// completion line.
func TestRunLogout_Success(t *testing.T) {
	c := &stubAuthClient{
		logoutResp: &pkgauth.LogoutResponse{},
	}

	var out bytes.Buffer
	err := runLogout(c, &out, testTheme)
	require.NoError(t, err)
	assert.Equal(t, "✓ signed out\n", out.String())
	assert.NotContains(t, out.String(), "\x1b[", "piped output must be ANSI-free")
}

// TestRunLogout_Unsupported verifies that an unsupported Logout maps to the
// shared unsupported copy.
func TestRunLogout_Unsupported(t *testing.T) {
	c := &stubAuthClient{
		logoutResp: &pkgauth.LogoutResponse{ErrorCode: pkgauth.ErrorCodeUnsupported},
	}

	var out bytes.Buffer
	err := runLogout(c, &out, testTheme)
	require.Error(t, err)
	assert.Equal(t, "the active profile's auth plugin does not support this operation", err.Error())
}
