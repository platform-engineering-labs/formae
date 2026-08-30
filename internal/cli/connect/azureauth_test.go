// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"context"
	"os"
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
// hint the placeholder in the command stays literal: formae has no
// credential yet from which to derive one.
func TestNoCredentialsReportsTheLoginCommand(t *testing.T) {
	err := azureCredentialFailure(azureCredentialsNeedsAuthentication, "")

	require.Error(t, err)
	var f *printer.Failure
	require.ErrorAs(t, err, &f)
	assert.Equal(t, printer.CodeCredentialsRequired, f.Code)
	assert.Equal(t, "az login --tenant <id>", f.Details["command"])
}

// A tenant hint the operator already gave travels into the reported command
// verbatim, so pasting it back in works the first time.
func TestAzureTenantHintNamesTheLoginCommand(t *testing.T) {
	err := azureCredentialFailure(azureCredentialsNeedsAuthentication, "11111111-1111-1111-1111-111111111111")

	var f *printer.Failure
	require.ErrorAs(t, err, &f)
	assert.Equal(t, "az login --tenant 11111111-1111-1111-1111-111111111111", f.Details["command"])
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

// The structural guarantee: none of the azure connect files may import
// os/exec, so a later change cannot reintroduce spawning a *login* without
// turning this test red, regardless of which of the three files it lands
// in or how it is wired up.
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
	for _, name := range []string{"azureauth.go", "azure.go", "azureprovision.go"} {
		src, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.NotContains(t, string(src), `"os/exec"`,
			"%s must never import os/exec: Azure reports the login command, it never spawns one", name)
	}
}
