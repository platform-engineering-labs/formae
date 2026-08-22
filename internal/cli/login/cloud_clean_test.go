// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
)

// The gate on the whole hosted sign-in path: a machine that has never been
// configured ends with the installation's profile active and no classic default
// beside it.
//
// It drives the cobra command rather than runCloudLoginAndSync, and that is the
// point. What would create the default is the profile resolution inside
// AppFromContext, which the command's RunE has to skip; a test of the inner
// function would still pass with an AppFromContext call put back in. Both halves
// of the promise are properties of the command, and neither is observable from a
// unit test of the auth step.

// cleanConfigDir points formae's config-directory resolution at an empty
// directory for the duration of a test, and returns it.
func cleanConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", dir)
	// The platform overrides have to be absent, or resolvePlatform refuses the
	// half-set pair the fixture's flags would make.
	clearCloudEnv(t)
	return dir
}

// stubCloudSignIn substitutes the three things a hosted sign-in reaches the
// outside world through: the auth plugin, the control plane, and the profile
// verifier. It returns the directory the command will treat as the config dir.
func stubCloudSignIn(t *testing.T, installations ...Installation) string {
	t.Helper()
	dir := cleanConfigDir(t)

	flow := signedIn()
	creds := bearerCredentials()

	realPlugin, realAPI, realVerifier := newAuthPlugin, newCloudAPI, newVerifier
	t.Cleanup(func() { newAuthPlugin, newCloudAPI, newVerifier = realPlugin, realAPI, realVerifier })

	// The command uses one plugin in both roles, so the stub supplies both: the
	// flow driver and the credential reader.
	newAuthPlugin = func(string, json.RawMessage, string) (authClient, credentialProvider, error) {
		return flow, creds, nil
	}
	newCloudAPI = func(string) CloudClient {
		return &stubCloudClient{snapshot: Snapshot{Installations: installations, Authoritative: true}}
	}
	newVerifier = func() profileVerifier { return &stubVerifier{} }

	return dir
}

// cliContext carries an App the way the real root command does.
//
// This is not decoration. AppFromContext returns AppNotFoundError immediately
// when the context holds no App, *before* it resolves a config path — so a test
// running without one would never bootstrap a profile, and "no default was
// created" would hold whether or not the command skipped that step. The negative
// control below is what caught it.
func cliContext() context.Context {
	return context.WithValue(context.Background(), "app", app.NewApp()) //nolint:staticcheck // the CLI keys this context value by a plain string; matching it is the point.
}

// runHostedLogin executes `formae login --hosted` exactly as the CLI would.
func runHostedLogin(t *testing.T, extra ...string) error {
	t.Helper()
	cmd := LoginCmd()
	cmd.SetArgs(append([]string{"--hosted"}, extra...))
	cmd.SilenceUsage = true
	return cmd.ExecuteContext(cliContext())
}

func TestCleanDirectory_HostedSignInCreatesNoClassicDefault(t *testing.T) {
	dir := stubCloudSignIn(t, installation(installOne, "prod", stateActive))

	require.NoError(t, runHostedLogin(t))

	s := store.New(dir)
	assert.NoFileExists(t, s.ProfilePath("default"),
		"a hosted sign-in bootstrapped a classic localhost profile")
}

func TestCleanDirectory_HostedSignInLeavesTheInstallationsProfileActive(t *testing.T) {
	dir := stubCloudSignIn(t, installation(installOne, "prod", stateActive))

	require.NoError(t, runHostedLogin(t))

	s := store.New(dir)
	name := cloudProfileName()
	assert.FileExists(t, s.ProfilePath(name))

	active, err := s.Active()
	require.NoError(t, err)
	assert.Equal(t, name, active)
}

// The profile addresses the installation that was enumerated, not the control
// plane that described it. They are deliberately different hosts.
func TestCleanDirectory_TheProfileAddressesTheInstallation(t *testing.T) {
	dir := stubCloudSignIn(t, installation(installOne, "prod", stateActive))

	require.NoError(t, runHostedLogin(t))

	content, err := os.ReadFile(store.New(dir).ProfilePath(cloudProfileName()))
	require.NoError(t, err)
	assert.Contains(t, string(content), testEndpoint)
	assert.Contains(t, string(content), installOne)
}

// The ledger records the profile, so the next sign-in can maintain and prune it.
// A profile written without a record is one formae could never manage again.
func TestCleanDirectory_TheLedgerRecordsTheProfile(t *testing.T) {
	dir := stubCloudSignIn(t, installation(installOne, "prod", stateActive))

	require.NoError(t, runHostedLogin(t))

	recorded, err := os.ReadFile(store.New(dir).ManagedLedgerPath())
	require.NoError(t, err)
	assert.Contains(t, string(recorded), installOne)
	assert.Contains(t, string(recorded), cloudProfileName())
}

// The negative that gives the first assertion its teeth. Without --hosted the
// command takes the profile path, which resolves the active profile and so does
// create the default — so the absence above is about the hosted path and not
// about a temp directory nothing ever wrote to.
//
// The sign-in itself is expected to fail: the bootstrapped default is a classic
// localhost profile with no auth block, and refusing that is correct. What is
// asserted is the file, not the outcome.
func TestCleanDirectory_WithoutHostedTheDefaultIsCreated(t *testing.T) {
	dir := cleanConfigDir(t)

	cmd := LoginCmd()
	cmd.SetArgs(nil)
	cmd.SilenceUsage = true
	_ = cmd.ExecuteContext(cliContext())

	assert.FileExists(t, store.New(dir).ProfilePath("default"),
		"the profile path no longer bootstraps, so the hosted assertion proves nothing")
}

// A classic active profile with no auth plugin no longer dead-ends bare
// `formae login`: the only sign-in formae offers in that state is the hosted
// platform, so the command falls through to it — and the synced installation
// profile becomes active, exactly as if --hosted had been passed.
func TestCleanDirectory_BareLoginFallsThroughToHosted(t *testing.T) {
	dir := stubCloudSignIn(t, installation(installOne, "prod", stateActive))

	cmd := LoginCmd()
	cmd.SetArgs(nil)
	cmd.SilenceUsage = true
	require.NoError(t, cmd.ExecuteContext(cliContext()))

	s := store.New(dir)
	assert.FileExists(t, s.ProfilePath(cloudProfileName()))

	active, err := s.Active()
	require.NoError(t, err)
	assert.Equal(t, cloudProfileName(), active)
}
