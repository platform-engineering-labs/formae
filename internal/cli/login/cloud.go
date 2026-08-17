// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/util"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	"github.com/platform-engineering-labs/formae/pkg/plugin/discovery"
)

// Signing in to the hosted platform has to work on a machine that has never
// been configured, which is the state a new user is in. Everything in this file
// exists because the ordinary login path cannot reach that state: it resolves a
// profile before it authenticates, and resolving a profile on a clean machine
// creates one.
//
// What is synthesised is an **auth block** — an auth plugin's configuration —
// and never a hosted connection. The distinction is the whole reason this works:
// a hosted connection requires the installation it addresses, and no
// installation is known until the caller's grants have been enumerated, which
// happens after sign-in. An earlier design tried to fabricate the connection and
// could not.
//
// Nothing here is written to disk. The profiles come afterwards, from the sync,
// which is the one place that creates them.

// defaultCloudPluginDir is where an auth plugin is looked for when there is no
// profile to name a directory. It is the schema's own default for pluginDir
// (see Config.pkl) and the same literal internal/cli/app falls back to for a
// nil config, so a sign-in with no profile searches exactly where a sign-in
// with a default profile would.
const defaultCloudPluginDir = "~/.pel/formae/plugins"

// cloudAuthBlock builds the auth-plugin configuration a hosted sign-in drives,
// for an issuer that has already been resolved and canonicalised.
//
// It returns both forms because both are needed and they must not diverge: the
// raw JSON is handed to the plugin verbatim, and the typed block is what the
// gate checks and what a generated profile is rendered from.
//
// The role is the cliAuthRole constant rather than a parameter. There is one
// role a CLI sign-in can want, and a flag for it would only be a way to get it
// wrong. The client id and scopes are the same constants a generated profile
// carries, so the credential this mints is one the profile it produces can
// refresh; a sign-in that requested different scopes would leave the user
// authenticated now and unable to renew later.
func cloudAuthBlock(issuer string) (json.RawMessage, cliAuthBlock, error) {
	canonical, err := canonicalOrigin(issuer)
	if err != nil {
		// Reported rather than defaulted. Falling back to the built-in issuer
		// for an override the caller actually specified would mint a credential
		// for a platform they did not ask for.
		return nil, cliAuthBlock{}, fmt.Errorf("cloud issuer: %w", err)
	}

	block := cliAuthBlock{
		Type:     oidcAuthType,
		Role:     cliAuthRole,
		Issuer:   canonical,
		ClientID: defaultOidcClientID,
		Scopes:   defaultOidcScopes,
	}

	raw, err := json.Marshal(block)
	if err != nil {
		return nil, cliAuthBlock{}, fmt.Errorf("build the hosted auth configuration: %w", err)
	}
	return raw, block, nil
}

// gateSynthesised admits a block this package built itself, for the platform it
// was built from.
//
// It is the configuration half of the gate, as gateProfile is for a block read
// off a profile, and it exists as a separate function rather than as a flag on
// that one because the two answer the same question from different evidence.
// gateProfile decodes an opaque block a user or a model may have written and
// checks four things about it; here the type and the role are constants this
// file wrote, and there is no connection to classify.
//
// What is *not* assumed is the issuer. It is compared against the platform's
// exactly as gateProfile compares a profile's, so this stays a check rather than
// becoming a formality that trusts its caller. The credential half is not here
// at all: every path reaches gateCredential, which is what stops a synthesised
// block from skipping the one condition that is only knowable at runtime.
func gateSynthesised(block cliAuthBlock, p platform) gateResult {
	if block.Type != oidcAuthType || block.Role != cliAuthRole {
		return refuse(fmt.Sprintf(
			"this sign-in's auth configuration does not name the %s plugin in the %s role", oidcAuthType, cliAuthRole))
	}

	issuer, err := canonicalOrigin(block.Issuer)
	if err != nil {
		return refuse(fmt.Sprintf(
			"this sign-in's auth configuration does not name a usable issuer origin, "+
				"so formae cannot tell whether its credential would be issued for %s", p.Origin))
	}
	if issuer != p.Issuer {
		return refuse(fmt.Sprintf(
			"this sign-in's auth configuration names an issuer other than %s, so the credential it would "+
				"produce was not issued for %s", p.Issuer, p.Origin))
	}

	return gateResult{Auth: block, OK: true}
}

