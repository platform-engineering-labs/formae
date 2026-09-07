// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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

	// onLogout runs before the answer is returned, so a test can observe the
	// state of the world at the moment the tokens are dropped.
	onLogout func()

	// onLoginWait runs before the wait answers, so a test can observe what had
	// already reached the caller at the moment the flow began waiting — which is
	// where the "the URL is out before this blocks" promise lives.
	onLoginWait func()
}

func (s *stubAuthClient) LoginStart(req *pkgauth.LoginStartRequest) (*pkgauth.LoginStartResponse, error) {
	s.loginStartReq = req
	return s.loginStartResp, s.loginStartErr
}

func (s *stubAuthClient) LoginWait(req *pkgauth.LoginWaitRequest) (*pkgauth.LoginWaitResponse, error) {
	s.loginWaitCalled = true
	s.loginWaitReq = req
	if s.onLoginWait != nil {
		s.onLoginWait()
	}
	return s.loginWaitResp, s.loginWaitErr
}

func (s *stubAuthClient) Logout() (*pkgauth.LogoutResponse, error) {
	if s.onLogout != nil {
		s.onLogout()
	}
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
	_, err := runLogin(c, &out, testTheme, false, nil)
	require.NoError(t, err)

	assert.Equal(t, "Open this URL to sign in:\n  https://issuer.example/authorize?req=abc\n✓ signed in as jane\n", out.String())
	assert.True(t, c.loginWaitCalled)
	require.NotNil(t, c.loginWaitReq)
	assert.Equal(t, "sess-1", c.loginWaitReq.SessionID)

	require.NotNil(t, c.loginStartReq)
	assert.Equal(t, "browser", c.loginStartReq.Mode)
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
	_, err := runLogin(c, &out, testTheme, true, nil)
	require.NoError(t, err)

	assert.Equal(t, "Visit https://issuer.example/device and enter code: ABCD-1234\n✓ signed in as jane\n", out.String())
	assert.True(t, c.loginWaitCalled)

	require.NotNil(t, c.loginStartReq)
	assert.Equal(t, "device", c.loginStartReq.Mode)
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
	_, err := runLogin(c, &out, testTheme, false, nil)
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
	_, err := runLogin(c, &out, testTheme, false, nil)
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
	_, err := runLogin(c, &out, testTheme, false, nil)
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
	_, err := runLogin(c, &out, testTheme, false, nil)
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
	_, err := runLogin(c, &out, testTheme, false, nil)
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
	_, err := runLogin(c, &out, testTheme, false, nil)
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
	_, err := runLogin(c, &out, testTheme, false, nil)
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

// stubCredentials is a test double for credentialProvider: the credential the
// auth plugin hands back once a sign-in has completed. It records how it was
// asked, so a test can assert the CLI does not force a refresh of a
// credential a sign-in has just produced.
type stubCredentials struct {
	resp   *pkgauth.GetAuthHeaderResponse
	err    error
	calls  int
	forced []bool
}

func (s *stubCredentials) GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error) {
	s.calls++
	s.forced = append(s.forced, forceRefresh)
	return s.resp, s.err
}

// bearerCredentials is the usable answer: a Bearer token under the canonical
// Authorization key, which is the only shape the CLI can transmit.
func bearerCredentials() *stubCredentials {
	return &stubCredentials{resp: &pkgauth.GetAuthHeaderResponse{
		Headers: map[string][]string{"Authorization": {"Bearer " + testToken}},
	}}
}

// loginStep returns the sync half of a login, pointed at the fixture's config
// directory and its stub control plane, on a hosted profile whose auth block
// the gate accepts.
func (f *syncFixture) loginStep() syncStep {
	return syncStep{
		Creds:      bearerCredentials(),
		Entry:      syncFromProfile{conn: hosted(oidcAuth(f.t, nil))},
		ConfigDir:  func() (string, error) { return f.root, nil },
		NewClient:  func(string) CloudClient { return f.client },
		Verifier:   f.verifier,
		Out:        f.out,
		Theme:      testTheme,
		CloudFlag:  testOrigin,
		IssuerFlag: testIssuer,
	}
}

// signedIn is a client whose LoginStart completes a browser flow and whose
// LoginWait signs the user in, which is the ordinary successful login.
func signedIn() *stubAuthClient {
	return &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:     "started",
			Method:     "browser",
			BrowserURL: "https://issuer.example/authorize?req=abc",
			SessionID:  "sess-1",
		},
		loginWaitResp: &pkgauth.LoginWaitResponse{SubjectName: "jane"},
	}
}

