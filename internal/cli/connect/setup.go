// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
	"github.com/platform-engineering-labs/formae/internal/cli/login"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// credentialProvider is the auth plugin, narrowed to the one call this needs.
type credentialProvider interface {
	GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error)
}

// lazyCreds builds the auth client only if a credential is actually wanted.
type lazyCreds struct {
	app    *app.App
	client credentialProvider
}

func (l *lazyCreds) GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error) {
	if l.client == nil {
		client, err := l.app.AuthClient()
		if err != nil {
			return nil, err
		}
		l.client = client
	}
	return l.client.GetAuthHeader(forceRefresh)
}

// The seams a contract test reaches the outside world through. Nothing but a
// test ever assigns them.
var (
	newCloudAPI    = cloudapi.NewClient
	newCredentials = func(a *app.App) credentialProvider { return &lazyCreds{app: a} }
)

// selection is the profile choice a run evaluated, for the messages that name
// it.
type selection struct {
	Path string
	Name string
}

// resolveHosted selects the profile without bootstrapping and requires it to
// be hosted: a bare machine gets hosted_required, not a manufactured classic
// default (the property the login clean-machine tests hold; held here by
// never calling store.Resolve on the empty path).
func resolveHosted(configFlag, profileFlag string) (*pkgmodel.HostedConnection, *app.App, selection, error) {
	sel, err := selectWithoutBootstrap(configFlag, profileFlag)
	if err != nil {
		return nil, nil, selection{}, err
	}

	a := &app.App{}
	if err := a.LoadConfig(sel.Path, ""); err != nil {
		return nil, nil, selection{}, err
	}

	conn, ok := a.Config.Cli.Connection.(*pkgmodel.HostedConnection)
	if !ok || conn == nil {
		return nil, nil, selection{}, printer.Fail(printer.CodeHostedRequired,
			"this profile does not use a hosted connection; only a hosted installation can be connected to a cloud account. "+
				"Sign in with `formae login --hosted` first", nil)
	}
	return conn, a, sel, nil
}

// selectWithoutBootstrap mirrors the connection command's selection for the
// explicit cases; the default case reads the store as it stands and never
// manufactures a profile deciding the question it is asking.
func selectWithoutBootstrap(configFlag, profileFlag string) (selection, error) {
	root, err := store.ResolveConfigDir()
	if err != nil {
		return selection{}, err
	}
	s := store.New(root)

	switch {
	case profileFlag != "":
		if err := store.ValidateName(profileFlag); err != nil {
			return selection{}, err
		}
		path := s.ProfilePath(profileFlag)
		if _, err := os.Stat(path); err != nil {
			return selection{}, fmt.Errorf("%w: %s", store.ErrNotFound, profileFlag)
		}
		return selection{Path: path, Name: profileFlag}, nil

	case configFlag != "":
		return selection{Path: configFlag}, nil

	default:
		names, err := s.List()
		if err != nil {
			return selection{}, err
		}
		if len(names) == 0 {
			return selection{}, printer.Fail(printer.CodeHostedRequired,
				"no profile exists on this machine; sign in with `formae login --hosted` before connecting a cloud account", nil)
		}
		active, err := s.Active()
		if err != nil {
			return selection{}, err
		}
		if !slices.Contains(names, active) {
			return selection{}, fmt.Errorf("%w: %s", store.ErrNotFound, active)
		}
		path := s.ProfilePath(active)
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		return selection{Path: path, Name: name}, nil
	}
}

// cpContext is an authenticated handle to the control plane: a profile has
// been resolved, the login-issuer gate has passed, and a credential has been
// force-refreshed. It touches nothing AWS-side, so a read that only lists or
// looks up state can build on it without inheriting the provisioning paths'
// template and issuer pin.
type cpContext struct {
	Client         cloudapi.Client
	Bearer         string
	InstallationID string

	// Validated and Creds are not optional: openSession's registration path
	// force-refreshes the credential a second time before registering
	// (boundary two), and both are needed to do that without authenticating
	// again from scratch.
	Validated login.ValidatedHosted
	Creds     credentialProvider
}

