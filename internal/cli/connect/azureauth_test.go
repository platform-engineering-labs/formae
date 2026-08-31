// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// stubAzureCredentialState installs a fixed classification, so a test can
// drive every state without a real Azure environment.
func stubAzureCredentialState(t *testing.T, state azureCredentialState) {
	t.Helper()
	restore := usableCredentials
	usableCredentials = func(_ context.Context, _, _ string) (azureCredentialState, error) { return state, nil }
	t.Cleanup(func() { usableCredentials = restore })
}

// Azure reports the login command; it never spawns one. Without a tenant
// hint, the reported command must still be one the operator can actually
// run: a caller relaying this verbatim (the MCP tool's own contract) hands
// it straight to a shell, and a placeholder like "<id>" is not runnable.
func TestNoCredentialsReportsTheLoginCommand(t *testing.T) {
	stubLookPathAz(t, true)

	err := azureCredentialFailure(azureCredentialsNeedsAuthentication, "")

	require.Error(t, err)
	var f *printer.Failure
	require.ErrorAs(t, err, &f)
	assert.Equal(t, printer.CodeCredentialsRequired, f.Code)
	assert.Equal(t, "az login", f.Details["command"])
}

// A tenant hint the operator already gave travels into the reported command
// verbatim, so pasting it back in works the first time.
func TestAzureTenantHintNamesTheLoginCommand(t *testing.T) {
	stubLookPathAz(t, true)

	err := azureCredentialFailure(azureCredentialsNeedsAuthentication, "11111111-1111-1111-1111-111111111111")

	var f *printer.Failure
	require.ErrorAs(t, err, &f)
	assert.Equal(t, "az login --tenant 11111111-1111-1111-1111-111111111111", f.Details["command"])
}

// azLoginCommand's two branches, tested directly: a known tenant names it,
// and an unknown one omits the flag entirely rather than filling it with a
// placeholder. Both must be commands an operator can actually paste into a
// shell - never a string containing "<...>", which reads as a helpful hint
// but is a broken command.
func TestAzLoginCommand(t *testing.T) {
	withTenant := azLoginCommand("11111111-1111-1111-1111-111111111111")
	assert.Equal(t, "az login --tenant 11111111-1111-1111-1111-111111111111", withTenant)
	assert.NotContains(t, withTenant, "<")

	withoutTenant := azLoginCommand("")
	assert.Equal(t, "az login", withoutTenant)
	assert.NotContains(t, withoutTenant, "<", "a command containing a placeholder is not runnable")
	assert.NotContains(t, withoutTenant, ">")
}

// A permission or reachability failure is not a credential failure:
// re-authenticating returns the same principal and fixes nothing, so neither
// carries a login command.
func TestUnreachableSubscriptionIsNotACredentialFailure(t *testing.T) {
	for _, state := range []azureCredentialState{azureCredentialsLacksPermission, azureSubscriptionUnreachable} {
		err := azureCredentialFailure(state, "")

		var f *printer.Failure
		require.ErrorAs(t, err, &f)
		assert.NotEqual(t, printer.CodeCredentialsRequired, f.Code, "state %v must not report a login command", state)
		_, hasCommand := f.Details["command"]
		assert.False(t, hasCommand, "state %v must not carry a login command", state)
	}
}

// azureCredentialFailure is only ever meant to be called for a non-usable
// state; calling it for azureCredentialsUsable is a programmer error, and
// the function must say so rather than silently returning no error, which
// would read as success to a caller that only checks err != nil.
func TestAzureCredentialFailureNeverSilentlySucceeds(t *testing.T) {
	err := azureCredentialFailure(azureCredentialsUsable, "")
	require.Error(t, err)
}

// The structural guarantee: none of the azure connect files may spawn a
// process, so a later change cannot reintroduce running a *login* without
// turning this test red, regardless of which of the three files it lands
// in or how it is wired up.
//
// os/exec itself is allowed - azureCredentialFailure resolves `az` on PATH
// with exec.LookPath to tell "not installed" apart from "installed but not
// signed in", and that resolves a path without executing anything. What is
// banned is anything that actually starts a process: exec.Command and the
// methods that run it.
//
// This does not mean azure connect makes zero process executions in an
// absolute sense: azidentity's AzureCLICredential shells out to
// `az account get-access-token` internally as one of DefaultAzureCredential's
// chained sources, and that is out of this package's control. What formae
// itself guarantees is narrower and is the thing that matters here: it never
// spawns a *sign-in* the way connect gcp does.
func TestAzureNeverSpawnsALogin(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(thisFile)
	spawnIndicators := []string{"exec.Command(", ".Run()", ".Start()", ".Output()", ".CombinedOutput()"}
	for _, name := range []string{"azureauth.go", "azure.go", "azureprovision.go"} {
		src, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		for _, indicator := range spawnIndicators {
			assert.NotContains(t, string(src), indicator,
				"%s must never spawn a process: Azure reports the login command, it never spawns one", name)
		}
	}
}

// az_missing and credentials_required are the two faces of
// needs-authentication: whether az is on PATH decides which one a run gets,
// and lookPathAz is the seam that lets a test choose without depending on
// what happens to be installed on the machine running it.
func stubLookPathAz(t *testing.T, found bool) {
	t.Helper()
	restore := lookPathAz
	lookPathAz = func(file string) (string, error) {
		if found {
			return "/usr/bin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPathAz = restore })
}

// az on PATH: needs-authentication is a sign-in problem, reported the same
// way it always was.
func TestNeedsAuthenticationWithAzOnPathReportsLoginCommand(t *testing.T) {
	stubLookPathAz(t, true)

	err := azureCredentialFailure(azureCredentialsNeedsAuthentication, "")

	var f *printer.Failure
	require.ErrorAs(t, err, &f)
	assert.Equal(t, printer.CodeCredentialsRequired, f.Code)
	assert.Equal(t, "az login", f.Details["command"])
	assert.Contains(t, f.Message, "connect azure template",
		"an operator who will not sign in needs to be told about the credential-less path")
}

// az missing from PATH: reporting `az login` would send the operator to run
// a command that does not exist, so this must name the install step instead.
func TestNeedsAuthenticationWithoutAzReportsAzMissing(t *testing.T) {
	stubLookPathAz(t, false)

	err := azureCredentialFailure(azureCredentialsNeedsAuthentication, "")

	var f *printer.Failure
	require.ErrorAs(t, err, &f)
	assert.Equal(t, printer.CodeAzMissing, f.Code)
	assert.Contains(t, f.Message, "https://learn.microsoft.com/cli/azure/install-azure-cli")
	assert.Contains(t, f.Message, "connect azure template",
		"an operator who will not install az needs to be told about the credential-less path")
	_, hasCommand := f.Details["command"]
	assert.False(t, hasCommand, "az_missing must not report a login command az cannot run")
}