// alreadySignedIn is a client whose LoginStart reports an existing session,
// the branch that never calls LoginWait.
func alreadySignedIn() *stubAuthClient {
	return &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{
			Status:      "already_authenticated",
			SubjectName: "jane",
		},
	}
}

// lines returns the output as lines, so a test can assert on the marker each
// one carries.
func lines(out *bytes.Buffer) []string {
	return strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
}

// TestLoginSyncsAfterASignIn verifies that a completed sign-in is followed by
// the sync, that its change lines are rendered in the same acknowledgment
// idiom as the sign-in line that precedes them, and that the credential the
// request carries is the one the plugin already holds rather than a forced
// refresh of it.
func TestLoginSyncsAfterASignIn(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	step := f.loginStep()

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), step, false))

	assert.Equal(t, []string{
		"Open this URL to sign in:",
		"  https://issuer.example/authorize?req=abc",
		"✓ signed in as jane",
		"✓ created profile " + nameOne,
		"✓ made profile " + nameOne + " active (was default; `formae profile use default` switches back)",
	}, lines(f.out))
	assert.Equal(t, 1, f.client.calls)
	assert.True(t, f.exists(nameOne))
	assert.Equal(t, []bool{false}, step.Creds.(*stubCredentials).forced,
		"a sign-in has just produced a credential, so nothing forces a refresh of it")
}

// TestLoginSyncsOnTheAlreadyAuthenticatedBranch verifies that the
// short-circuit taken when a session already exists still syncs: repairing a
// profile that has gone missing is the same path as creating one, so running
// login again is how a user gets it back.
func TestLoginSyncsOnTheAlreadyAuthenticatedBranch(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	c := alreadySignedIn()

	require.NoError(t, runLoginAndSync(context.Background(), c, f.loginStep(), false))

	assert.False(t, c.loginWaitCalled)
	assert.Equal(t, 1, f.client.calls, "an existing session covers the same installations")
	assert.True(t, f.exists(nameOne))
	assert.Equal(t, []string{
		"✓ already signed in as jane",
		"✓ created profile " + nameOne,
		"✓ made profile " + nameOne + " active (was default; `formae profile use default` switches back)",
	}, lines(f.out))
}

// TestLoginForcesARefreshOnTheAlreadyAuthenticatedBranch verifies that a login
// which short-circuits on an existing session asks the auth plugin to refresh
// the credential rather than reusing the one it holds.
//
// The credential carries the caller's grants as claims, and the control plane
// resolves them at mint time, so a token minted before the user's access
// changed reports the old answer for as long as it stays valid. That is not a
// hypothetical ordering: signing in and only then being granted an
// installation is the ordinary onboarding sequence, and reusing the cached
// token there enumerates nothing and writes no profile.
//
// This is the branch where no sign-in has just happened, which is what
// separates it from TestLoginSyncsAfterASignIn: a credential a flow has only
// now produced is current by construction and refreshing it buys nothing.
func TestLoginForcesARefreshOnTheAlreadyAuthenticatedBranch(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	step := f.loginStep()

	require.NoError(t, runLoginAndSync(context.Background(), alreadySignedIn(), step, false))

	assert.Equal(t, []bool{true}, step.Creds.(*stubCredentials).forced,
		"no sign-in produced this credential, so its grants may predate the user's access")
}

// TestLoginDoesNotSyncWhenTheSignInFails verifies that a failed sign-in ends
// the command before anything is asked or written.
func TestLoginDoesNotSyncWhenTheSignInFails(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	c := &stubAuthClient{
		loginStartResp: &pkgauth.LoginStartResponse{ErrorCode: pkgauth.ErrorCodeUnsupported},
	}

	err := runLoginAndSync(context.Background(), c, f.loginStep(), false)

	require.Error(t, err)
	assert.Equal(t, "the active profile's auth plugin does not support this operation", err.Error())
	assert.Zero(t, f.client.calls)
	assert.False(t, f.exists(nameOne))
}

// TestLoginOnAClassicProfileIsUnchanged verifies that a profile addressing the
// user's own agent syncs nothing, asks the auth plugin for no credential, and
// prints exactly what it printed before profile sync existed. A notice on
// every login of the most common kind of profile there is would be noise.
func TestLoginOnAClassicProfileIsUnchanged(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	step := f.loginStep()
	step.Entry = syncFromProfile{conn: &pkgmodel.ClassicConnection{}}

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), step, false))

	assert.Equal(t,
		"Open this URL to sign in:\n  https://issuer.example/authorize?req=abc\n✓ signed in as jane\n",
		f.out.String())
	assert.Zero(t, f.client.calls)
	assert.Zero(t, step.Creds.(*stubCredentials).calls, "no credential is fetched for a profile that cannot sync")
}

