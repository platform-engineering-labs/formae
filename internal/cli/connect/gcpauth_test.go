// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// fakeGcloud writes a script standing in for gcloud and points the resolver at
// it. body is the shell after the shebang, so a test decides what the sign-in
// prints and whether it succeeds.
func fakeGcloud(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gcloud")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write fake gcloud: %v", err)
	}
	restore := gcloudBinary
	gcloudBinary = path
	t.Cleanup(func() { gcloudBinary = restore })
}

// A sign-in that cannot finish must hand back what gcloud said, because that is
// the only place the URL exists.
//
// On a machine that can open a browser gcloud completes over a loopback
// redirect and never reads stdin. Where it cannot - a container, an SSH
// session, a headless box - it falls back to printing a URL and waiting for a
// verification code typed back, and the CLI's stdin is not a terminal there. So
// the sign-in fails, and everything the operator needs in order to finish it by
// hand has already been printed. Dropping that leaves them with "did not
// complete" and no way to act on it.
func TestFailedSignInReportsWhatGcloudPrinted(t *testing.T) {
	fakeGcloud(t, `echo "Go to the following link in your browser:"
echo "    https://accounts.google.com/o/oauth2/auth?code_challenge=abc"
echo "gcloud crashed (EOFError): EOF when reading a line" >&2
exit 1`)

	err := runGcloudLogin(context.Background(), &bytes.Buffer{})
	if err == nil {
		t.Fatal("a sign-in that exited non-zero must fail")
	}

	var failure *printer.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("want a printer failure, got %T: %v", err, err)
	}
	if failure.Code != printer.CodeCredentialsRequired {
		t.Errorf("code = %q, want %q", failure.Code, printer.CodeCredentialsRequired)
	}
	if failure.Details["command"] != gcloudLoginCommand {
		t.Errorf("details lost the command: %#v", failure.Details)
	}

	output, _ := failure.Details["output"].(string)
	if !strings.Contains(output, "https://accounts.google.com/o/oauth2/auth") {
		t.Errorf("details must carry the sign-in URL, got %q", output)
	}
	// Both streams, because gcloud splits the instructions across them and
	// either half alone is not enough to act on.
	if !strings.Contains(output, "EOF when reading a line") {
		t.Errorf("details must carry gcloud's own diagnosis, got %q", output)
	}
}

// A sign-in that works carries no output detail. There is nothing to report and
// an empty key invites a reader to look for meaning in it.
func TestSuccessfulSignInReportsNothing(t *testing.T) {
	fakeGcloud(t, `echo "Credentials saved."`)

	if err := runGcloudLogin(context.Background(), &bytes.Buffer{}); err != nil {
		t.Fatalf("a sign-in that exited zero must succeed: %v", err)
	}
}

// What gcloud prints still reaches the operator's own stream as it happens.
// Capturing it for the failure detail must not silence the live run, or a human
// watching a terminal loses the URL they are supposed to open.
func TestSignInStillReportsLive(t *testing.T) {
	fakeGcloud(t, `echo "Go to https://accounts.google.com/o/oauth2/auth?x=1"
exit 1`)

	var out bytes.Buffer
	_ = runGcloudLogin(context.Background(), &out)

	if !strings.Contains(out.String(), "https://accounts.google.com/o/oauth2/auth") {
		t.Errorf("the live stream lost the URL: %q", out.String())
	}
	if !strings.Contains(out.String(), gcloudLoginCommand) {
		t.Errorf("the live stream must still name the command it ran: %q", out.String())
	}
}
