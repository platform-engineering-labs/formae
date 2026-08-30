// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	provxgcp "github.com/platform-engineering-labs/oox/provx/gcp"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

const (
	testProject       = "example-project"
	testProjectNumber = "123456789012"
	testProviderName  = "//iam.googleapis.com/projects/" + testProjectNumber +
		"/locations/global/workloadIdentityPools/formae-ai/providers/formae-ai"
)

// stubGCPProvisioner drives the provisioning seam without reaching Google.
type stubGCPProvisioner struct {
	result *provxgcp.Result
	err    error
	calls  int
}

func (s *stubGCPProvisioner) Create(_ context.Context) (*provxgcp.Result, error) {
	s.calls++
	return s.result, s.err
}

func installGCPProvisioner(t *testing.T, p *stubGCPProvisioner, gotSubject *string) {
	t.Helper()
	restore := newGCPProvisioner
	newGCPProvisioner = func(_ context.Context, _, subject, _ string) (gcpProvisioner, error) {
		if gotSubject != nil {
			*gotSubject = subject
		}
		return p, nil
	}
	t.Cleanup(func() { newGCPProvisioner = restore })
}

// stubCredentialState pins what the run believes about local credentials, and
// counts how often the interactive sign-in was reached.
func stubCredentialState(t *testing.T, state credentialState, logins *int) {
	t.Helper()
	restoreFind, restoreLogin := findCredentials, runGcloudLogin
	findCredentials = func(_ context.Context) (credentialState, error) { return state, nil }
	runGcloudLogin = func(_ context.Context, _ io.Writer) error {
		if logins != nil {
			*logins++
		}
		// A login that "works" makes credentials usable from then on.
		findCredentials = func(_ context.Context) (credentialState, error) { return credentialsUsable, nil }
		return nil
	}
	t.Cleanup(func() { findCredentials, runGcloudLogin = restoreFind, restoreLogin })
}

func seedGCPRun(t *testing.T) *controlPlane {
	t.Helper()
	cp := newControlPlane(t)
	cp.registerBody = `{"cloud":"gcp","account":"` + testProject + `","workloadIdentityProvider":"` + testProviderName + `"}`
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	return cp
}

func gcpLocalArgs() []string {
	return []string{"gcp", "--project", testProject, "--no-input",
		"--output-consumer", "machine", "--output-schema", "json"}
}

// The local path: credentials are already usable, provx receives the
// server-produced subject verbatim, and the provider it returns is registered.
func TestGCPLocalPathProvisionsAndRegisters(t *testing.T) {
	stub := &stubGCPProvisioner{result: &provxgcp.Result{
		ProviderName:  testProviderName,
		ProjectNumber: testProjectNumber,
	}}
	var gotSubject string
	installGCPProvisioner(t, stub, &gotSubject)
	stubCredentialState(t, credentialsUsable, nil)
	cp := seedGCPRun(t)

	out, err := runConnect(t, gcpLocalArgs()...)

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Equal(t, "registered", got["phase"])
	assert.Equal(t, "gcp", got["cloud"])
	assert.Equal(t, testProject, got["account"])
	assert.Equal(t, testProviderName, got["workloadIdentityProvider"])
	assert.NotContains(t, got, "roleArn", "a GCP document must not carry an AWS coordinate")

	assert.Equal(t, 1, stub.calls)
	assert.Equal(t, "fai:acme/"+contractInstallation, gotSubject, "the server-produced subject travels verbatim")

	posts := cp.posts()
	require.Len(t, posts, 1)
	assert.Contains(t, posts[0].Body, testProviderName)
	assert.NotContains(t, posts[0].Body, "roleArn",
		"the control plane's GCP variant admits no roleArn, not even an empty one")
}