// openControlPlane resolves the profile, requires it to be hosted, holds the
// bearer to the login-issuer gate, and force-refreshes the credential
// (boundary one). Nothing here depends on FORMAE_CONNECT_*: that pair governs
// only the AWS-side trust artifacts, which a control-plane read never
// touches.
func openControlPlane(ctx context.Context, opts options) (*cpContext, error) {
	conn, a, _, err := resolveHosted(opts.ConfigFlag, opts.ProfileFlag)
	if err != nil {
		return nil, err
	}

	// The login-issuer gate, untouched: the bearer goes to the login trust
	// domain, and no credential is minted for a profile nothing checked.
	validated, err := login.ValidateHosted(conn, "", "")
	if err != nil {
		return nil, printer.Fail(printer.CodeUntrustedIssuer, err.Error(), nil)
	}

	creds := newCredentials(a)
	bearer, err := validated.Credential(creds, true) // boundary 1: before setup
	if err != nil {
		return nil, authFailure(err)
	}

	// The control-plane origin comes from the login platform pair: that is
	// where the bearer goes; FORMAE_CONNECT_* governs only the AWS-side
	// issuer/template pin.
	origin, _, err := cloudapi.ResolvePlatform("", "")
	if err != nil {
		return nil, err
	}
	client := newCloudAPI(origin)

	return &cpContext{
		Client:         client,
		Bearer:         bearer,
		InstallationID: conn.Installation,
		Validated:      validated,
		Creds:          creds,
	}, nil
}

// session is an authenticated connect run whose coordinates have been read
// and whose issuer has passed the pin. Everything path-specific happens on
// top of one of these.
type session struct {
	InstallationID string
	Setup          cloudapi.CloudConnectionSetup
	Platform       connectPlatform
	Warnings       []string

	client    cloudapi.Client
	validated login.ValidatedHosted
	creds     credentialProvider
}

// openSession resolves the profile, force-refreshes the credential (boundary
// one), reads the setup coordinates, and holds them against the issuer pin.
func openSession(ctx context.Context, opts options) (*session, error) {
	cp, err := openControlPlane(ctx, opts)
	if err != nil {
		return nil, err
	}

	p, err := resolveConnectPlatform()
	if err != nil {
		return nil, err
	}

	setup, err := cp.Client.GetCloudConnectionSetup(ctx, cp.Bearer, cp.InstallationID)
	if err != nil {
		return nil, classifySetupError(ctx, cp.Client, cp.Bearer, cp.InstallationID, err)
	}

	// F6: the server's spelling is canonicalised before the comparison, so a
	// slash-terminated value cannot self-reject a paired override.
	issuer, err := cloudapi.CanonicalOrigin(setup.Issuer)
	if err != nil || issuer != p.Issuer {
		return nil, printer.Fail(printer.CodeUntrustedIssuer,
			"the control plane names an issuer this build does not trust for cloud connections", nil)
	}

	return &session{
		InstallationID: cp.InstallationID,
		Setup:          setup,
		Platform:       p,
		Warnings:       setup.Warnings,
		client:         cp.Client,
		validated:      cp.Validated,
		creds:          cp.Creds,
	}, nil
}

// classifySetupError maps a setup read failure onto the declared codes. A 404
// is ambiguous on its own — no grant and a control plane too old to carry the
// route answer identically — so the listing disambiguates, and only an
// authoritative listing may conclude anything about authorization.
func classifySetupError(ctx context.Context, client cloudapi.Client, bearer, installationID string, err error) error {
	var authErr *cloudapi.AuthError
	if errors.As(err, &authErr) {
		return printer.Fail(printer.CodeNotAuthorized,
			"the control plane refused this request; connecting a cloud account requires an admin of the installation", nil)
	}

	var notReady *cloudapi.NotReadyError
	if errors.As(err, &notReady) {
		var details map[string]any
		if notReady.State != "" {
			details = map[string]any{"state": notReady.State}
		}
		return printer.Fail(printer.CodeInstallationNotReady, notReady.Error(), details)
	}

	var notFound *cloudapi.NotFoundError
	if errors.As(err, &notFound) {
		snapshot, lerr := client.ListInstallations(ctx, bearer)
		if lerr != nil || !snapshot.Authoritative {
			// An incomplete listing licenses no claim about authorization.
			return fmt.Errorf("the control plane answered 404 for the cloud-connection setup, "+
				"and the installations listing could not settle whether this installation is visible to you; try again: %w", err)
		}
		for _, installation := range snapshot.Installations {
			if installation.InstallationID == installationID {
				return printer.Fail(printer.CodeControlPlaneTooOld,
					"this installation is visible to you, but its control plane predates cloud connections; upgrade it and re-run", nil)
			}
		}
		return printer.Fail(printer.CodeNotAuthorized,
			"this installation is not among the ones your grants cover; if you were granted admin recently, "+
				"run `formae login` to refresh your session and try again",
			map[string]any{"reason": "not_visible"})
	}

	var transient *cloudapi.TransientError
	if errors.As(err, &transient) {
		return fmt.Errorf("the control plane could not answer the cloud-connection setup request; try again: %w", err)
	}

	return err
}

