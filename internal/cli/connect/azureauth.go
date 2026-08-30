// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"errors"

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

// azureCredentialFailure maps a non-usable classification onto the declared
// codes. Only needs-authentication carries a login command: a
// lacks-permission or unreachable-subscription failure would send the
// operator through a sign-in that returns the same principal and fixes
// nothing.
func azureCredentialFailure(state azureCredentialState, tenantHint string) error {
	switch state {
	case azureCredentialsNeedsAuthentication:
		return printer.Fail(printer.CodeCredentialsRequired,
			"no usable Azure credentials for this subscription; run the sign-in and re-run this command",
			map[string]any{"command": azLoginCommand(tenantHint)})
	case azureCredentialsLacksPermission:
		return printer.Fail(printer.CodeNotAuthorized,
			"these credentials may not read this subscription; connect needs at least Reader access on it to provision the trust", nil)
	case azureSubscriptionUnreachable:
		return printer.Fail(printer.CodeProjectUnreachable,
			"the subscription could not be read with these credentials; check the subscription id, and that this principal can see it", nil)
	default:
		return nil
	}
}

// azLoginCommand is the exact remedy a needs-authentication failure names,
// mirroring classifySSO's shape on the AWS path: report the command, never
// run it. When no tenant hint was given the placeholder stays literal -
// formae has no credential yet from which to derive a real one.
func azLoginCommand(tenantHint string) string {
	id := tenantHint
	if id == "" {
		id = "<id>"
	}
	return "az login --tenant " + id
}
