// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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

// loginShellTimeout bounds the login-shell probe below. The probe runs the
// user's shell rc, which is arbitrary code: a slow prompt framework must cost
// a bounded wait, not a hung command.
const loginShellTimeout = 10 * time.Second

// The marker wrapping the probe's answer. A login shell prints banners, motd
// and whatever else the user's rc decides to say, so the answer has to be
// findable rather than assumed to be the whole of stdout.
const (
	gcloudMarkBegin = "__formae_gcloud_begin__"
	gcloudMarkEnd   = "__formae_gcloud_end__"
)

// errGcloudNotFound reports that no gcloud could be found by any route.
var errGcloudNotFound = errors.New("gcloud not found")

// resolveGcloud locates the gcloud executable.
//
// Three routes, cheapest first. PATH answers for nearly everyone: every
// packaged install (deb, rpm, snap, Homebrew) puts gcloud there, and every
// Google-authored tool that shells out to gcloud looks nowhere else.
//
// The login-shell probe exists for one case PATH cannot answer. A long-running
// process inherits its environment once, at start, so a gcloud installed
// afterwards is invisible to it and to every child it spawns - including this
// CLI when an agent runs it. Asking the user's own login shell is how editors
// escape the same inheritance problem.
//
// It runs only after PATH has already failed, which is what keeps it safe:
// resolving eagerly and caching, as those editors do, reintroduces exactly the
// staleness it was meant to fix.
//
// Deliberately no list of well-known install locations. The two such lists in
// the wild are both already out of date - neither knows /opt/google-cloud-cli,
// which is where a current Linux install puts it - and a hardcoded list is a
// standing promise to keep chasing someone else's packaging.
func resolveGcloud(ctx context.Context) (string, error) {
	// gcloud's own convention for saying where the SDK lives, so an operator
	// with an unusual layout has a fixed contract rather than a guess.
	if root := os.Getenv("CLOUDSDK_ROOT_DIR"); root != "" {
		if candidate := filepath.Join(root, "bin", gcloudBinary); isExecutableFile(candidate) {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath(gcloudBinary); err == nil {
		return path, nil
	}
	// Validated here rather than only inside the probe: the answer comes from
	// the user's shell, and the check belongs where the value is used rather
	// than where it happens to be produced.
	if path := gcloudFromLoginShell(ctx); isExecutableFile(path) {
		return path, nil
	}
	return "", errGcloudNotFound
}

// gcloudFromLoginShell asks the user's login shell where gcloud is, returning
// "" when it cannot say.
//
// The shell is interactive as well as login (-ilc) because a PATH edit is as
// likely to live in an interactive rc as in a profile: gcloud's own installer
// appends to .bashrc. Every failure is silent by design - this is a fallback,
// and its inability to answer is not itself an error worth reporting.
var gcloudFromLoginShell = func(ctx context.Context) string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, loginShellTimeout)
	defer cancel()

	script := fmt.Sprintf("printf '%%s' \"%s$(command -v %s)%s\"",
		gcloudMarkBegin, gcloudBinary, gcloudMarkEnd)
	cmd := exec.CommandContext(ctx, shell, "-ilc", script)
	// The timeout has to bind the shell's children too, not just the shell.
	// An rc that backgrounds anything leaves that child holding the stdout
	// pipe, and Output waits on the pipe rather than on the process, so
	// killing only the shell would let a "bounded" probe run indefinitely.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	// And a backstop for anything that survives the signal: after cancellation
	// Wait stops waiting on inherited pipes rather than blocking on them.
	cmd.WaitDelay = time.Second

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return between(string(out), gcloudMarkBegin, gcloudMarkEnd)
}

// between returns what sits between the first begin and the following end.
func between(s, begin, end string) string {
	i := strings.Index(s, begin)
	if i < 0 {
		return ""
	}
	rest := s[i+len(begin):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

// isExecutableFile reports whether path is a regular file this process may
// execute. The probe's answer comes from the user's shell, so it is checked
// rather than trusted.
func isExecutableFile(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// runGcloudLogin runs the interactive Application Default Credentials login.
// A variable so tests observe invocations without spawning anything.
var runGcloudLogin = func(ctx context.Context, out io.Writer) error {
	path, err := resolveGcloud(ctx)
	if err != nil {
		// Says what happens next, because "install gcloud" on its own reads as
		// the first of an unknown number of steps. There is exactly one more:
		// re-run, and formae does the sign-in. Nobody needs to run the login
		// by hand.
		//
		// The restart hint is not padding. A gcloud installed after this
		// process started is not on the PATH this process inherited, so the
		// re-run finds nothing and repeats the same message, which reads as
		// the install having failed.
		// Leads with the credential problem rather than with a missing binary,
		// following the shape Terraform and Pulumi use: someone reading "not on
		// PATH" goes hunting for a binary, when what they need to know is that
		// formae could not sign them in and how to let it.
		//
		// It says what happens next, because "install gcloud" on its own reads
		// as the first of an unknown number of steps and the obvious guess is
		// that logging in is another one. There is exactly one more step, and
		// it is running this again.
		return printer.Fail(printer.CodeGcloudMissing,
			"no usable Google Cloud credentials, and formae could not sign you in because the gcloud CLI "+
				"is not available. Install it from https://cloud.google.com/sdk/docs/install, then run this "+
				"again and formae will sign you in - there is no need to run the login yourself",
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