// TestGCPLocalRequiresProjectNumberAgreement takes the compared coordinate
// from what the provisioner returned, never from command input: a provisioner
// that returned a name in the wrong project must be caught.
func TestGCPLocalRequiresProjectNumberAgreement(t *testing.T) {
	stub := &stubGCPProvisioner{result: &provxgcp.Result{
		// The name says one project, the resolved number says another.
		ProviderName:  "//iam.googleapis.com/projects/999999999999/locations/global/workloadIdentityPools/formae-ai/providers/formae-ai",
		ProjectNumber: testProjectNumber,
	}}
	installGCPProvisioner(t, stub, nil)
	stubCredentialState(t, credentialsUsable, nil)
	cp := seedGCPRun(t)

	out, err := runConnect(t, gcpLocalArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "account_mismatch", got["code"])
	assert.Empty(t, cp.posts(), "nothing may be registered when the coordinate names another project")
}

// TestGCPRegisterOnlyRejectsMalformedProvider covers the mode that holds no
// credentials: the only thing it can check is the coordinate's shape, and it
// must actually check it rather than posting whatever it was handed.
func TestGCPRegisterOnlyRejectsMalformedProvider(t *testing.T) {
	bad := map[string]string{
		"lookalike host": "//iam.googleapis.com.evil/projects/1/locations/global/workloadIdentityPools/formae-ai/providers/formae-ai",
		"https form":     "https://iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/formae-ai/providers/formae-ai",
		"nonsense":       "not-a-provider",
	}
	for name, provider := range bad {
		t.Run(name, func(t *testing.T) {
			cp := seedGCPRun(t)
			out, err := runConnect(t, "gcp", "--project", testProject,
				"--workload-identity-provider", provider, "--no-input",
				"--output-consumer", "machine", "--output-schema", "json")

			require.Error(t, err)
			assert.Empty(t, cp.posts(), "a malformed coordinate must never be registered")
			_ = out
		})
	}
}

// Register-only says what it did not check, so nobody reads "registered" as
// "working".
func TestGCPRegisterOnlySaysWhatItDidNotVerify(t *testing.T) {
	seedGCPRun(t)

	out, err := runConnect(t, "gcp", "--project", testProject,
		"--workload-identity-provider", testProviderName, "--no-input",
		"--output-consumer", "machine", "--output-schema", "json")

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Equal(t, testProviderName, got["workloadIdentityProvider"])
	assert.Contains(t, fmt.Sprintf("%v", got["warnings"]), "shape only")
}

// Register-only must not reach for credentials at all: needing them would
// destroy the one reason the mode exists.
func TestGCPRegisterOnlyNeedsNoCredentials(t *testing.T) {
	logins := 0
	stubCredentialState(t, credentialsMissing, &logins)
	seedGCPRun(t)

	out, err := runConnect(t, "gcp", "--project", testProject,
		"--workload-identity-provider", testProviderName, "--no-input",
		"--output-consumer", "machine", "--output-schema", "json")

	require.NoError(t, err, "out: %s", out)
	assert.Zero(t, logins, "register-only signed in, which it must never need to do")
}

// TestGCPMachineModeNeverSignsIn: a caller that built one fixed command line
// did not consent to a browser opening. Machine output and --no-input both
// mean the run reports what to do instead.
func TestGCPMachineModeNeverSignsIn(t *testing.T) {
	for _, args := range [][]string{
		{"gcp", "--project", testProject, "--no-input", "--output-consumer", "machine", "--output-schema", "json"},
		{"gcp", "--project", testProject, "--output-consumer", "machine", "--output-schema", "json"},
	} {
		logins := 0
		stubCredentialState(t, credentialsMissing, &logins)
		cp := seedGCPRun(t)
		installGCPProvisioner(t, &stubGCPProvisioner{result: &provxgcp.Result{
			ProviderName: testProviderName, ProjectNumber: testProjectNumber,
		}}, nil)

		out, err := runConnect(t, args...)

		require.Error(t, err, "out: %s", out)
		assert.Zero(t, logins, "a machine-mode run opened a browser")
		got := decodeOut(t, out)
		assert.Equal(t, "credentials_required", got["code"])
		assert.Contains(t, fmt.Sprintf("%v", got["details"]), "gcloud auth application-default login",
			"the failure must name the command to run")
		assert.Empty(t, cp.posts())
	}
}

