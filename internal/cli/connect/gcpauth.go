// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"golang.org/x/oauth2/google"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// credentialState is what a run knows about the local Google credentials
// before it touches the project.
//
// The distinction that matters is the last two: only a missing or unusable
// credential is something a fresh sign-in can fix. A principal that is
// authenticated but cannot read the project is not, and re-running the login
// there would return the same principal while overwriting credentials the
// operator configured deliberately.
type credentialState int

const (
	// credentialsUsable: a token was obtained.
	credentialsUsable credentialState = iota
	// credentialsMissing: no credential could be obtained at all. Absent,
	// expired, revoked, malformed — all the same remedy.
	credentialsMissing
)

// gcloudBinary is the executable the login path runs. A variable so tests can
// point it somewhere harmless; production is the real name resolved on PATH.
var gcloudBinary = "gcloud"

// findCredentials reports whether usable Application Default Credentials exist.
//
// It asks for a token rather than looking for the well-known file, because the
// file existing proves nothing: it may hold an expired or revoked grant, and
// the failure the operator cares about is "cannot authenticate", not "no file".
var findCredentials = func(ctx context.Context) (credentialState, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return credentialsMissing, nil //nolint:nilerr // the absence is the answer, not a failure
	}
	if creds.TokenSource == nil {
		return credentialsMissing, nil
	}
	if _, err := creds.TokenSource.Token(); err != nil {
		// A credential that cannot mint a token is as good as absent, and has
		// the same remedy.
		return credentialsMissing, nil //nolint:nilerr // as above
	}
	return credentialsUsable, nil
}

// runGcloudLogin runs the interactive Application Default Credentials login.
// A variable so tests observe invocations without spawning anything.
var runGcloudLogin = func(ctx context.Context, out io.Writer) error {
	path, err := exec.LookPath(gcloudBinary)
	if err != nil {
		return printer.Fail(printer.CodeGcloudMissing,
			"the gcloud CLI is required to sign in to Google Cloud and is not on PATH; "+
				"install it from https://cloud.google.com/sdk/docs/install and re-run",
			map[string]any{"command": gcloudLoginCommand})
	}

	// Printed before it runs. formae is about to open a browser and ask for
	// consent on the operator's behalf; the least it can do is say exactly
	// what it is running.
	_, _ = fmt.Fprintf(out, "running %s\n", gcloudLoginCommand)

	cmd := exec.CommandContext(ctx, path, "auth", "application-default", "login")
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return printer.Fail(printer.CodeCredentialsRequired,
			"the Google Cloud sign-in did not complete",
			map[string]any{"command": gcloudLoginCommand})
	}
	return nil
}

const gcloudLoginCommand = "gcloud auth application-default login"

// ensureCredentials makes sure the run holds usable Google credentials,
// signing in for the operator when it may and refusing clearly when it may
// not.
//
// mayPrompt is false under --no-input and under machine output, where a
// browser popping open is not something a caller consented to. Those get the
// command to run instead, which is the one place this flow hands work back.
func ensureCredentials(ctx context.Context, out io.Writer, mayPrompt bool) error {
	state, err := findCredentials(ctx)
	if err != nil {
		return err
	}
	if state == credentialsUsable {
		return nil
	}
	if !mayPrompt {
		return printer.Fail(printer.CodeCredentialsRequired,
			"no usable Google Cloud credentials on this machine; run the sign-in and re-run this command",
			map[string]any{"command": gcloudLoginCommand})
	}
	if err := runGcloudLogin(ctx, out); err != nil {
		return err
	}

	// Confirm rather than assume: a login that exits zero without producing a
	// usable credential would otherwise fail much later, somewhere less
	// obvious.
	state, err = findCredentials(ctx)
	if err != nil {
		return err
	}
	if state != credentialsUsable {
		return printer.Fail(printer.CodeCredentialsRequired,
			"the sign-in completed but produced no usable credentials",
			map[string]any{"command": gcloudLoginCommand})
	}
	return nil
}