// cloudLogin is everything a profile-independent sign-in needs from the command.
// It is a struct for the same reason syncStep is: several of these are seams a
// test substitutes, and threading them positionally through two calls would make
// the argument list the documentation.
type cloudLogin struct {
	Cloud     string // --cloud, empty when not given
	Issuer    string // --cloud-issuer, empty when not given
	Device    bool
	PluginDir string

	ConfigDir func() (string, error)
	NewClient func(origin string) CloudClient
	Verifier  profileVerifier
	Out       io.Writer
	Theme     *theme.Theme

	// NewPlugin is the auth-plugin factory, injectable so a test can drive a
	// whole sign-in without a plugin subprocess on disk.
	NewPlugin func(authType string, raw json.RawMessage, pluginDir string) (*pkgauth.Client, error)
}

// runCloudLoginAndSync signs in to the hosted platform without reading or
// writing a profile, then hands the credential straight to the sync.
//
// The order is the same as the profile path's — authenticate, then enumerate,
// then write profiles — and it is deliberately not shortened. What differs is
// only where the auth configuration comes from.
func runCloudLoginAndSync(ctx context.Context, c cloudLogin) error {
	p, err := resolvePlatform(c.Cloud, c.Issuer)
	if err != nil {
		return err
	}

	raw, block, err := cloudAuthBlock(p.Issuer)
	if err != nil {
		return err
	}

	// The gate runs before the plugin is started, exactly as it does for a
	// profile's block: driving an auth plugin is what sends a request to an
	// issuer, so a block that would not pass has to be refused first.
	if g := gateSynthesised(block, p); !g.OK {
		return errors.New(g.Reason)
	}

	client, err := c.NewPlugin(block.Type, raw, c.PluginDir)
	if err != nil {
		return err
	}

	return runLoginAndSync(ctx, client, syncStep{
		Creds:      client,
		Entry:      syncFromFlags(block),
		ConfigDir:  c.ConfigDir,
		NewClient:  c.NewClient,
		Verifier:   c.Verifier,
		Out:        c.Out,
		Theme:      c.Theme,
		CloudFlag:  c.Cloud,
		IssuerFlag: c.Issuer,
	}, c.Device)
}

// authPluginFor discovers the named auth plugin and starts it against raw.
//
// pluginDir is the configured directory to search first; the second is derived
// from the running binary's own location. Both paths through this package use
// it, so where an auth plugin is found is decided in one place rather than
// twice — internal/cli/app.App.AuthClient searches the same two directories in
// the same order, and a second copy of that rule would be free to drift.
//
// A plugin that is not installed is reported with the command that installs it.
// That is not defensive: the release which introduces hosted sign-in is also the
// one that adds the auth plugin to the standard bundle, so a user who upgraded
// formae by name has a bundle without it, and "not installed" with no remedy is
// a dead end on the very path that is supposed to need no setup.
//
// No part of raw reaches the error. It is the configuration that may hold a
// credential for another system, and a refusal has no reason to repeat it.
func authPluginFor(authType string, raw json.RawMessage, pluginDir string) (*pkgauth.Client, error) {
	binPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("determine the running formae's location: %w", err)
	}

	dirs := []string{util.ExpandHomePath(pluginDir), discovery.SystemPluginDir(binPath)}
	for _, p := range discovery.DiscoverPluginsMulti(dirs, discovery.Auth) {
		if p.Name != authType {
			continue
		}
		client, err := pkgauth.NewClient(p.BinaryPath, raw)
		if err != nil {
			return nil, fmt.Errorf("start the %s auth plugin: %w", authType, err)
		}
		return client, nil
	}

	return nil, fmt.Errorf(
		"signing in to the hosted platform needs the %s auth plugin, which is not installed. "+
			"Install it with `pelmgr install %s`", authType, authType)
}
