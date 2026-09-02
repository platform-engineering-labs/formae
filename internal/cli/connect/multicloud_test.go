// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"context"
	"fmt"
	"testing"

	provxazure "github.com/platform-engineering-labs/oox/provx/azure"
	provxgcp "github.com/platform-engineering-labs/oox/provx/gcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These table-drive assertions that hold, in the same shape, across every
// cloud that has the property. A property that only looks similar per cloud
// stays in that cloud's own _test.go file instead.

// Every cloud's account flag is required: a run naming no account must be
// refused rather than infer one.
func TestAccountFlagIsRequired(t *testing.T) {
	tests := []struct {
		name   string
		decide func() error
	}{
		{"gcp requires --project", func() error {
			_, err := decideGCPMode(gcpOptions{})
			return err
		}},
		{"azure requires --subscription", func() error {
			_, err := decideAzureMode(azureOptions{})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.decide(), "a run with no account must be refused rather than inferring one")
		})
	}
}

// Register-only performs no credential resolution: needing credentials would
// destroy the one reason the mode exists.
func TestRegisterOnlyNeedsNoCredentials(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T)
		stub func(t *testing.T) func() int // returns a reader for the credential-check call count
		args []string
	}{
		{
			name: "gcp register-only",
			seed: func(t *testing.T) { seedGCPRun(t) },
			stub: func(t *testing.T) func() int {
				logins := 0
				stubCredentialState(t, credentialsMissing, &logins)
				return func() int { return logins }
			},
			args: []string{"gcp", "--project", testProject,
				"--workload-identity-provider", testProviderName, "--no-input",
				"--output-consumer", "machine", "--output-schema", "json"},
		},
		{
			name: "azure register-only",
			seed: func(t *testing.T) { seedAzureRun(t) },
			stub: func(t *testing.T) func() int {
				calls := 0
				restore := usableCredentials
				usableCredentials = func(context.Context, string, string) (azureCredentialState, error) {
					calls++
					return azureCredentialsUsable, nil
				}
				t.Cleanup(func() { usableCredentials = restore })
				return func() int { return calls }
			},
			args: azureRegisterOnlyArgs(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := tc.stub(t)
			tc.seed(t)

			out, err := runConnect(t, tc.args...)

			require.NoError(t, err, "out: %s", out)
			assert.Zero(t, calls(), "register-only resolved credentials, which it must never need to do")
		})
	}
}

// Register-only states the reduced assurance in its output, so nobody reads
// "registered" as "working".
func TestRegisterOnlySaysWhatItDidNotVerify(t *testing.T) {
	tests := []struct {
		name        string
		seed        func(t *testing.T)
		args        []string
		wantWarning string
		checkFields func(t *testing.T, got map[string]any)
	}{
		{
			name: "gcp names the unverified coordinate",
			seed: func(t *testing.T) { seedGCPRun(t) },
			args: []string{"gcp", "--project", testProject,
				"--workload-identity-provider", testProviderName, "--no-input",
				"--output-consumer", "machine", "--output-schema", "json"},
			wantWarning: "shape only",
			checkFields: func(t *testing.T, got map[string]any) {
				assert.Equal(t, testProviderName, got["workloadIdentityProvider"])
			},
		},
		{
			name:        "azure names the unverified coordinates",
			seed:        func(t *testing.T) { seedAzureRun(t) },
			args:        azureRegisterOnlyArgs(),
			wantWarning: "validated for shape only",
			checkFields: func(t *testing.T, got map[string]any) {},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.seed(t)

			out, err := runConnect(t, tc.args...)

			require.NoError(t, err, "out: %s", out)
			got := decodeOut(t, out)
			assert.Contains(t, fmt.Sprintf("%v", got["warnings"]), tc.wantWarning)
			tc.checkFields(t, got)
		})
	}
}

// Provisioning succeeded and registration did not: the failure must name what
// survives (a project or subscription now trusting an installation the
// control plane does not know about), never a generic internal error. Each
// cloud states this in its own document shape, which the check functions
// preserve rather than flatten.
func TestRegistrationFailureNamesTheStandingTrust(t *testing.T) {
	const azureIdentityID = "/subscriptions/x/resourceGroups/formae-ai/providers/Microsoft.ManagedIdentity/userAssignedIdentities/formae-ai-inst"

	tests := []struct {
		name  string
		run   func(t *testing.T) (string, error)
		check func(t *testing.T, out string, err error)
	}{
		{
			name: "gcp names the standing trust in its error text",
			run: func(t *testing.T) (string, error) {
				stubCredentialState(t, credentialsUsable, nil)
				installGCPProvisioner(t, &stubGCPProvisioner{result: &provxgcp.Result{
					ProviderName: testProviderName, ProjectNumber: testProjectNumber,
				}}, nil)
				cp := seedGCPRun(t)
				cp.registerStatus = 500
				cp.registerBody = `{"error":"boom"}`

				return runConnect(t, gcpLocalArgs()...)
			},
			check: func(t *testing.T, out string, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "now trusts this installation",
					"the failure must name the trust that stands")
			},
		},
		{
			name: "azure names the surviving trust with structured details",
			run: func(t *testing.T) (string, error) {
				stubAzureCredentialState(t, azureCredentialsUsable)
				installAzureProvisioner(t, &stubAzureProvisioner{result: &provxazure.Result{
					TenantID: testAzureTenant, ClientID: testAzureClient, IdentityID: azureIdentityID,
				}}, nil)
				cp := seedAzureRun(t)
				cp.registerStatus = 500
				cp.registerBody = `{"error":"boom"}`

				return runConnect(t, azureLocalArgs()...)
			},
			check: func(t *testing.T, out string, err error) {
				require.Error(t, err)
				got := decodeOut(t, out)
				assert.Equal(t, "orphaned_trust", got["code"],
					"the machine document must name what survives, not a generic internal failure: %s", out)
				assert.Contains(t, got["message"], "does not know about")
				assert.Contains(t, got["message"], "no rollback")
				assert.Contains(t, got["message"], "near-owner")

				details, ok := got["details"].(map[string]any)
				require.True(t, ok, "the failure must carry structured details: %s", out)
				assert.Equal(t, defaultAzureResourceGroup, details["resourceGroup"],
					"the resource group must be checkable distinctly from the identity id")
				assert.Equal(t, azureIdentityID, details["identity"])
				assert.Equal(t, testAzureClient, details["clientId"])
				assert.NotEqual(t, details["resourceGroup"], details["identity"],
					"resource group and identity must be distinguishable, not both just \"contains formae-ai\"")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.run(t)
			tc.check(t, out, err)
		})
	}
}