// TestGCPSignsInWhenItMay is the other half: where prompting is allowed and
// the credentials are simply absent, formae does the sign-in rather than
// telling the operator to.
func TestGCPSignsInWhenItMay(t *testing.T) {
	logins := 0
	stubCredentialState(t, credentialsMissing, &logins)

	err := ensureCredentials(context.Background(), io.Discard, true)

	require.NoError(t, err)
	assert.Equal(t, 1, logins, "formae did not sign in on the operator's behalf")
}

func TestGCPDoesNotSignInWhenCredentialsAreUsable(t *testing.T) {
	logins := 0
	stubCredentialState(t, credentialsUsable, &logins)

	require.NoError(t, ensureCredentials(context.Background(), io.Discard, true))
	assert.Zero(t, logins, "a usable credential was replaced by a fresh sign-in")
}

// An authorization failure is not an authentication failure. Signing in again
// returns the same principal, and would overwrite credentials the operator
// configured deliberately, so the project-unreachable case must report the
// project rather than reach for a login.
func TestGCPProjectUnreachableIsNotACredentialProblem(t *testing.T) {
	logins := 0
	stubCredentialState(t, credentialsUsable, &logins)
	installGCPProvisioner(t, &stubGCPProvisioner{
		err: &provxgcp.ProjectUnreachableError{Project: testProject},
	}, nil)
	cp := seedGCPRun(t)

	out, err := runConnect(t, gcpLocalArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "project_unreachable", got["code"])
	assert.Zero(t, logins, "an authorization failure triggered a sign-in")
	assert.Empty(t, cp.posts())
}

// A disabled API and a denied permission both arrive as HTTP 403 and have
// nothing in common as remedies, so they must not collapse into one code.
func TestGCPProvisionFailuresAreClassifiedApart(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"disabled api", &provxgcp.APIDisabledError{API: "iam.googleapis.com"}, "api_disabled"},
		{"denied permission", &provxgcp.PermissionDeniedError{Permission: "iam.workloadIdentityPools.create"}, "not_authorized"},
		{"org policy", &provxgcp.OrgPolicyError{Reason: "ORG_POLICY_VIOLATION"}, "not_authorized"},
		{"foreign provider", &provxgcp.ProviderNotOursError{Name: "x", IssuerFound: "https://elsewhere.example"}, "provider_conflict"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubCredentialState(t, credentialsUsable, nil)
			installGCPProvisioner(t, &stubGCPProvisioner{err: c.err}, nil)
			cp := seedGCPRun(t)

			out, err := runConnect(t, gcpLocalArgs()...)

			require.Error(t, err)
			got := decodeOut(t, out)
			assert.Equal(t, c.wantCode, got["code"])
			assert.Empty(t, cp.posts())
		})
	}
}

// A run that provisioned and then failed to register must say that the
// project now trusts an installation the control plane does not know about.
func TestGCPRegistrationFailureNamesTheStandingTrust(t *testing.T) {
	stubCredentialState(t, credentialsUsable, nil)
	installGCPProvisioner(t, &stubGCPProvisioner{result: &provxgcp.Result{
		ProviderName: testProviderName, ProjectNumber: testProjectNumber,
	}}, nil)
	cp := seedGCPRun(t)
	cp.registerStatus = 500
	cp.registerBody = `{"error":"boom"}`

	out, err := runConnect(t, gcpLocalArgs()...)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "now trusts this installation",
		"the failure must name the trust that stands")
	_ = out
}

