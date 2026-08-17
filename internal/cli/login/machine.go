// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"errors"
	"io"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// A sign-in is the one command a program cannot drive as a single call: the user
// has to be shown a URL and then act on it, so the flow has a middle. This file
// is the protocol for that middle.
//
// The stream is: zero or one `started` document, then exactly one of a `complete`
// document or a failure envelope, on the same stdout, with the exit status saying
// which to expect. Zero `started` documents is a session that was already open —
// nothing for the user to do — and it is why a consumer must not require one.
//
// A refusal *after* the started document is the ordinary failure rather than an
// impossible state: the URL is handed out before the flow is waited on, and
// waiting is where a timeout, a rejected exchange, an invalid id_token and a
// failed session write all land. A consumer built on "no partial output before a
// failure" would break on the most common way a sign-in fails.
//
// No free text crosses. The messages a person reads are built from an auth
// plugin's error string, and a plugin is not a trusted source of prose; a
// consumer gets a code and the fields declared here, and renders its own text.

// loginSchemaVersion identifies the shape of both documents. A consumer reads it
// before any other field, so a document it cannot understand is an error rather
// than a guess.
const loginSchemaVersion = 1

// startedView is the "here is what to do next" document. Exactly one of the
// browser or device group is populated, by the method the plugin chose.
type startedView struct {
	SchemaVersion int    `json:"schemaVersion" yaml:"schemaVersion"`
	Phase         string `json:"phase" yaml:"phase"`
	Method        string `json:"method" yaml:"method"`

	BrowserURL string `json:"browserUrl,omitempty" yaml:"browserUrl,omitempty"`

	VerificationURI  string `json:"verificationUri,omitempty" yaml:"verificationUri,omitempty"`
	UserCode         string `json:"userCode,omitempty" yaml:"userCode,omitempty"`
	ExpiresInSeconds int    `json:"expiresInSeconds,omitempty" yaml:"expiresInSeconds,omitempty"`
}

// completeView is what a finished sign-in produced: who signed in, which
// profiles it wrote, and which one is now active.
type completeView struct {
	SchemaVersion int    `json:"schemaVersion" yaml:"schemaVersion"`
	Phase         string `json:"phase" yaml:"phase"`
	// Status tells a flow that was completed from one that was already open, so a
	// caller can drive setup twice without reporting a second sign-in.
	Status      string `json:"status" yaml:"status"`
	Subject     string `json:"subject,omitempty" yaml:"subject,omitempty"`
	SubjectName string `json:"subjectName,omitempty" yaml:"subjectName,omitempty"`

	Profiles profilesView `json:"profiles" yaml:"profiles"`
	// Active is the profile a caller's next request would use. Empty when the
	// sign-in wrote none and the store already had an answer.
	Active string `json:"active,omitempty" yaml:"active,omitempty"`
	// Warnings are the sync's own, about the user's own profiles. They are
	// surfaced because a sign-in that silently skipped a profile is exactly what
	// a caller has to be told; they carry no auth-block values.
	Warnings []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

// profilesView is what the sync did, by name rather than by count: a caller that
// has to name the profile it will use next cannot do it from a number.
type profilesView struct {
	Created []string `json:"created" yaml:"created"`
	Updated []string `json:"updated" yaml:"updated"`
	Renamed []string `json:"renamed" yaml:"renamed"`
	Removed []string `json:"removed" yaml:"removed"`
}

// emitter writes the documents a driven sign-in produces. The human path leaves
// it nil and prints prose instead.
//
// started is separate from complete because it has to reach the consumer *before*
// the flow is waited on, which is the whole reason the protocol has two
// documents. A single "render the outcome" hook could only be called once the
// outcome existed, by which time the URL is no use to anybody.
type emitter interface {
	started(*pkgauth.LoginStartResponse) error
	complete(completeView) error
}

// machineEmitter writes both documents to w in the given schema.
func machineEmitter(w io.Writer, schema string) emitter {
	return &docEmitter{w: w, schema: schema}
}

type docEmitter struct {
	w      io.Writer
	schema string
}

func (e *docEmitter) started(resp *pkgauth.LoginStartResponse) error {
	v := startedView{
		SchemaVersion: loginSchemaVersion,
		Phase:         "started",
		Method:        resp.Method,
	}
	if resp.Method == "device" {
		v.VerificationURI, v.UserCode = resp.VerificationURI, resp.UserCode
		v.ExpiresInSeconds = resp.ExpiresInSeconds
	} else {
		v.BrowserURL = resp.BrowserURL
	}
	return printer.NewMachineReadablePrinter[startedView](e.w, e.schema).Print(&v)
}

func (e *docEmitter) complete(v completeView) error {
	v.SchemaVersion, v.Phase = loginSchemaVersion, "complete"
	return printer.NewMachineReadablePrinter[completeView](e.w, e.schema).Print(&v)
}

// reportLogin renders err as a failure envelope, and reports whether it wrote
// one.
//
// It maps this package's own failures onto declared codes and everything else
// onto internal, so a consumer parses one protocol on every path. The auth
// plugin's own code is carried through in details, because it is the only thing
// that can say why a refusal happened and it is what a consumer branches on:
// not_logged_in and session_expired mean "sign in again", where
// issuer_unreachable and unsupported do not.
//
// The producer's message is deliberately not the consumer's. It is passed for a
// human reading their own terminal; a program reads the code.
func reportLogin(w io.Writer, schema string, err error) (bool, error) {
	return printer.PrintFailure(w, schema, asDeclaredFailure(err))
}

// asDeclaredFailure gives err a code, if it does not already carry one.
func asDeclaredFailure(err error) error {
	var already *printer.Failure
	if errors.As(err, &already) {
		return err
	}

	var ae *AuthError
	if errors.As(err, &ae) {
		var details map[string]any
		if ae.Code != "" {
			details = map[string]any{"pluginCode": ae.Code}
		}
		return printer.Fail(printer.CodeAuthFailed, ae.Message, details)
	}

	var se *SyncIncompleteError
	if errors.As(err, &se) {
		return printer.Fail(printer.CodeSyncIncomplete, se.Error(), nil)
	}

	var pe *pluginMissingError
	if errors.As(err, &pe) {
		return printer.Fail(printer.CodePluginMissing, pe.Error(),
			map[string]any{"plugin": pe.Plugin, "install": pe.Install})
	}

	return printer.Fail(printer.CodeInternal, err.Error(), nil)
}
