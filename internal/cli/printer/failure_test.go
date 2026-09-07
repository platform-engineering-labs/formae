// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package printer

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPrintFailureEmitsTheEnvelope(t *testing.T) {
	var out bytes.Buffer
	handled, err := PrintFailure(&out, "json", Fail(CodeAmbiguousProfile, "more than one profile", map[string]any{
		"candidates": []string{"a", "b"},
		"active":     "a",
	}))
	if err != nil {
		t.Fatalf("PrintFailure: %v", err)
	}
	if !handled {
		t.Fatal("a Failure must be handled")
	}

	var got struct {
		SchemaVersion int            `json:"schemaVersion"`
		Code          string         `json:"code"`
		Message       string         `json:"message"`
		Details       map[string]any `json:"details"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("envelope is not json: %v (%q)", err, out.String())
	}
	if got.SchemaVersion != 1 {
		t.Errorf("schemaVersion = %d, want 1", got.SchemaVersion)
	}
	if got.Code != "ambiguous_profile" {
		t.Errorf("code = %q", got.Code)
	}
	if got.Message != "more than one profile" {
		t.Errorf("message = %q", got.Message)
	}
	if got.Details["active"] != "a" {
		t.Errorf("details lost: %#v", got.Details)
	}
}

// Details are optional, and an absent one must not appear as a null key: a
// consumer reading the envelope should not have to tell null from missing.
func TestPrintFailureOmitsEmptyDetails(t *testing.T) {
	var out bytes.Buffer
	if _, err := PrintFailure(&out, "json", Fail(CodeAuthFailed, "plugin refused", nil)); err != nil {
		t.Fatalf("PrintFailure: %v", err)
	}
	if strings.Contains(out.String(), "details") {
		t.Fatalf("empty details should be omitted: %s", out.String())
	}
}

// The envelope is for failures this command declares. Anything else is someone
// else's error and must keep its ordinary handling rather than being dressed up
// with a code that means nothing.
func TestPrintFailureIgnoresAnOrdinaryError(t *testing.T) {
	var out bytes.Buffer
	handled, err := PrintFailure(&out, "json", errors.New("disk on fire"))
	if err != nil {
		t.Fatalf("PrintFailure: %v", err)
	}
	if handled {
		t.Fatal("an ordinary error must not be rendered as an envelope")
	}
	if out.Len() != 0 {
		t.Fatalf("nothing should have been written: %q", out.String())
	}
}

// The namespace is closed: a consumer branches on these codes, so one that is
// not in the set must never reach the wire. A code added without registering it
// degrades to internal rather than inventing a contract.
func TestPrintFailureClosesTheCodeNamespace(t *testing.T) {
	var out bytes.Buffer
	if _, err := PrintFailure(&out, "json", Fail(Code("invented_by_a_typo"), "nope", nil)); err != nil {
		t.Fatalf("PrintFailure: %v", err)
	}
	if !strings.Contains(out.String(), `"code":"internal"`) {
		t.Fatalf("unregistered code should degrade to internal: %s", out.String())
	}
}

func TestFailureIsAnError(t *testing.T) {
	var err error = Fail(CodeUntrustedIssuer, "issuer not trusted", nil)
	if !strings.Contains(err.Error(), "issuer not trusted") {
		t.Fatalf("Error() should carry the message: %q", err.Error())
	}
	var f *Failure
	if !errors.As(err, &f) || f.Code != CodeUntrustedIssuer {
		t.Fatalf("a Failure should be recoverable with errors.As: %#v", err)
	}
}

// Every code the connect stream declares is registered, so it reaches the
// wire as itself rather than degrading to internal.
func TestPrintFailureCarriesTheConnectCodes(t *testing.T) {
	for _, code := range []Code{
		CodeHostedRequired,
		CodeAccountMismatch,
		CodeSSOLoginRequired,
		CodeProvisionFailed,
		CodeRoleCollision,
		CodeProviderConflict,
		CodeRegistrationConflict,
		CodeNotAuthorized,
		CodeUnsupportedPartition,
		CodeControlPlaneTooOld,
		CodeInstallationNotReady,
	} {
		t.Run(string(code), func(t *testing.T) {
			var out bytes.Buffer
			if _, err := PrintFailure(&out, "json", Fail(code, "declared", nil)); err != nil {
				t.Fatalf("PrintFailure: %v", err)
			}
			if !strings.Contains(out.String(), `"code":"`+string(code)+`"`) {
				t.Fatalf("code %s did not survive the envelope: %s", code, out.String())
			}
		})
	}
}

func TestPrintFailureSupportsYAML(t *testing.T) {
	var out bytes.Buffer
	if _, err := PrintFailure(&out, "yaml", Fail(CodeNoConnection, "no connection", nil)); err != nil {
		t.Fatalf("PrintFailure: %v", err)
	}
	if !strings.Contains(out.String(), "code: no_connection") {
		t.Fatalf("yaml envelope: %s", out.String())
	}
}