// TestGCPAllowLoginReachesTheSignIn is the case that was unreachable before:
// the agent runs on the operator's machine, consumes machine output, and can
// still have a browser completed by the person sitting there. Without an
// explicit opt-in the render format was read as "nobody is present", which
// made the sign-in impossible from the interface most people use.
func TestGCPAllowLoginReachesTheSignIn(t *testing.T) {
	logins := 0
	stubCredentialState(t, credentialsMissing, &logins)
	installGCPProvisioner(t, &stubGCPProvisioner{result: &provxgcp.Result{
		ProviderName: testProviderName, ProjectNumber: testProjectNumber,
	}}, nil)
	seedGCPRun(t)

	out, err := runConnect(t, "gcp", "--project", testProject, "--no-input", "--allow-login",
		"--output-consumer", "machine", "--output-schema", "json")

	require.NoError(t, err, "out: %s", out)
	assert.Equal(t, 1, logins, "--allow-login did not reach the sign-in")
	got := decodeOut(t, out)
	assert.Equal(t, testProviderName, got["workloadIdentityProvider"])
}

// The default is unchanged: without the opt-in, machine output still refuses
// and names the command, so a CI script is not handed a browser.
func TestGCPWithoutAllowLoginMachineModeStillRefuses(t *testing.T) {
	logins := 0
	stubCredentialState(t, credentialsMissing, &logins)
	seedGCPRun(t)

	out, err := runConnect(t, "gcp", "--project", testProject,
		"--output-consumer", "machine", "--output-schema", "json")

	require.Error(t, err)
	assert.Zero(t, logins)
	assert.Equal(t, "credentials_required", decodeOut(t, out)["code"])
}

// --allow-login governs the browser, not this command's own prompts, so a
// usable credential still means no sign-in.
func TestGCPAllowLoginDoesNotSignInWhenCredentialsWork(t *testing.T) {
	logins := 0
	stubCredentialState(t, credentialsUsable, &logins)
	installGCPProvisioner(t, &stubGCPProvisioner{result: &provxgcp.Result{
		ProviderName: testProviderName, ProjectNumber: testProjectNumber,
	}}, nil)
	seedGCPRun(t)

	_, err := runConnect(t, "gcp", "--project", testProject, "--no-input", "--allow-login",
		"--output-consumer", "machine", "--output-schema", "json")

	require.NoError(t, err)
	assert.Zero(t, logins, "a usable credential was replaced by a sign-in")
}

func TestGCPProjectIsRequired(t *testing.T) {
	_, err := decideGCPMode(gcpOptions{})
	require.Error(t, err, "a run with no project must be refused rather than inferring one")
}

func TestGCPModeSelection(t *testing.T) {
	local, err := decideGCPMode(gcpOptions{Project: testProject})
	require.NoError(t, err)
	assert.Equal(t, gcpModeLocal, local, "no coordinate means provision it")

	registerOnly, err := decideGCPMode(gcpOptions{Project: testProject, WorkloadIdentityProvider: testProviderName})
	require.NoError(t, err)
	assert.Equal(t, gcpModeRegisterOnly, registerOnly)
}

func TestSplitSubjectRefusesWhatItDoesNotRecognise(t *testing.T) {
	tenant, installation, err := splitSubject("fai:acme/inst-1")
	require.NoError(t, err)
	assert.Equal(t, "acme", tenant)
	assert.Equal(t, "inst-1", installation)

	for _, bad := range []string{"", "acme/inst", "fai:", "fai:acme", "fai:/inst", "fai:acme/"} {
		if _, _, err := splitSubject(bad); err == nil {
			t.Errorf("splitSubject(%q) was accepted", bad)
		}
	}
}

