// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// A hosted sign-in needs an auth-plugin configuration, and on a clean machine
// there is no profile to read one from. These tests pin what is synthesised
// instead: an auth block, never a hosted connection. The distinction is what
// keeps the schema's "a hosted connection cannot exist without the installation
// it addresses" invariant true — an installation is not known until grants are
// enumerated, which is after sign-in.

func TestCloudAuthBlock_NamesThePluginAndRole(t *testing.T) {
	raw, block, err := cloudAuthBlock("https://auth.example")
	require.NoError(t, err)

	assert.Equal(t, oidcAuthType, block.Type)
	assert.Equal(t, cliAuthRole, block.Role)
	assert.Equal(t, "https://auth.example", block.Issuer)

	// The raw form is what reaches the plugin, so it carries the same facts.
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, oidcAuthType, decoded["type"])
	assert.Equal(t, cliAuthRole, decoded["role"])
	assert.Equal(t, "https://auth.example", decoded["issuer"])
}

// The client id and scopes are the ones a generated profile carries, so the
// block a sign-in drives and the block a profile records are the same block.
// A sign-in that requested different scopes than the profile it produces would
// mint a credential the profile could not refresh.
func TestCloudAuthBlock_CarriesTheGeneratedProfileDefaults(t *testing.T) {
	_, block, err := cloudAuthBlock("https://auth.example")
	require.NoError(t, err)

	assert.Equal(t, defaultOidcClientID, block.ClientID)
	assert.Equal(t, defaultOidcScopes, block.Scopes)
}

// The synthesised block must satisfy the same issuer gate a profile's block
// does, against the platform it was built from. Anything less would mean the
// cloud path is trusted where the profile path is checked.
func TestCloudAuthBlock_PassesTheIssuerGate(t *testing.T) {
	p, err := resolvePlatform("https://cloud.example", "https://auth.example")
	require.NoError(t, err)

	_, block, err := cloudAuthBlock(p.Issuer)
	require.NoError(t, err)

	g := gateSynthesised(block, p)
	assert.True(t, g.OK, "reason: %s", g.Reason)
}

// A block whose issuer is not the platform's is refused. This cannot arise from
// cloudAuthBlock, which is handed the resolved issuer — it is pinned so the gate
// stays a check rather than becoming a formality that trusts its caller.
func TestGateSynthesised_RefusesAnIssuerThatIsNotThePlatforms(t *testing.T) {
	p, err := resolvePlatform("https://cloud.example", "https://auth.example")
	require.NoError(t, err)

	_, block, err := cloudAuthBlock("https://elsewhere.example")
	require.NoError(t, err)

	g := gateSynthesised(block, p)
	assert.False(t, g.OK)
	assert.NotContains(t, g.Reason, "elsewhere.example",
		"a refusal must not repeat a value from the block it refused")
}

// The credential half is not skipped for a synthesised block. Three of the
// profile gate's conditions are true by construction here (it is not a
// connection, the type and role are constants, the issuer came from the same
// resolvePlatform call) but the fourth is about what the plugin handed back and
// is knowable only at runtime.
func TestGateSynthesised_StillRequiresABearer(t *testing.T) {
	p, err := resolvePlatform("https://cloud.example", "https://auth.example")
	require.NoError(t, err)
	_, block, err := cloudAuthBlock(p.Issuer)
	require.NoError(t, err)

	g := gateCredential(gateSynthesised(block, p), p, nil)
	assert.False(t, g.OK)
	assert.Empty(t, g.Bearer)
}

// An issuer that is not a usable origin is an error rather than a fall back to
// the default. Signing in against a default the caller did not ask for is how a
// credential gets minted for the wrong platform.
func TestCloudAuthBlock_RefusesAnUnusableIssuer(t *testing.T) {
	for _, issuer := range []string{"", "not a url", "ftp://auth.example", "https://auth.example/path"} {
		_, _, err := cloudAuthBlock(issuer)
		assert.Error(t, err, "issuer %q", issuer)
	}
}

// A plugin that is not installed is the ordinary state during the release that
// introduces this path: the standard bundle gains the auth plugin, but a user who
// upgraded formae by name still has the bundle they had. The refusal therefore
// names the command that fixes it.
func TestAuthPluginFor_MissingPluginNamesTheRemedy(t *testing.T) {
	_, _, err := authPluginFor(oidcAuthType, json.RawMessage(`{"type":"oidc"}`), t.TempDir())
	require.Error(t, err)

	assert.Contains(t, err.Error(), oidcAuthType)
	assert.Contains(t, err.Error(), "pelmgr install oidc")
}

// The refusal names the plugin and the remedy, and nothing about the
// configuration it was handed: that block is the one that may carry a
// credential for another system.
func TestAuthPluginFor_MissingPluginRevealsNothingFromTheBlock(t *testing.T) {
	raw := json.RawMessage(`{"type":"oidc","clientSecret":"s3cr3t-do-not-print"}`)
	_, _, err := authPluginFor(oidcAuthType, raw, t.TempDir())
	require.Error(t, err)

	assert.NotContains(t, err.Error(), "s3cr3t-do-not-print")
	assert.NotContains(t, err.Error(), "clientSecret")
}

// The cloud path resolves its platform through the same pair rule every other
// caller uses: both halves together, or neither. A custom control plane paired
// with the default issuer is the state the gate exists to catch, so it must not
// be reachable by setting one flag.
func TestResolveCloudPlatform_RefusesAHalfSetPair(t *testing.T) {
	clearCloudEnv(t)

	_, err := resolvePlatform("https://cloud.example", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, errPlatformHalfSet)

	_, err = resolvePlatform("", "https://auth.example")
	require.Error(t, err)
	assert.ErrorIs(t, err, errPlatformHalfSet)
}

