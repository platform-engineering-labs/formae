// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package printer

import (
	"errors"
	"io"
)

// failureSchemaVersion identifies the shape of the failure envelope. Consumers
// read it before any other field, so an envelope they cannot understand is an
// error rather than a guess.
const failureSchemaVersion = 1

// Code names a failure a machine consumer branches on. The namespace is closed:
// a consumer decides what to do next from this value, so it can only carry
// meanings we have agreed to keep.
type Code string

const (
	// CodeAmbiguousProfile: a hosted connection was resolved without a profile
	// being named, and more than one profile exists. The caller has to choose;
	// no credential is minted before they do.
	CodeAmbiguousProfile Code = "ambiguous_profile"
	// CodeAuthFailed: the auth plugin refused. Details carry the plugin's own
	// code, which is the only thing that can say why.
	CodeAuthFailed Code = "auth_failed"
	// CodeUntrustedIssuer: a hosted profile names an issuer we will not drive
	// an auth plugin against.
	CodeUntrustedIssuer Code = "untrusted_issuer"
	// CodeNoConnection: the profile resolved no connection we can use.
	CodeNoConnection Code = "no_connection"
	// CodeLoginFailed: a sign-in flow did not complete for a reason that is not
	// the auth plugin refusing — the flow was abandoned, or the identity it
	// returned could not be read.
	CodeLoginFailed Code = "login_failed"
	// CodePluginMissing: the auth plugin a sign-in needs is not installed. Its
	// own code because the remedy is specific and a consumer names it.
	CodePluginMissing Code = "plugin_missing"
	// CodeSyncIncomplete: the sign-in succeeded and the profiles that follow it
	// did not get written. Its own code because the two halves have opposite
	// remedies: the user is authenticated and must not be sent back through a
	// sign-in, which is what any code meaning "login failed" would cause.
	CodeSyncIncomplete Code = "sync_incomplete"
	// CodeInternal: everything else. A command rendering machine output is
	// responsible for mapping errors it did not declare onto this, so a
	// consumer parses one protocol on every path; PrintFailure only reports
	// that it did not recognise them.
	CodeInternal Code = "internal"
	// CodeHostedRequired: connect ran against a classic profile; only a hosted
	// installation can be connected to a cloud account.
	CodeHostedRequired Code = "hosted_required"
	// CodeAccountMismatch: the stated account is not the one the credentials
	// (or the role ARN) belong to. Refused before any IAM call.
	CodeAccountMismatch Code = "account_mismatch"
	// CodeSSOLoginRequired: the shared-config profile's SSO token is expired;
	// details carry the exact `aws sso login --profile <p>` command.
	CodeSSOLoginRequired Code = "sso_login_required"
	// CodeProvisionFailed: provisioning stopped partway; the message states
	// what stands, because re-running converges.
	CodeProvisionFailed Code = "provision_failed"
	// CodeRoleCollision: the role exists and is not provx-owned for this
	// subject; never treated as repairable drift.
	CodeRoleCollision Code = "role_collision"
	// CodeProviderConflict: the OIDC provider exists with an unexpected shape.
	CodeProviderConflict Code = "provider_conflict"
	// CodeRegistrationConflict: a different role ARN is already registered for
	// this account on this installation.
	CodeRegistrationConflict Code = "registration_conflict"
	// CodeNotAuthorized: the caller lacks the access this operation needs on
	// this installation. Provisioning uses it for a 403 meaning the caller is
	// not an admin; listing uses it both for a 403 meaning a member's tenant
	// grant excludes this installation, and for a 404 meaning the
	// installation is not visible to the caller at all. Terminal, not
	// retried.
	CodeNotAuthorized Code = "not_authorized"
	// CodeUnsupportedPartition: a non-commercial region, ARN, or STS caller.
	CodeUnsupportedPartition Code = "unsupported_partition"
	// CodeControlPlaneTooOld: the installation is listed but the setup
	// endpoint 404s, so the control plane predates connect.
	CodeControlPlaneTooOld Code = "control_plane_too_old"
	// CodeInstallationNotReady: the installation has not applied the
	// split-key template version yet, or is destroying.
	CodeInstallationNotReady Code = "installation_not_ready"
	// CodeGcloudMissing: the local path needs the gcloud CLI to obtain
	// credentials and it is not on PATH. Its own code because the remedy is a
	// specific install step and a consumer names it.
	CodeGcloudMissing Code = "gcloud_missing"
	// CodeCredentialsRequired: no usable Google credentials, in a run that may
	// not prompt (--no-input, or machine output). Details carry the exact
	// command to run.
	CodeCredentialsRequired Code = "credentials_required"
	// CodeProjectUnreachable: the stated project could not be read with these
	// credentials — it does not exist, or this principal cannot see it.
	// Deliberately distinct from a credential problem: signing in again
	// returns the same principal and would overwrite deliberately configured
	// credentials.
	CodeProjectUnreachable Code = "project_unreachable"
	// CodeApiDisabled: a Google API the connection needs is not enabled on the
	// project. Details name it, because the remedy is one command and formae
	// does not enable APIs on someone's project uninvited.
	CodeApiDisabled Code = "api_disabled"
)