// TestRegisteredDocumentBytes pins all three clouds' documents. Making
// roleArn omitempty for GCP's sake must not drop it from an AWS document,
// which is a v2 contract a consumer already reads; the same applies to
// azureTenantId/azureClientId, added for Azure without moving the version.
func TestRegisteredDocumentBytes(t *testing.T) {
	awsDoc, err := json.Marshal(registeredDocument(statusRegisteredUnverified, testAccount, contractRoleArn, nil))
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"schemaVersion": 2,
		"phase": "registered",
		"status": "registered_unverified",
		"cloud": "aws",
		"account": "`+testAccount+`",
		"roleArn": "`+contractRoleArn+`"
	}`, string(awsDoc))

	gcpDoc, err := json.Marshal(gcpRegisteredDocument(statusRegisteredUnverified, testProject, testProviderName, nil))
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"schemaVersion": 2,
		"phase": "registered",
		"status": "registered_unverified",
		"cloud": "gcp",
		"account": "`+testProject+`",
		"workloadIdentityProvider": "`+testProviderName+`"
	}`, string(gcpDoc))

	const testTenantID = "11111111-1111-1111-1111-111111111111"
	const testClientID = "22222222-2222-2222-2222-222222222222"
	azureDoc, err := json.Marshal(azureRegisteredDocument(statusRegisteredUnverified, testSubscription, testTenantID, testClientID, nil))
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"schemaVersion": 2,
		"phase": "registered",
		"status": "registered_unverified",
		"cloud": "azure",
		"account": "`+testSubscription+`",
		"azureTenantId": "`+testTenantID+`",
		"azureClientId": "`+testClientID+`"
	}`, string(azureDoc))
}

// TestRegisteredHumanNamesEachCloudInItsOwnWords pins both renderings.
//
// The AWS line is unchanged, and the GCP line has to be: a shared line prints
// "aws account" over a GCP project and an empty role beneath it, which reads
// as a value that failed rather than one that never existed.
func TestRegisteredHumanNamesEachCloudInItsOwnWords(t *testing.T) {
	var aws, gcp strings.Builder
	require.NoError(t, printRegisteredHuman(&aws, false, nil,
		registeredDocument(statusRegisteredUnverified, testAccount, contractRoleArn, nil), contractInstallation))
	require.NoError(t, printRegisteredHuman(&gcp, false, nil,
		gcpRegisteredDocument(statusRegisteredUnverified, testProject, testProviderName, nil), contractInstallation))

	assert.Contains(t, aws.String(), "aws account "+testAccount)
	assert.Contains(t, aws.String(), "role: "+contractRoleArn)

	assert.Contains(t, gcp.String(), "gcp project "+testProject)
	assert.Contains(t, gcp.String(), "workload identity provider: "+testProviderName)
	assert.NotContains(t, gcp.String(), "aws account", "a GCP run called the project an aws account")
	assert.NotContains(t, gcp.String(), "role:", "a GCP run printed a role it does not have")
}

// A gcloud installed after a long-running process started is invisible to it
// and to every child it spawns, because the environment was inherited once at
// start. The login-shell probe is the way out, and it must actually be
// consulted when PATH has nothing.
func TestResolveGcloudFallsBackToTheLoginShell(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "gcloud")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	// A login shell that says its piece before answering, which is what a real
	// one does: banners, motd, whatever the user's rc prints.
	shell := filepath.Join(dir, "fakeshell")
	script := "#!/bin/sh\nprintf 'welcome to the shell\\n'\nprintf '%s' \"__formae_gcloud_begin__" +
		fake + "__formae_gcloud_end__\"\n"
	require.NoError(t, os.WriteFile(shell, []byte(script), 0o755))

	t.Setenv("PATH", dir+"-empty") // nothing on PATH
	t.Setenv("SHELL", shell)
	t.Setenv("CLOUDSDK_ROOT_DIR", "")

	got, err := resolveGcloud(context.Background())
	require.NoError(t, err, "the probe did not rescue a gcloud that is off PATH")
	assert.Equal(t, fake, got)
}

// PATH answers for nearly everyone, and must be preferred: the probe runs the
// user's shell rc and should not be paid for when there is nothing to fix.
func TestResolveGcloudPrefersPath(t *testing.T) {
	dir := t.TempDir()
	onPath := filepath.Join(dir, "gcloud")
	require.NoError(t, os.WriteFile(onPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	probed := 0
	restore := gcloudFromLoginShell
	gcloudFromLoginShell = func(context.Context) string { probed++; return "/never/used" }
	t.Cleanup(func() { gcloudFromLoginShell = restore })

	t.Setenv("PATH", dir)
	t.Setenv("CLOUDSDK_ROOT_DIR", "")

	got, err := resolveGcloud(context.Background())
	require.NoError(t, err)
	assert.Equal(t, onPath, got)
	assert.Zero(t, probed, "the login shell was run even though PATH answered")
}

// The probe's answer comes from the user's shell, so it is checked rather than
// trusted: a path that is not an executable file is no answer at all.
func TestResolveGcloudRejectsAnUnusableProbeAnswer(t *testing.T) {
	dir := t.TempDir()
	notExec := filepath.Join(dir, "gcloud")
	require.NoError(t, os.WriteFile(notExec, []byte("not executable"), 0o644))

	for _, answer := range []string{notExec, "relative/path", "/nonexistent/gcloud", dir, ""} {
		restore := gcloudFromLoginShell
		gcloudFromLoginShell = func(context.Context) string { return answer }
		t.Setenv("PATH", dir+"-empty")
		t.Setenv("CLOUDSDK_ROOT_DIR", "")

		_, err := resolveGcloud(context.Background())
		assert.ErrorIs(t, err, errGcloudNotFound, "probe answer %q was accepted", answer)
		gcloudFromLoginShell = restore
	}
}

// gcloud's own convention for an unusual layout: a fixed contract rather than
// a guessed install location.
func TestResolveGcloudHonoursCloudsdkRootDir(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	inRoot := filepath.Join(root, "bin", "gcloud")
	require.NoError(t, os.WriteFile(inRoot, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	t.Setenv("CLOUDSDK_ROOT_DIR", root)
	t.Setenv("PATH", root+"-empty")

	got, err := resolveGcloud(context.Background())
	require.NoError(t, err)
	assert.Equal(t, inRoot, got)
}

// A shell that never answers costs a bounded wait, not a hung command.
func TestLoginShellProbeIsBounded(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "hangingshell")
	require.NoError(t, os.WriteFile(shell, []byte("#!/bin/sh\nsleep 60\n"), 0o755))
	t.Setenv("SHELL", shell)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	got := gcloudFromLoginShell(ctx)

	assert.Empty(t, got)
	assert.Less(t, time.Since(start), 30*time.Second, "the probe outlived its bound")
}

// Provisioning hands a near-owner grant to an installation in someone's
// project. An interactive run says so and stops for an answer first, as all
// three AWS paths have since they shipped.
func TestGCPInteractiveRunConfirmsBeforeProvisioning(t *testing.T) {
	restore := confirmFn
	asked := 0
	var gotTitle, gotBody string
	confirmFn = func(_ *theme.Theme, title, body string) (bool, error) {
		asked++
		gotTitle, gotBody = title, body
		return false, nil // decline: nothing may be created
	}
	t.Cleanup(func() { confirmFn = restore })

	restoreTTY := isInteractive
	isInteractive = func() bool { return true }
	t.Cleanup(func() { isInteractive = restoreTTY })

	stubCredentialState(t, credentialsUsable, nil)
	stub := &stubGCPProvisioner{result: &provxgcp.Result{
		ProviderName: testProviderName, ProjectNumber: testProjectNumber,
	}}
	installGCPProvisioner(t, stub, nil)
	cp := seedGCPRun(t)

	_, err := runConnect(t, "gcp", "--project", testProject)

	require.Error(t, err, "declining the confirmation must abort")
	assert.Equal(t, 1, asked, "the run did not ask before provisioning")
	assert.Contains(t, gotTitle, "gcp project "+testProject, "the prompt does not name what is being connected")
	assert.Contains(t, gotBody, "near-owner", "the prompt does not say what is being granted")
	assert.Zero(t, stub.calls, "provisioning ran despite the user declining")
	assert.Empty(t, cp.posts(), "a connection was registered despite the user declining")
}

// runConnectSplit runs the command with stdout and stderr separated, which is
// the only way to state where a stream's content actually landed. runConnect
// points both at one buffer, so a test built on it cannot tell the machine
// document apart from anything printed beside it.
func runConnectSplit(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	c := ConnectCmd()
	c.SetOut(&stdout)
	c.SetErr(&stderr)
	c.SetArgs(args)
	err := c.Execute()
	return stdout.String(), stderr.String(), err
}

// stubLoggingGcloudLogin stands in for the sign-in and, unlike
// stubCredentialState's silent stub, writes to the writer it is handed - which
// is what the real one does, both for its own "running ..." line and for
// gcloud's stdout. A stub that writes nothing cannot observe where those bytes
// go, which is the whole question here.
func stubLoggingGcloudLogin(t *testing.T, state credentialState, logins *int) {
	t.Helper()
	restoreFind, restoreLogin := findCredentials, runGcloudLogin
	findCredentials = func(_ context.Context) (credentialState, error) { return state, nil }
	runGcloudLogin = func(_ context.Context, w io.Writer) error {
		if logins != nil {
			*logins++
		}
		_, _ = fmt.Fprintf(w, "running %s\n", gcloudLoginCommand)
		_, _ = fmt.Fprintln(w, "Credentials saved to file: [/home/u/.config/gcloud/application_default_credentials.json]")
		findCredentials = func(_ context.Context) (credentialState, error) { return credentialsUsable, nil }
		return nil
	}
	t.Cleanup(func() { findCredentials, runGcloudLogin = restoreFind, restoreLogin })
}

// Under machine output the document is the whole of stdout. The sign-in prints
// progress for a person to read, and the only caller that asks for it is an
// agent parsing that same stream, so the two cannot share it: chatter ahead of
// the JSON turns a successful connect into output the caller cannot read.
func TestGCPAllowLoginKeepsTheMachineDocumentClean(t *testing.T) {
	logins := 0
	stubLoggingGcloudLogin(t, credentialsMissing, &logins)
	installGCPProvisioner(t, &stubGCPProvisioner{result: &provxgcp.Result{
		ProviderName: testProviderName, ProjectNumber: testProjectNumber,
	}}, nil)
	seedGCPRun(t)

	stdout, stderr, err := runConnectSplit(t, "gcp", "--project", testProject, "--no-input",
		"--allow-login", "--output-consumer", "machine", "--output-schema", "json")

	require.NoError(t, err, "stdout: %s stderr: %s", stdout, stderr)
	require.Equal(t, 1, logins, "--allow-login did not reach the sign-in")

	got := decodeOut(t, stdout)
	assert.Equal(t, testProviderName, got["workloadIdentityProvider"])
	assert.Contains(t, stderr, gcloudLoginCommand,
		"the sign-in left no trace for the operator on stderr")
}

// The human path is unchanged: there stdout is prose already, so the sign-in
// belongs with the rest of what the operator is reading.
func TestGCPHumanRunKeepsTheSignInOnStdout(t *testing.T) {
	logins := 0
	stubLoggingGcloudLogin(t, credentialsMissing, &logins)
	installGCPProvisioner(t, &stubGCPProvisioner{result: &provxgcp.Result{
		ProviderName: testProviderName, ProjectNumber: testProjectNumber,
	}}, nil)
	seedGCPRun(t)

	stdout, _, err := runConnectSplit(t, "gcp", "--project", testProject, "--allow-login")

	require.NoError(t, err)
	require.Equal(t, 1, logins)
	assert.Contains(t, stdout, gcloudLoginCommand)
}