// register declares the connection, minting a fresh credential first
// (boundary two): provisioning can outlive a token, and a stale bearer must
// never ride the registration.
func (s *session) register(ctx context.Context, account, roleArn string) (string, error) {
	bearer, err := s.validated.Credential(s.creds, true) // boundary 2: before registration
	if err != nil {
		return "", authFailure(err)
	}

	_, err = s.client.RegisterCloudConnection(ctx, bearer, s.InstallationID, cloudapi.CloudConnectionRegistration{
		Cloud:   "aws",
		Account: account,
		RoleArn: roleArn,
	})
	if err == nil {
		return statusRegisteredUnverified, nil
	}

	var conflict *cloudapi.ConflictError
	if !errors.As(err, &conflict) {
		return "", classifyRegisterError(err)
	}

	// 409: read the listing and compare. The same ARN is the idempotent
	// success; a different one is a conflict the user has to resolve.
	snapshot, lerr := s.client.ListCloudConnections(ctx, bearer, s.InstallationID)
	s.Warnings = append(s.Warnings, snapshot.Warnings...)
	if lerr != nil {
		return "", fmt.Errorf("a cloud connection for this account already exists, and the existing "+
			"registration could not be read to compare: %w", lerr)
	}
	for _, connection := range snapshot.Connections {
		if connection.Cloud != "aws" || connection.Account != account {
			continue
		}
		if connection.RoleArn == roleArn {
			return statusAlreadyRegistered, nil
		}
		return "", printer.Fail(printer.CodeRegistrationConflict,
			"a different role is already registered for this account on this installation",
			map[string]any{"registeredRoleArn": connection.RoleArn, "statedRoleArn": roleArn})
	}
	if !snapshot.Complete {
		return "", fmt.Errorf("a cloud connection for this account already exists, and the connections listing " +
			"used to compare it was incomplete, so the existing registration is not visible to compare")
	}
	return "", printer.Fail(printer.CodeRegistrationConflict,
		"the control plane refused this registration as a duplicate, but no connection for this account "+
			"appears in the installation's full listing",
		map[string]any{"statedRoleArn": roleArn})
}

// classifyRegisterError maps a registration failure onto the declared codes.
func classifyRegisterError(err error) error {
	var authErr *cloudapi.AuthError
	if errors.As(err, &authErr) {
		return printer.Fail(printer.CodeNotAuthorized,
			"the control plane refused the registration; connecting a cloud account requires an admin of the installation", nil)
	}
	var notFound *cloudapi.NotFoundError
	if errors.As(err, &notFound) {
		return printer.Fail(printer.CodeNotAuthorized,
			"the installation is no longer visible to you, so the registration was refused",
			map[string]any{"reason": "not_visible"})
	}
	return err
}

// authFailure turns a refusal from the auth plugin into a declared failure,
// carrying the plugin's own code because it is the only thing that can say
// why.
func authFailure(err error) error {
	var ae *login.AuthError
	if errors.As(err, &ae) {
		var details map[string]any
		if ae.Code != "" {
			details = map[string]any{"pluginCode": ae.Code}
		}
		return printer.Fail(printer.CodeAuthFailed, "the auth plugin could not produce a credential", details)
	}
	return printer.Fail(printer.CodeAuthFailed, "the auth plugin could not produce a credential", nil)
}

// multiInstallationWarning renders the hint entries naming this account on
// other installations into one warning.
func multiInstallationWarning(account string, elsewhere []cloudapi.ConnectedAccount) string {
	names := make([]string, 0, len(elsewhere))
	for _, entry := range elsewhere {
		name := entry.InstallationName
		if name == "" {
			name = entry.InstallationID
		} else {
			name = fmt.Sprintf("%s (%s)", name, entry.InstallationID)
		}
		names = append(names, name)
	}
	return fmt.Sprintf("account %s is already connected to %s; connecting it here too means more than one installation can manage it",
		account, strings.Join(names, ", "))
}
