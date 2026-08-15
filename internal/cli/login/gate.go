// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/platform-engineering-labs/formae/internal/cli/authmsg"
	"net/http"
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Profile sync takes the credential a sign-in just produced and sends it to
// the control plane to ask which installations the caller can reach. This is
// the gate that decides whether that request may be made at all, and it is the
// only thing standing between a credential and an origin it was not issued
// for.
//
// A connection being hosted says where formae talks to, not who issued what it
// talks with. The schema types a connection's auth block as any auth plugin's
// configuration, and core's base type declares exactly one field — type — so a
// hosted profile can perfectly well carry a basic-auth username and password,
// or an oidc block pointed at a customer's own issuer. Four conditions must
// therefore hold, and each closes a hole the others leave open:
//
//  1. the connection resolves to a hosted connection — a classic one addresses
//     someone's own agent, and its credential has nothing to do with us;
//  2. the auth block decodes into the oidc plugin's CLI configuration, with
//     type "oidc" and role "cli". Without this a basic-auth password would be
//     forwarded to our control plane, and then copied into a generated,
//     world-readable profile;
//  3. that block's issuer is the issuer of the platform we are about to talk
//     to. Type alone is not enough: the oidc plugin is generic, so a bearer
//     minted by someone else's issuer is the same leak one level down;
//  4. the credential is a Bearer token under the canonical Authorization key.
//
// Any of them failing means no request is made and nothing is written. The
// sign-in itself is unaffected — the user is signed in either way; only the
// sync does not run, and the notice says which condition stopped it.
//
// No refusal ever repeats a value from the auth block back to the user. The
// block that failed the gate is precisely the one that may hold a secret
// belonging to another system, so a refusal names the field or the condition
// and nothing else.

// cliAuthBlock is the oidc auth plugin's CLI config, reproduced here because
// the CLI renders it into generated profiles. The source of truth is
// formae-plugin-oidc's schema/Config.pkl, class CliConfig — a coupling no
// compiler checks: a change to that class is a change to this struct.
type cliAuthBlock struct {
	Type     string `json:"type"`
	Role     string `json:"role"`
	Issuer   string `json:"issuer"`
	ClientID string `json:"clientId"`
	Scopes   string `json:"scopes"`
}

// oidcAuthType and cliAuthRole are the two values that identify a block as the
// one this command understands. They are matched exactly: both are literals in
// the plugin's schema, so a difference in spelling or case is a block written
// against something other than that schema.
const (
	oidcAuthType = "oidc"
	cliAuthRole  = "cli"
)

// defaultOidcClientID and defaultOidcScopes are the values a generated profile
// carries when the block that licensed it named neither. They are applied when
// a profile is rendered, never here: filling them in at the gate would leave no
// way to tell a profile that named them from one that did not.
const (
	defaultOidcClientID = "formae-cli"
	defaultOidcScopes   = "openid profile email offline_access"
)

// errAuthBlockUndecodable is wrapped into every error decodeCliAuthBlock
// returns, so a caller can tell "this is not the block we understand" from any
// other failure without matching on message text.
var errAuthBlockUndecodable = errors.New("the auth block is not the oidc plugin's CLI configuration")

// decodeCliAuthBlock decodes a hosted connection's opaque auth block. The
// schema constrains nothing beyond `type`, so this is a decode and not a
// type check: a non-string clientId or a list-valued scopes fails here.
//
// That strictness is what earns the renderer the right to re-emit the block
// from typed fields instead of copying opaque JSON: every field it will write
// has been read as the string it is supposed to be. A field this struct does
// not name is ignored rather than refused, mirroring how a record from the
// control plane is decoded — an unfamiliar field is the plugin's config
// growing, not evidence that this is some other plugin's block.
//
// That tolerance is not unconditionally safe. Today CliConfig names nothing
// beyond who the token is for — where it was fetched from comes entirely from
// the issuer's own discovery document, so an unrecognised field cannot change
// that. But if CliConfig ever grows a field that does — a token- or
// discovery-endpoint override — a block could name our issuer while the
// plugin minted the bearer somewhere else entirely, and this decode would
// carry that field through unseen because it is not one gateSync inspects.
// Whoever adds such a field to the plugin schema must also teach the gate to
// look at it.
func decodeCliAuthBlock(raw json.RawMessage) (cliAuthBlock, error) {
	if len(raw) == 0 {
		return cliAuthBlock{}, fmt.Errorf("%w: the connection carries no auth block", errAuthBlockUndecodable)
	}

	// Decoded through a pointer so that a JSON null is told apart from an
	// object: unmarshalling null into a struct succeeds and leaves every field
	// zero, which would otherwise read as a block that merely names nothing.
	var block *cliAuthBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		// The decoder's own message is deliberately not repeated. encoding/json
		// quotes the offending input back for some targets, and this block is
		// the one that may hold a credential for another system. Only the field
		// name is reported, and that name comes from this struct's tags rather
		// than from the block.
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && typeErr.Field != "" {
			return cliAuthBlock{}, fmt.Errorf("%w: its %q field is not a string", errAuthBlockUndecodable, typeErr.Field)
		}
		return cliAuthBlock{}, fmt.Errorf("%w: it is not a JSON object of string fields", errAuthBlockUndecodable)
	}
	if block == nil {
		return cliAuthBlock{}, fmt.Errorf("%w: it is JSON null", errAuthBlockUndecodable)
	}

	return *block, nil
}