// TestLoginNoticesAnUnpairedPlatformOverride verifies that overriding one half
// of the control-plane pair is reported as sync not applying — a notice and a
// zero exit — rather than as a failed login.
func TestLoginNoticesAnUnpairedPlatformOverride(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	step := f.loginStep()
	step.IssuerFlag = ""

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), step, false))

	notice := lines(f.out)[len(lines(f.out))-1]
	assert.True(t, strings.HasPrefix(notice, "· "), "sync not applying is a notice, not a warning: %q", notice)
	assert.Contains(t, notice, "--cloud-issuer")
	assert.Zero(t, f.client.calls)
	assert.False(t, f.exists(nameOne))
}

// TestLoginNoticesAGateRefusal verifies that a hosted profile the gate refuses
// is reported with the gate's own reason and exits zero: the sign-in worked,
// only the sync does not apply.
func TestLoginNoticesAGateRefusal(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	step := f.loginStep()
	step.Entry = syncFromProfile{conn: hosted(oidcAuth(f.t, map[string]any{"issuer": testOtherIssuer}))}

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), step, false))

	notice := lines(f.out)[len(lines(f.out))-1]
	assert.True(t, strings.HasPrefix(notice, "· "), "sync not applying is a notice, not a warning: %q", notice)
	assert.Contains(t, notice, "issuer")
	assert.Zero(t, f.client.calls, "a refused gate sends no request")
	assert.False(t, f.exists(nameOne))
}

// TestLoginNoticesALedgerItCannotRead verifies that a ledger written by a
// newer formae ends the sync with a message and a zero exit: the sign-in
// succeeded and nothing was left needing repair.
func TestLoginNoticesALedgerItCannotRead(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	data := []byte(`{"schemaVersion": 99, "entries": []}`)
	require.NoError(t, os.WriteFile(f.store.ManagedLedgerPath(), data, 0o600))

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false))

	assert.Contains(t, f.out.String(), "schema version")
	assert.Zero(t, f.client.calls)
	assert.False(t, f.exists(nameOne))
}

// TestLoginFailsWhenTheSignInProducedNoUsableCredential verifies that a plugin
// that cannot produce a credential the CLI can send fails the command rather
// than being read as a sync with nothing to do — including the case where it
// reports success and returns a credential under a key the CLI never
// transmits. Every case says the sign-in itself worked.
func TestLoginFailsWhenTheSignInProducedNoUsableCredential(t *testing.T) {
	cases := []struct {
		name  string
		creds *stubCredentials
	}{
		{
			name:  "the plugin failed",
			creds: &stubCredentials{err: errors.New("the auth plugin exited")},
		},
		{
			name: "the plugin reported an error",
			creds: &stubCredentials{resp: &pkgauth.GetAuthHeaderResponse{
				ErrorCode: pkgauth.ErrorCodeUnsupported,
			}},
		},
		{
			name:  "the plugin answered nothing",
			creds: &stubCredentials{},
		},
		{
			name:  "the header is empty",
			creds: &stubCredentials{resp: &pkgauth.GetAuthHeaderResponse{}},
		},
		{
			name: "the credential is under a key the CLI never sends",
			creds: &stubCredentials{resp: &pkgauth.GetAuthHeaderResponse{
				Headers: map[string][]string{"authorization": {"Bearer " + testToken}},
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncFixture(t)
			f.answer(installation(installOne, "prod", stateActive))
			step := f.loginStep()
			step.Creds = tc.creds

			err := runLoginAndSync(context.Background(), signedIn(), step, false)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "you are signed in",
				"the sign-in worked, and a message that reads as a failed login sends the user to fix the wrong thing")
			assert.Contains(t, f.out.String(), "✓ signed in as jane")
			assert.Zero(t, f.client.calls)
			assert.NotContains(t, err.Error(), testToken, "no message repeats a credential")
		})
	}
}

// TestLoginFailsWhenTheSyncDidNotComplete verifies that an enumeration that
// failed exits non-zero, and says both facts: signed in, sync incomplete.
func TestLoginFailsWhenTheSyncDidNotComplete(t *testing.T) {
	f := newSyncFixture(t)
	f.client.err = &cloudapi.TransientError{Cause: errors.New("the control plane returned HTTP 503")}

	err := runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "you are signed in")
	assert.Contains(t, err.Error(), "503")
	assert.Contains(t, f.out.String(), "✓ signed in as jane")
}

