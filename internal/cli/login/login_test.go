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

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// stubAuthClient is a test double for authClient. It returns canned
// responses and records whether LoginWait was invoked, so tests can assert
// on the short-circuit behavior without a plugin subprocess.
type stubAuthClient struct {
	loginStartResp *pkgauth.LoginStartResponse
	loginStartErr  error
	loginWaitResp  *pkgauth.LoginWaitResponse
	loginWaitErr   error
	logoutResp     *pkgauth.LogoutResponse
	logoutErr      error

	loginWaitCalled bool
	loginWaitReq    *pkgauth.LoginWaitRequest
}

func (s *stubAuthClient) LoginStart(req *pkgauth.LoginStartRequest) (*pkgauth.LoginStartResponse, error) {
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

// TestRunLogin_BrowserPath exercises the browser flow: the URL to open is
// rendered before LoginWait is called, then the resolved identity is printed.
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
	err := runLogin(c, &out, false, false)
	require.NoError(t, err)

	assert.Equal(t, "Open this URL to sign in:\n  https://issuer.example/authorize?req=abc\nsigned in as jane\n", out.String())
	assert.True(t, c.loginWaitCalled)
	require.NotNil(t, c.loginWaitReq)
	assert.Equal(t, "sess-1", c.loginWaitReq.SessionID)
}

// TestRunLogin_DevicePath exercises the device-code flow: the verification
// URI and code are rendered before LoginWait is called.
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
	err := runLogin(c, &out, false, true)
	require.NoError(t, err)

	assert.Equal(t, "Visit https://issuer.example/device and enter code: ABCD-1234\nsigned in as jane\n", out.String())
	assert.True(t, c.loginWaitCalled)
}

// TestRunLogin_AlreadyAuthenticatedShortCircuits verifies that when
// LoginStart reports an existing session, runLogin prints the identity and
// returns without ever calling LoginWait — the property repeated invocation
// depends on.
func TestRunLogin_AlreadyAuthenticatedShortCircuits(t *testing.T) {
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:      "already_authenticated",
			SubjectName: "jane",
		},
	}

	var out bytes.Buffer
	err := runLogin(c, &out, false, false)
	require.NoError(t, err)

	assert.Equal(t, "already signed in as jane\n", out.String())
	assert.False(t, c.loginWaitCalled, "LoginWait must not be called when already authenticated")
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
	err := runLogin(c, &out, false, false)
	require.Error(t, err)
	assert.Equal(t, "the active profile's auth plugin does not support this operation", err.Error())
	assert.False(t, c.loginWaitCalled)
}

// TestRunLogout_Success verifies the plain success message.
func TestRunLogout_Success(t *testing.T) {
	c := &stubAuthClient{
		logoutResp: &pkgauth.LogoutResponse{},
	}

	var out bytes.Buffer
	err := runLogout(c, &out)
	require.NoError(t, err)
	assert.Equal(t, "signed out\n", out.String())
}

// TestRunLogout_Unsupported verifies that an unsupported Logout maps to the
// shared unsupported copy.
func TestRunLogout_Unsupported(t *testing.T) {
	c := &stubAuthClient{
		logoutResp: &pkgauth.LogoutResponse{ErrorCode: pkgauth.ErrorCodeUnsupported},
	}

	var out bytes.Buffer
	err := runLogout(c, &out)
	require.Error(t, err)
	assert.Equal(t, "the active profile's auth plugin does not support this operation", err.Error())
}

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
			got := describeAuthError(tt.code, tt.fallback)
			assert.Equal(t, tt.want, got)
		})
	}
}