// With neither override the cloud path signs in against the built-in platform.
func TestResolveCloudPlatform_DefaultsToTheBuiltInPlatform(t *testing.T) {
	clearCloudEnv(t)

	p, err := resolvePlatform("", "")
	require.NoError(t, err)
	assert.Equal(t, DefaultCloudURL, p.Origin)
	assert.Equal(t, DefaultCloudIssuer, p.Issuer)
}

// clearCloudEnv removes both platform overrides for the duration of a test and
// restores them afterwards.
//
// os.Unsetenv is used rather than t.Setenv(k, "") because resolveHalf reads
// LookupEnv: a variable present but empty counts as set, which is deliberate —
// an override the environment actually specified is never silently ignored — and
// would make these tests assert against a set-but-empty override rather than an
// absent one.
func clearCloudEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"FORMAE_CLOUD_URL", "FORMAE_CLOUD_ISSUER"} {
		if old, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		}
		require.NoError(t, os.Unsetenv(k))
	}
}

// The sync half. A cloud sign-in has no connection at all, and the sync's entry
// used to be a connection type assertion — which a nil connection fails, so the
// user would have signed in successfully and received no profiles and no
// message. These pin that the entry decides applicability instead.

// A cloud sign-in reaches the sync and publishes a profile for the installation
// the grants cover. This is the behaviour the whole task exists for.
func TestCloudEntry_SyncsWithNoConnection(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	step := f.loginStep()
	_, block, err := cloudAuthBlock(testIssuer)
	require.NoError(t, err)
	step.Entry = syncFromFlags(block)

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), step, false))

	assert.FileExists(t, f.store.ProfilePath(cloudProfileName()))
}

// The credential condition is not skipped along with the three a synthesised
// block satisfies by construction. A plugin that reports success while handing
// back nothing sendable must stop the sync, not be carried forward as an
// unauthenticated request.
func TestCloudEntry_StillRefusesWithoutABearer(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	step := f.loginStep()
	_, block, err := cloudAuthBlock(testIssuer)
	require.NoError(t, err)
	step.Entry = syncFromFlags(block)
	step.Creds = &stubCredentials{resp: &pkgauth.GetAuthHeaderResponse{}}

	err = runLoginAndSync(context.Background(), signedIn(), step, false)

	// A hard error, not a notice: the sign-in worked, so the message says so,
	// and the sync did not complete. credential() fails closed on a header this
	// CLI could never transmit, before the gate's own bearer check is reached.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "you are signed in")
	assert.NoFileExists(t, f.store.ProfilePath(cloudProfileName()))
}

// A credential under a non-canonical header key is the same failure as none at
// all: http.Header.Get canonicalises what it looks up but not what is already
// stored, so this is a value the CLI could never transmit.
func TestCloudEntry_RefusesANonCanonicalHeaderKey(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	step := f.loginStep()
	_, block, err := cloudAuthBlock(testIssuer)
	require.NoError(t, err)
	step.Entry = syncFromFlags(block)
	step.Creds = &stubCredentials{resp: &pkgauth.GetAuthHeaderResponse{
		Headers: map[string][]string{"authorization": {"Bearer " + testToken}},
	}}

	err = runLoginAndSync(context.Background(), signedIn(), step, false)
	require.Error(t, err)
	assert.NoFileExists(t, f.store.ProfilePath(cloudProfileName()))
	assert.NotContains(t, err.Error(), testToken)
}

// cloudProfileName is the profile the fixture's single installation derives. It
// is computed rather than written out so the test tracks the naming rule instead
// of restating it.
func cloudProfileName() string {
	return deriveProfileName("acme", "default", "prod", installOne)
}

// The auth plugin is not asked for a credential until the configuration half has
// passed. Driving it is what sends a request to the issuer the block names, so a
// block that would be refused must be refused first.
func TestCloudEntry_GatesBeforeAskingForACredential(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	creds := bearerCredentials()
	step := f.loginStep()
	step.Creds = creds
	// An issuer that is not the platform's: the gate must stop this.
	_, block, err := cloudAuthBlock(testOtherIssuer)
	require.NoError(t, err)
	step.Entry = syncFromFlags(block)

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), step, false))

	assert.Zero(t, creds.calls, "the credential was requested for a block the gate refuses")
}

// No refusal on the cloud path repeats the credential.
func TestCloudEntry_RefusalsNeverCarryTheBearer(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	step := f.loginStep()
	_, block, err := cloudAuthBlock(testOtherIssuer)
	require.NoError(t, err)
	step.Entry = syncFromFlags(block)

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), step, false))
	assert.NotContains(t, f.out.String(), testToken)
}

// TestCloudAuthBlock_IsNotAConnection guards the distinction the design turns
// on. A hosted connection requires an installation, which is unknown before
// grants are enumerated; an auth block does not. If this ever starts producing
// something with an endpoint or an installation in it, the ordering promise
// (login, then installations, then profiles) has been broken.
func TestCloudAuthBlock_IsNotAConnection(t *testing.T) {
	raw, _, err := cloudAuthBlock("https://auth.example")
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	for _, forbidden := range []string{"installation", "endpoint", "url", "port", "mode"} {
		assert.NotContains(t, decoded, forbidden)
	}
	assert.False(t, strings.Contains(string(raw), "installation"))
}