// bearerFrom returns the bearer credential from the canonical Authorization
// header, and whether one is present.
//
// The key is read canonically and only canonically, mirroring hasCredential in
// internal/cli/app for the same reason: http.Header.Get canonicalises the key
// it looks up but not the keys already stored in the map, so a credential a
// plugin returned under "authorization", or under another name entirely, is
// one this CLI could never transmit. That is the same failure as no credential
// at all and fails closed the same way, rather than being read as success by a
// scan that credits any value under any key.
//
// The scheme is matched case-insensitively because RFC 7235 defines auth
// schemes that way, and the value is returned exactly as it was stored, so the
// credential reaches the control plane byte for byte as the plugin produced
// it. A scheme with nothing behind it is not a credential: "Bearer" alone, or
// followed only by whitespace, is refused here rather than sent as an empty
// token for the control plane to reject.
func bearerFrom(h http.Header) (string, bool) {
	value := h.Get("Authorization")
	scheme, token, found := strings.Cut(value, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return value, true
}

// gateResult is the gate's decision. Auth and Bearer are populated only when
// OK is true, so nothing downstream can act on a block or a credential the
// gate did not clear.
type gateResult struct {
	Auth   cliAuthBlock // the validated block to render from
	Bearer string       // the credential, never logged or printed
	OK     bool         // false means no request is made and nothing is written
	Reason string       // why sync does not apply, for the notice
}

// String implements fmt.Stringer so that "never logged or printed" holds for
// Bearer by construction: any caller that formats a gateResult with %v or
// %+v, rather than reading Bearer directly, gets a redaction marker instead of
// the credential. Everything else useful for diagnosing a decision — whether
// it passed, why not, and the non-secret auth fields the gate validated —
// stays visible.
//
// The receiver is a value, not a pointer, because gateSync returns and every
// caller holds a gateResult by value; a pointer receiver would leave that
// value unformatted by this method.
func (g gateResult) String() string {
	bearer := ""
	if g.Bearer != "" {
		bearer = pkgmodel.RedactedForLog
	}
	return fmt.Sprintf("gateResult{Auth:%+v Bearer:%s OK:%v Reason:%q}", g.Auth, bearer, g.OK, g.Reason)
}

// gateSync reports whether profile sync may run against p.
//
// p is expected to come from resolvePlatform, which canonicalises both halves;
// the block's issuer is canonicalised here and the two are compared as origins
// so that https://Auth.Formae.AI:443 and https://auth.formae.ai are one issuer
// while nothing else is.
func gateSync(conn pkgmodel.Connection, p platform, hdr http.Header) gateResult {
	g := gateProfile(conn, p)
	if !g.OK {
		return g
	}
	return gateCredential(g, p, hdr)
}

// gateProfile decides everything that can be decided from configuration alone:
// that this is a hosted profile, that its auth block names the plugin and role
// we can re-render, and that its issuer is the platform's.
//
// Split out from the credential half so it can run *before* an auth plugin is
// invoked. A profile a model wrote controls the issuer, so driving the plugin
// first would send it at whatever token endpoint the profile named; refusing
// afterwards would be too late. The order is the protection.
func gateProfile(conn pkgmodel.Connection, p platform) gateResult {
	hostedConn, isHosted := conn.(*pkgmodel.HostedConnection)
	if !isHosted || hostedConn == nil {
		return refuse("this profile does not use a hosted connection, so its sign-in covers no hosted installations")
	}

	auth, err := decodeCliAuthBlock(hostedConn.Auth)
	if err != nil {
		return refuse(fmt.Sprintf(
			"%v, so formae cannot tell which platform its credential belongs to", err))
	}

	if auth.Type != oidcAuthType {
		return refuse(fmt.Sprintf(
			"this profile's auth block does not name the %s auth plugin in its type field, "+
				"so formae cannot tell which platform its credential belongs to", oidcAuthType))
	}

	// The role is required outright and never synthesised. The plugin ships an
	// agent config class and a CLI one, and its config reader errors on an
	// absent role, so a block that got this far with a working credential
	// carried role "cli" already. Inventing one for a block we could not
	// classify would manufacture the very claim the renderer then relies on.
	switch auth.Role {
	case cliAuthRole:
	case "":
		return refuse(fmt.Sprintf(
			"this profile's auth block sets no role, so formae cannot tell whether it is a %s configuration", cliAuthRole))
	default:
		return refuse(fmt.Sprintf(
			"this profile's auth block sets a role other than %q, so it is not one formae can re-render", cliAuthRole))
	}

	issuer, err := canonicalOrigin(auth.Issuer)
	if err != nil {
		return refuse(fmt.Sprintf(
			"this profile's auth block does not name a usable issuer origin, "+
				"so formae cannot tell whether its credential was issued for %s", p.Origin))
	}
	if issuer != p.Issuer {
		return refuse(fmt.Sprintf(
			"this profile's auth block names an issuer other than %s, so the credential it produced "+
				"was not issued for %s", p.Issuer, p.Origin))
	}

	return gateResult{Auth: auth, OK: true}
}

// gateCredential adds the credential the sign-in produced to an already-gated
// profile. It runs after the auth plugin, which is why it is not part of
// gateProfile: by this point the plugin has been driven, and the only question
// left is whether it handed back something we can actually send.
func gateCredential(g gateResult, p platform, hdr http.Header) gateResult {
	bearer, ok := bearerFrom(hdr)
	if !ok {
		return refuse(fmt.Sprintf(
			"this sign-in produced no Bearer credential under the canonical Authorization header, "+
				"so there is nothing to authenticate a request to %s with", p.Origin))
	}
	g.Bearer = bearer
	return g
}

// refuse returns the decision that stops sync, carrying the reason and
// nothing else: no block and no credential leave the gate when it refuses.
func refuse(reason string) gateResult {
	return gateResult{Reason: reason}
}

// ValidatedHosted is a hosted connection that has passed the issuer gate. Its
// fields are unexported and this package alone constructs it, so a credential
// cannot be minted for a connection nothing checked: the ordering is a property
// of the type rather than a rule every caller has to remember.
type ValidatedHosted struct {
	conn *pkgmodel.HostedConnection
	plat platform
}

// Connection returns the connection that was validated.
func (v ValidatedHosted) Connection() *pkgmodel.HostedConnection { return v.conn }

// ValidateHosted checks that conn is a hosted profile whose auth block names
// the platform this build trusts, reading configuration only. Callers must run
// it before minting a credential: a profile a model wrote controls the issuer,
// so driving the auth plugin first would send it at whatever token endpoint the
// profile named.
func ValidateHosted(conn pkgmodel.Connection, cloudFlag, issuerFlag string) (ValidatedHosted, error) {
	p, err := resolvePlatform(cloudFlag, issuerFlag)
	if err != nil {
		return ValidatedHosted{}, err
	}
	g := gateProfile(conn, p)
	if !g.OK {
		return ValidatedHosted{}, errors.New(g.Reason)
	}
	// gateProfile has already established the arm.
	return ValidatedHosted{conn: conn.(*pkgmodel.HostedConnection), plat: p}, nil
}

// AuthError is a refusal from the auth plugin, carrying the plugin's own code
// so a caller can report why rather than only that.
type AuthError struct {
	Code    string
	Message string
}

func (e *AuthError) Error() string { return e.Message }

// Credential drives the auth plugin and returns the credential to send, scheme
// included. It is a method on ValidatedHosted because minting for an unchecked
// connection must not be expressible.
//
// A response carrying nothing under the canonical Authorization header fails
// closed: the client attaches only that header, so anything else is a value
// this formae could never send, and reading it as success would defer the
// failure to an opaque rejection at the far end.
func (v ValidatedHosted) Credential(creds credentialProvider, forceRefresh bool) (string, error) {
	// The type is exported so callers can hold one, which means another package
	// can write ValidatedHosted{} even though it cannot fill the fields. Refuse
	// that here, before the provider is touched: a zero value has passed no
	// gate, and minting for it would drive the auth plugin at whatever the
	// caller had in mind rather than at an issuer we checked.
	if v.conn == nil {
		return "", errors.New("this connection has not been validated, so no credential may be minted for it")
	}
	resp, err := creds.GetAuthHeader(forceRefresh)
	if err != nil {
		return "", err
	}
	if resp.ErrorCode != "" || resp.Error != "" {
		return "", &AuthError{
			Code:    string(resp.ErrorCode),
			Message: authmsg.DescribeAuthError(resp.ErrorCode, resp.Error),
		}
	}
	g := gateCredential(gateResult{OK: true}, v.plat, http.Header(resp.Headers))
	if !g.OK {
		return "", &AuthError{Message: g.Reason}
	}
	return g.Bearer, nil
}
