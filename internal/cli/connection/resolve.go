// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package connection resolves the connection a formae command would use, and
// the credential that reaches it, from one evaluation of one profile.
//
// One command producing both is the point. Resolving them through two
// independently timed reads cannot produce one snapshot: between the two the
// active pointer can move, the profile can be rewritten, or the auth block can
// change, and a request would then carry an endpoint from one revision and a
// credential from another — for hosted, one installation's endpoint with
// another's credential.
package connection

import (
	"errors"
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/cli/login"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/configview"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// schemaVersion identifies the shape of the resolved view. A consumer reads it
// before any other field.
const schemaVersion = 1

// credentialProvider is the auth plugin, narrowed to the one call this needs.
type credentialProvider interface {
	GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error)
}

// input is everything resolution decides from. It is passed rather than read so
// the decision is testable without a config file, a plugin, or a store.
type input struct {
	// Conn is the connection the profile resolved to.
	Conn pkgmodel.Connection
	// Profile is the effective profile name — the one actually used, not the
	// one asked for.
	Profile string
	// Explicit records that the caller named the profile, which is what makes
	// the choice unambiguous however many profiles exist.
	Explicit bool
	// Profiles is every profile name, used only to decide ambiguity.
	Profiles []string
	// Creds drives the auth plugin. Only reached for a hosted connection that
	// has passed the issuer gate.
	Creds credentialProvider
	// ForceRefresh skips the plugin's freshness check.
	ForceRefresh bool
	// CloudFlag and IssuerFlag override the platform, as a pair.
	CloudFlag, IssuerFlag string
}

// view is what a consumer parses.
type view struct {
	SchemaVersion int            `json:"schemaVersion" yaml:"schemaVersion"`
	Profile       string         `json:"profile" yaml:"profile"`
	Connection    map[string]any `json:"connection" yaml:"connection"`
	// Credential is absent for classic: the CLI sends none to a self-hosted
	// agent, and an empty key would invite one.
	Credential string `json:"credential,omitempty" yaml:"credential,omitempty"`
}

// resolve produces the view, or a declared failure a consumer can act on.
//
// The order is deliberate. Ambiguity and issuer trust are settled from
// configuration alone, before the auth plugin is driven: no credential is
// minted before the user has chosen which installation they meant, and none is
// requested from an issuer a profile named but this build does not trust.
func resolve(in input) (view, error) {
	switch conn := in.Conn.(type) {
	case *pkgmodel.ClassicConnection:
		return view{
			SchemaVersion: schemaVersion,
			Profile:       in.Profile,
			Connection:    configview.ConnectionView(conn),
		}, nil

	case *pkgmodel.HostedConnection:
		if err := checkUnambiguous(in); err != nil {
			return view{}, err
		}
		validated, err := login.ValidateHosted(conn, in.CloudFlag, in.IssuerFlag)
		if err != nil {
			return view{}, printer.Fail(printer.CodeUntrustedIssuer, err.Error(), nil)
		}
		credential, err := validated.Credential(in.Creds, in.ForceRefresh)
		if err != nil {
			return view{}, authFailure(err)
		}
		return view{
			SchemaVersion: schemaVersion,
			Profile:       in.Profile,
			Connection:    configview.ConnectionView(conn),
			Credential:    credential,
		}, nil

	default:
		return view{}, printer.Fail(printer.CodeNoConnection,
			fmt.Sprintf("profile %q resolves no connection this formae can use", in.Profile), nil)
	}
}

// checkUnambiguous refuses a hosted connection the caller did not name when
// more than one profile could have been meant.
//
// Narrow on purpose: classic never prompts, and a single profile is
// unambiguous whatever its mode. Deciding it here keeps configuration a single
// read — a consumer needs no second command to count profiles.
func checkUnambiguous(in input) error {
	if in.Explicit || len(in.Profiles) <= 1 {
		return nil
	}
	return printer.Fail(printer.CodeAmbiguousProfile,
		"more than one profile exists and none was named, so formae cannot tell which installation you meant",
		map[string]any{"candidates": in.Profiles, "active": in.Profile})
}

// authFailure turns a refusal from the auth plugin into a declared failure,
// carrying the plugin's own code because it is the only thing that can say why.
// A hosted connection that cannot be authenticated is not a usable connection,
// so this fails rather than reporting a clean endpoint beside a broken
// credential and deferring the failure into an opaque response.
func authFailure(err error) error {
	var ae *login.AuthError
	if errors.As(err, &ae) {
		var details map[string]any
		if ae.Code != "" {
			details = map[string]any{"pluginCode": ae.Code}
		}
		// The plugin's code is what a consumer acts on; its prose stays out of the
		// envelope, since it is text we did not write.
		return printer.Fail(printer.CodeAuthFailed, "the auth plugin could not produce a credential", details)
	}
	return printer.Fail(printer.CodeAuthFailed, "the auth plugin could not produce a credential", nil)
}
