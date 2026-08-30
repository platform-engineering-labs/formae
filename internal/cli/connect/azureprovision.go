// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	provxazure "github.com/platform-engineering-labs/oox/provx/azure"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// azureProvisioner is provx narrowed to the one call the local path makes.
type azureProvisioner interface {
	Create(ctx context.Context) (*provxazure.Result, error)
}

// newAzureProvisioner is the seam tests substitute. Production constructs
// provx with the server-produced installation coordinates and the operator's
// flags verbatim: connect has no naming knowledge of its own, and azTenantID
// travels through exactly as given - empty means derive, which is provx's
// own contract.
var newAzureProvisioner = func(subscriptionID, azTenantID, formaeTenantID, installationID, resourceGroup, location string) (azureProvisioner, error) {
	return provxazure.New(slog.Default(), subscriptionID, azTenantID, formaeTenantID, installationID, resourceGroup, location)
}

// provisionAzure converges the subscription's connection resources and
// reports what they converged to.
func provisionAzure(ctx context.Context, subscriptionID, azTenantID, formaeTenantID, installationID, resourceGroup, location string) (*provxazure.Result, error) {
	p, err := newAzureProvisioner(subscriptionID, azTenantID, formaeTenantID, installationID, resourceGroup, location)
	if err != nil {
		return nil, classifyAzureProvision(err)
	}
	result, err := p.Create(ctx)
	if err != nil {
		return nil, classifyAzureProvision(err)
	}
	return result, nil
}

// classifyAzureProvision maps provx's typed errors onto declared codes.
// Anything untyped is provision_failed with the honest what-stands message:
// re-running converges, so the message says so instead of promising a
// rollback.
func classifyAzureProvision(err error) error {
	var locMismatch *provxazure.LocationMismatchError
	if errors.As(err, &locMismatch) {
		return printer.Fail(printer.CodeProvisionFailed, fmt.Sprintf(
			"resource group %s already exists in %s, not the requested %s; a resource group's location cannot be changed",
			locMismatch.ResourceGroup, locMismatch.Existing, locMismatch.Requested), nil)
	}

	var tenantMismatch *provxazure.TenantMismatchError
	if errors.As(err, &tenantMismatch) {
		return printer.Fail(printer.CodeAccountMismatch, fmt.Sprintf(
			"the subscription belongs to tenant %s, not the stated %s", tenantMismatch.Actual, tenantMismatch.Pinned), nil)
	}

	var notOurs *provxazure.IdentityNotOursError
	if errors.As(err, &notOurs) {
		return printer.Fail(printer.CodeProviderConflict, fmt.Sprintf(
			"the managed identity %s already exists and its federated credential is not one formae recognizes "+
				"(%s); formae will not modify it", notOurs.Name, notOurs.Reason), nil)
	}

	// RoleAssignmentForbiddenError before PermissionDeniedError: both wrap the
	// same ARM AuthorizationFailed code, and provx has already told them apart
	// by which call failed.
	var forbidden *provxazure.RoleAssignmentForbiddenError
	if errors.As(err, &forbidden) {
		return printer.Fail(printer.CodeNotAuthorized,
			"these credentials may not create role assignments on this subscription; this requires the Owner "+
				"or User Access Administrator role", nil)
	}

	var notRegistered *provxazure.ProviderNotRegisteredError
	if errors.As(err, &notRegistered) {
		return printer.Fail(printer.CodeApiDisabled, fmt.Sprintf(
			"the %s resource provider is not registered on this subscription; register it and re-run", notRegistered.Provider),
			map[string]any{"provider": notRegistered.Provider})
	}

	var denied *provxazure.PermissionDeniedError
	if errors.As(err, &denied) {
		return printer.Fail(printer.CodeNotAuthorized,
			"these credentials may not create the resources this connection needs in that subscription", nil)
	}

	return printer.Fail(printer.CodeProvisionFailed,
		"provisioning did not complete; what was created stands, and re-running this command converges it", nil)
}
