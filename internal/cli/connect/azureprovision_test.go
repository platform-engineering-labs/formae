// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"testing"

	provxazure "github.com/platform-engineering-labs/oox/provx/azure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// Every typed failure provx's Azure package can raise must land on its own
// declared code: a tenant mismatch and a forbidden role assignment have
// nothing in common as remedies, and collapsing them into one generic
// failure would send an operator chasing the wrong fix.
func TestAzureProvisionFailuresAreClassifiedApart(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode printer.Code
	}{
		{"location mismatch",
			&provxazure.LocationMismatchError{ResourceGroup: "formae-ai", Existing: "westus", Requested: "eastus"},
			printer.CodeProvisionFailed},
		{"tenant mismatch",
			&provxazure.TenantMismatchError{Pinned: "aaaaaaaa-0000-0000-0000-000000000000", Actual: "bbbbbbbb-0000-0000-0000-000000000000"},
			printer.CodeAccountMismatch},
		{"identity not ours",
			&provxazure.IdentityNotOursError{Name: "formae-ai-inst", Reason: "issuer mismatch"},
			printer.CodeProviderConflict},
		{"role assignment forbidden",
			&provxazure.RoleAssignmentForbiddenError{Scope: "/subscriptions/x"},
			printer.CodeNotAuthorized},
		{"provider not registered",
			&provxazure.ProviderNotRegisteredError{Provider: "Microsoft.ManagedIdentity"},
			printer.CodeApiDisabled},
		{"permission denied",
			&provxazure.PermissionDeniedError{},
			printer.CodeNotAuthorized},
		{"unclassified", assert.AnError, printer.CodeProvisionFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := classifyAzureProvision(c.err)
			var f *printer.Failure
			require.ErrorAs(t, err, &f)
			assert.Equal(t, c.wantCode, f.Code)
		})
	}
}

// A disabled/unregistered resource provider names the provider, the same
// way GCP's api_disabled failure names the API: the remedy is one command
// and formae does not run it uninvited.
func TestAzureProviderNotRegisteredNamesTheProvider(t *testing.T) {
	err := classifyAzureProvision(&provxazure.ProviderNotRegisteredError{Provider: "Microsoft.ManagedIdentity"})

	var f *printer.Failure
	require.ErrorAs(t, err, &f)
	assert.Equal(t, "Microsoft.ManagedIdentity", f.Details["provider"])
}