// TestLoginReportsWhatTheFailedSyncDidAchieve verifies that a run which
// created a profile and then could not read one of its own records reports the
// change it made alongside the failure, rather than reading as though nothing
// happened.
func TestLoginReportsWhatTheFailedSyncDidAchieve(t *testing.T) {
	f := newSyncFixture(t)
	f.writeLedger(managedEntry(entryOwned, unreadableName, installTwo, rawEntry{
		"fingerprint": fingerprint([]byte("whatever was there")),
	}))
	f.answer(installation(installOne, "prod", stateActive))

	err := runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "created 1 profile")
	assert.True(t, f.exists(nameOne), "the profile the run did write is there")
	assert.Contains(t, f.out.String(), "! ", "the record it could not read is warned about")
}

// TestLoginFailsWhenAnotherProcessHoldsTheLedger verifies that a lock held
// elsewhere exits non-zero, names the other process rather than a lock file,
// and says what to do about it.
func TestLoginFailsWhenAnotherProcessHoldsTheLedger(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	held, err := lockLedger(f.store.ManagedLockPath())
	require.NoError(t, err)
	defer func() { _ = held.Unlock() }()

	err = runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false)

	require.Error(t, err)
	assert.ErrorIs(t, err, errLedgerLocked,
		"a caller telling a contended ledger from a real failure has the sentinel to test against")
	assert.Contains(t, err.Error(), "another formae process")
	assert.NotContains(t, err.Error(), f.store.ManagedLockPath(), "a lock path is not something the user can act on")
	assert.Zero(t, f.client.calls)
}

// TestLoginFailsWhenNoDesiredInstallationGotAProfile verifies the desired-set
// rule: a run where nothing this run's grants cover ended up with a profile we
// own exits non-zero, and a profile retained for an unrelated reason does not
// make it look successful.
func TestLoginFailsWhenNoDesiredInstallationGotAProfile(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installTwo, "staging", stateActive))
	_, _, syncErr := runSync(context.Background(), f.loginStep(), false)
	require.NoError(t, syncErr)
	require.True(t, f.exists(nameTwo))

	f.verifier.err = errors.New("the generated profile does not load")
	f.answer(
		installation(installOne, "prod", stateActive),
		installation(installTwo, "staging", "warping"),
	)

	_, _, err := runSync(context.Background(), f.loginStep(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "you are signed in")
	assert.Contains(t, err.Error(), "wrote no profile for any of the 1 installation your grants cover",
		"the row this exercises is the desired set nothing satisfied, not a sync that did not complete")
	assert.True(t, f.exists(nameTwo), "the retained profile is not one this run's grants were satisfied by")
	assert.False(t, f.exists(nameOne))
}

// TestLoginFailsWhenEveryDesiredRecordWasSkipped verifies the precedence
// between two rows that both match an all-skipped run: skips alone exit zero,
// but a desired set nothing satisfied exits non-zero, and that row is
// evaluated first.
func TestLoginFailsWhenEveryDesiredRecordWasSkipped(t *testing.T) {
	f := newSyncFixture(t)
	mine := []byte("# a profile I wrote myself\n")
	f.writeProfile(nameOne, mine)
	f.answer(installation(installOne, "prod", stateActive))

	err := runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrote no profile for any of the 1 installation your grants cover",
		"a taken name is a skip, so this ends on the desired-set row and never on an incomplete sync")
	assert.Equal(t, mine, f.read(nameOne), "the name was taken, and a taken name is a skip and not a write")
	assert.Contains(t, f.out.String(), "! ")
}

// TestLoginSucceedsWhenSomeRecordsWereSkipped verifies that a skip which still
// leaves the desired set partly satisfied is a warning and a zero exit.
func TestLoginSucceedsWhenSomeRecordsWereSkipped(t *testing.T) {
	f := newSyncFixture(t)
	f.writeProfile(nameOne, []byte("# a profile I wrote myself\n"))
	f.answer(
		installation(installOne, "prod", stateActive),
		installation(installTwo, "staging", stateActive),
	)

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false))

	assert.True(t, f.exists(nameTwo))
	warned := 0
	for _, line := range lines(f.out) {
		if strings.HasPrefix(line, "! ") {
			warned++
		}
	}
	assert.Equal(t, 1, warned, "one skip, one warning")
}