// registeredCodes is what may reach the wire. A code absent from here is a
// mistake in this repo, not a contract a consumer should have to handle.
var registeredCodes = map[Code]bool{
	CodeAmbiguousProfile: true,
	CodeAuthFailed:       true,
	CodeUntrustedIssuer:  true,
	CodeNoConnection:     true,
	CodeLoginFailed:      true,
	CodePluginMissing:    true,
	CodeSyncIncomplete:   true,
	CodeInternal:         true,

	CodeHostedRequired:       true,
	CodeAccountMismatch:      true,
	CodeSSOLoginRequired:     true,
	CodeProvisionFailed:      true,
	CodeRoleCollision:        true,
	CodeProviderConflict:     true,
	CodeRegistrationConflict: true,
	CodeNotAuthorized:        true,
	CodeUnsupportedPartition: true,
	CodeControlPlaneTooOld:   true,
	CodeInstallationNotReady: true,

	CodeGcloudMissing:       true,
	CodeCredentialsRequired: true,
	CodeProjectUnreachable:  true,
	CodeApiDisabled:         true,
}

// Failure is an error a command declares, carrying a code a machine consumer
// can act on. Errors that are not Failures keep their ordinary handling: a code
// invented for them would mean nothing.
type Failure struct {
	Code    Code
	Message string
	Details map[string]any
}

func (f *Failure) Error() string { return f.Message }

// Fail builds a Failure. Details are optional and are omitted from the envelope
// entirely when absent, so a consumer never has to tell null from missing.
func Fail(code Code, message string, details map[string]any) *Failure {
	return &Failure{Code: code, Message: message, Details: details}
}

// failureView is the wire shape of a failure.
type failureView struct {
	SchemaVersion int            `json:"schemaVersion" yaml:"schemaVersion"`
	Code          Code           `json:"code" yaml:"code"`
	Message       string         `json:"message" yaml:"message"`
	Details       map[string]any `json:"details,omitempty" yaml:"details,omitempty"`
}

// PrintFailure writes err to w as a failure envelope when it is a Failure, and
// reports whether it did. A false return means the error is someone else's and
// the caller should handle it the way it always has.
//
// The envelope goes to the same stream as a success document, because it is the
// same protocol: a consumer parses one or the other and needs no second channel.
func PrintFailure(w io.Writer, format string, err error) (bool, error) {
	var f *Failure
	if !errors.As(err, &f) {
		return false, nil
	}

	code := f.Code
	if !registeredCodes[code] {
		// Never put an unagreed code on the wire; a consumer branching on it
		// would be branching on a typo.
		code = CodeInternal
	}

	view := failureView{
		SchemaVersion: failureSchemaVersion,
		Code:          code,
		Message:       f.Message,
		Details:       f.Details,
	}
	if perr := NewMachineReadablePrinter[failureView](w, format).Print(&view); perr != nil {
		return true, perr
	}
	return true, nil
}
