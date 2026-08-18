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
	// CodeInternal: everything else. A command rendering machine output is
	// responsible for mapping errors it did not declare onto this, so a
	// consumer parses one protocol on every path; PrintFailure only reports
	// that it did not recognise them.
	CodeInternal Code = "internal"
)

// registeredCodes is what may reach the wire. A code absent from here is a
// mistake in this repo, not a contract a consumer should have to handle.
var registeredCodes = map[Code]bool{
	CodeAmbiguousProfile: true,
	CodeAuthFailed:       true,
	CodeUntrustedIssuer:  true,
	CodeNoConnection:     true,
	CodeInternal:         true,
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