// TestLoginSucceedsOnANonAuthoritativeSnapshot verifies that an answer too
// incomplete to license a removal still exits zero, with the client's warning
// shown.
func TestLoginSucceedsOnANonAuthoritativeSnapshot(t *testing.T) {
	f := newSyncFixture(t)
	f.client.snapshot = Snapshot{
		Installations: []Installation{installation(installOne, "prod", stateActive)},
		Warnings:      []string{"the control plane's answer was incomplete, so nothing was removed"},
	}

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false))

	assert.True(t, f.exists(nameOne))
	assert.Contains(t, f.out.String(), "! the control plane's answer was incomplete")
}

// TestLoginFailsWhenANonAuthoritativeRunPublishedNothing verifies the other
// overlapping pair: a non-authoritative snapshot exits zero on its own, but
// not when nothing in the desired set was satisfied either.
func TestLoginFailsWhenANonAuthoritativeRunPublishedNothing(t *testing.T) {
	f := newSyncFixture(t)
	f.writeProfile(nameOne, []byte("# a profile I wrote myself\n"))
	f.client.snapshot = Snapshot{
		Installations: []Installation{installation(installOne, "prod", stateActive)},
		Warnings:      []string{"the control plane's answer was incomplete, so nothing was removed"},
	}

	err := runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "you are signed in")
	assert.Contains(t, err.Error(), "wrote no profile for any of the 1 installation your grants cover",
		"an incomplete snapshot is not a failure to complete: the row is the desired set nothing satisfied")
}

// TestLoginSucceedsOnACleanRun verifies the ordinary case: profiles written,
// nothing warned, zero exit.
func TestLoginSucceedsOnACleanRun(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(
		installation(installOne, "prod", stateActive),
		installation(installTwo, "staging", stateActive),
	)

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false))

	for _, line := range lines(f.out) {
		assert.False(t, strings.HasPrefix(line, "! "), "nothing to warn about: %q", line)
		assert.False(t, strings.HasPrefix(line, "· "), "nothing to notice: %q", line)
	}
	assert.True(t, f.exists(nameOne))
	assert.True(t, f.exists(nameTwo))
}

// TestLoginSyncsAgainstTheResolvedOrigin verifies that the control plane the
// request goes to is the resolved one, so the override reaches the client
// rather than only the gate.
func TestLoginSyncsAgainstTheResolvedOrigin(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	var asked string
	step := f.loginStep()
	step.NewClient = func(origin string) CloudClient {
		asked = origin
		return f.client
	}

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), step, false))

	assert.Equal(t, testOrigin, asked)
}

// TestLoginCmdRegistersTheCloudFlags verifies that both halves of the
// control-plane override are offered, each documenting its environment
// variable and its default the way the hub URL flag does.
func TestLoginCmdRegistersTheCloudFlags(t *testing.T) {
	flags := LoginCmd().Flags()

	cloud := flags.Lookup("cloud")
	require.NotNil(t, cloud)
	assert.Contains(t, cloud.Usage, "FORMAE_CLOUD_URL")
	assert.Contains(t, cloud.Usage, DefaultCloudURL)

	issuer := flags.Lookup("cloud-issuer")
	require.NotNil(t, issuer)
	assert.Contains(t, issuer.Usage, "FORMAE_CLOUD_ISSUER")
	assert.Contains(t, issuer.Usage, DefaultCloudIssuer)
}

// TestLoginFailsWhenTheConfigDirectoryCannotBeFound verifies that a config
// directory formae cannot locate ends the sync non-zero and says so, rather
// than syncing against some other directory.
func TestLoginFailsWhenTheConfigDirectoryCannotBeFound(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	step := f.loginStep()
	step.ConfigDir = func() (string, error) { return "", errors.New("resolve home directory: no home") }

	err := runLoginAndSync(context.Background(), signedIn(), step, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "you are signed in")
	assert.Contains(t, err.Error(), "no home")
	assert.Zero(t, f.client.calls)
}

// TestLoginShowsAWarningTheUserCanActOn verifies that a ledger this formae
// will not act on reaches the user as a warning naming the file to remove: a
// profile written while it is in that state is one nothing manages until they
// do.
func TestLoginShowsAWarningTheUserCanActOn(t *testing.T) {
	f := newSyncFixture(t)
	f.writeLedger(
		managedEntry(entryOwned, nameOne, installOne, rawEntry{"fingerprint": fingerprint([]byte("one"))}),
		managedEntry(entryOwned, nameOne, installTwo, rawEntry{"fingerprint": fingerprint([]byte("two"))}),
	)
	f.answer(installation(installOne, "prod", stateActive))

	_ = runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false)

	assert.Contains(t, f.out.String(), "! managed-profile ledger entries")
	assert.Contains(t, f.out.String(), "remove "+f.store.ManagedLedgerPath())
}
