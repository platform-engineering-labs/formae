// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	provxazure "github.com/platform-engineering-labs/oox/provx/azure"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// azureCredentialState is what a run knows about the ambient Azure
// credentials and their reach into the stated subscription, before any
// provisioning call is made.
//
// The distinction that matters is the last two: only needs-authentication is
// something a fresh sign-in can fix. A principal that authenticates but
// cannot read the subscription, or a subscription that cannot be reached at
// all, is not - re-authenticating there returns the same principal and
// overwrites nothing but time.
type azureCredentialState int

const (
	azureCredentialsUsable azureCredentialState = iota
	azureCredentialsNeedsAuthentication
	azureCredentialsLacksPermission
	azureSubscriptionUnreachable
)

// usableCredentials classifies the ambient Azure credentials against the
// stated subscription with azidentity.NewDefaultAzureCredential plus one
// cheap ARM call (Subscriptions.Get) - the same call provx's own
// VerifySubscription makes. tenantHint is forwarded to the credential
// exactly as provx's New() would use it: an external or guest account can
// need an explicit tenant to authenticate at all, before the subscription
// can even be reached.
//
// A var so tests substitute it wholesale: there is no seam narrower than
// "build a credential and make one ARM call" that would let a test drive
// every classification without a real Azure environment, the same reason
// GCP's findCredentials is a var.
var usableCredentials = func(ctx context.Context, subscriptionID, tenantHint string) (azureCredentialState, error) {
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{TenantID: tenantHint})
	if err != nil {
		return azureCredentialsNeedsAuthentication, nil //nolint:nilerr // the classification is the answer, not a failure
	}
	client, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return azureCredentialsNeedsAuthentication, nil //nolint:nilerr // as above
	}
	if _, err := client.Get(ctx, subscriptionID, nil); err != nil {
		return classifyAzureCredentialError(err), nil
	}
	return azureCredentialsUsable, nil
}

// classifyAzureCredentialError turns a failed subscription read into one of
// the three non-usable states, reusing provx's own ARM error classification
// so the CLI's read of "permission denied" agrees with provisioning's.
func classifyAzureCredentialError(err error) azureCredentialState {
	var authErr *azidentity.AuthenticationFailedError
	if errors.As(err, &authErr) {
		return azureCredentialsNeedsAuthentication
	}
	var denied *provxazure.PermissionDeniedError
	if errors.As(provxazure.Classify(err, provxazure.Operation{Provider: "Microsoft.Resources"}), &denied) {
		return azureCredentialsLacksPermission
	}
	return azureSubscriptionUnreachable
}

// lookPathAz is exec.LookPath narrowed to the one call azureCredentialFailure
// makes. A var so tests substitute it: azidentity.NewDefaultAzureCredential
// tries env vars and managed identity before it ever reaches for az, so
// nothing about needs-authentication implies az is installed, and there is
// no seam narrower than "resolve this one binary on PATH" to drive both
// branches without depending on what is actually installed on the test
// machine.
//
// This resolves a path; it does not execute one. Unlike GCP's sign-in, which
// formae runs on the operator's behalf, formae never spawns `az` - it only
// reports the command - so this check must never grow into one that shells
// out.
var lookPathAz = func(file string) (string, error) { return exec.LookPath(file) }

// azureCredentialFailure maps a non-usable classification onto the declared
// codes. Only needs-authentication carries a login command: a
// lacks-permission or unreachable-subscription failure would send the
// operator through a sign-in that returns the same principal and fixes
// nothing.
//
// needs-authentication itself splits in two. azidentity.NewDefaultAzureCredential
// tries environment variables and managed identity before it ever reaches for
// the az CLI, so failing here does not mean az ran and refused - it can just
// as easily mean az is not installed at all, which is the likely state for
// a brand-new machine. Reporting `az login` in that case sends the operator
// to run a command that does not exist; checking PATH first tells them to
// install it instead.
func azureCredentialFailure(state azureCredentialState, tenantHint string) error {
	switch state {
	case azureCredentialsNeedsAuthentication:
		if _, err := lookPathAz("az"); err != nil {
			return printer.Fail(printer.CodeAzMissing,
				"no usable Azure credentials, and the az CLI needed to sign in to Azure is not installed; install it "+
					"from https://learn.microsoft.com/cli/azure/install-azure-cli and re-run this command, or run "+
					"`formae connect azure template` instead if you would rather not hand this machine a "+
					"provisioning credential", nil)
		}
		return printer.Fail(printer.CodeCredentialsRequired,
			"no usable Azure credentials for this subscription; run the sign-in and re-run this command, or run "+
				"`formae connect azure template` instead if you would rather not hand this machine a provisioning "+
				"credential",
			map[string]any{"command": azLoginCommand(tenantHint)})
	case azureCredentialsLacksPermission:
		return printer.Fail(printer.CodeNotAuthorized,
			"these credentials may not read this subscription; connect needs at least Reader access on it to provision the trust", nil)
	case azureSubscriptionUnreachable:
		return printer.Fail(printer.CodeProjectUnreachable,
			"the subscription could not be read with these credentials; check the subscription id, and that this principal can see it", nil)
	default:
		// azureCredentialsUsable, or any value this build does not know: the
		// caller is only meant to reach this function for a non-usable state,
		// so silently returning nil here would hide that mistake behind a
		// success. Every branch of an error-returning function returns an
		// error, including the one that should never be taken.
		return printer.Fail(printer.CodeInternal,
			fmt.Sprintf("azureCredentialFailure was called for credential state %d, which is not a failure", state), nil)
	}
}

// azLoginCommand is the exact remedy a needs-authentication failure names,
// mirroring classifySSO's shape on the AWS path: report the command, never
// run it. A caller relaying this (the MCP tool's own contract is to show it
// to the user verbatim) hands it straight to a shell, so it must always be
// runnable as given. With no tenant hint, the flag is omitted rather than
// filled with a placeholder like "<id>": that reads as a helpful stand-in
// but is a broken command the moment anyone actually runs it, and formae has
// no credential yet from which to derive a real value to put there. Plain
// "az login" is the correct fallback - it is exactly what an operator would
// run by hand in the same situation.
func azLoginCommand(tenantHint string) string {
	if tenantHint == "" {
		return "az login"
	}
	return "az login --tenant " + tenantHint
}
