// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	provxazure "github.com/platform-engineering-labs/oox/provx/azure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAzureTenant = "11111111-1111-1111-1111-111111111111"
	testAzureClient = "22222222-2222-2222-2222-222222222222"
)

// --subscription names the account being registered and is required in
// both modes.
func TestAzureSubscriptionIsRequired(t *testing.T) {
	_, err := decideAzureMode(azureOptions{})
	require.Error(t, err)
}

// Every flag combination decideAzureMode has to resolve, per the mode rules:
// client-id is the register-only signal (an existing managed identity is
// the one coordinate that can never be a mere hint); tenant-id alone is
// accepted as a provisioning-mode authentication hint, forwarded verbatim
// into provx's New(), which is exactly what "New() accepts an empty
// azTenantID" is for; client-id without tenant-id cannot name a complete
// external identity and fails.
func TestAzureModeSelection(t *testing.T) {
	tests := []struct {
		name     string
		opts     azureOptions
		wantMode azureMode
		wantErr  bool
	}{
		{"subscription alone provisions", azureOptions{Subscription: testSubscription}, azureModeLocal, false},
		{"tenant hint alone still provisions", azureOptions{Subscription: testSubscription, TenantID: testAzureTenant}, azureModeLocal, false},
		{"client id alone fails", azureOptions{Subscription: testSubscription, ClientID: testAzureClient}, 0, true},
		{"both coordinates register", azureOptions{Subscription: testSubscription, TenantID: testAzureTenant, ClientID: testAzureClient}, azureModeRegisterOnly, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := decideAzureMode(tc.opts)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantMode, mode)
		})
	}
}

// A malformed --tenant-id hint given alone (no --client-id) must be
// refused before it reaches provisioning: it is forwarded verbatim into
// provx's New(), and provx's own contract is that an empty value means
// derive, not that any string is acceptable.
func TestAzureTenantHintMustBeAUUIDEvenAlone(t *testing.T) {
	_, err := decideAzureMode(azureOptions{Subscription: testSubscription, TenantID: "not-a-uuid"})
	require.Error(t, err)
}

// One coordinate without the other fails outright: a bare client id cannot
// name a complete external identity.
func TestAzureRegisterOnlyRequiresBothCoordinates(t *testing.T) {
	_, err := decideAzureMode(azureOptions{Subscription: testSubscription, ClientID: testAzureClient})
	require.Error(t, err)
}

// A tenant or client id that is not a UUID is refused before any
// control-plane or Azure call.
func TestAzureRegisterOnlyRejectsMalformedCoordinates(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		clientID string
	}{
		{"malformed tenant", "not-a-uuid", testAzureClient},
		{"malformed client", testAzureTenant, "not-a-uuid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decideAzureMode(azureOptions{Subscription: testSubscription, TenantID: tc.tenantID, ClientID: tc.clientID})
			require.Error(t, err)
		})
	}
}

// --location and --resource-group are provisioning-only, and rejected only
// when explicitly set: their resolved value is not enough to tell, because
// a default applied before mode determination would make every register-only
// invocation look like it carried a provisioning flag.
func TestAzureLocationAndResourceGroupRejectedOnlyWhenExplicitlySetInRegisterOnly(t *testing.T) {
	base := azureOptions{Subscription: testSubscription, TenantID: testAzureTenant, ClientID: testAzureClient}

	_, err := decideAzureMode(base)
	require.NoError(t, err, "register-only with neither flag set must be accepted")

	withLocation := base
	withLocation.LocationSet = true
	_, err = decideAzureMode(withLocation)
	require.Error(t, err, "an explicitly set --location must be rejected in register-only mode")

	withRG := base
	withRG.ResourceGroupSet = true
	_, err = decideAzureMode(withRG)
	require.Error(t, err, "an explicitly set --resource-group must be rejected in register-only mode")
}

// --location defaults to eastus and --resource-group to formae-ai, applied
// only after mode determination, and only for the provisioning mode.
func TestAzureLocationDefaultsWhenAbsent(t *testing.T) {
	opts := applyAzureLocalDefaults(azureOptions{Subscription: testSubscription})
	assert.Equal(t, "eastus", opts.Location)
}

func TestAzureResourceGroupDefaults(t *testing.T) {
	opts := applyAzureLocalDefaults(azureOptions{Subscription: testSubscription})
	assert.Equal(t, "formae-ai", opts.ResourceGroup)
}

// An explicit value is never overwritten by the default.
func TestAzureDefaultsDoNotOverrideExplicitValues(t *testing.T) {
	opts := applyAzureLocalDefaults(azureOptions{Subscription: testSubscription, Location: "westeurope", ResourceGroup: "custom-rg"})
	assert.Equal(t, "westeurope", opts.Location)
	assert.Equal(t, "custom-rg", opts.ResourceGroup)
}

// stubAzureProvisioner drives the provisioning seam without reaching Azure.
type stubAzureProvisioner struct {
	result *provxazure.Result
	err    error
	calls  int
}

func (s *stubAzureProvisioner) Create(_ context.Context) (*provxazure.Result, error) {
	s.calls++
	return s.result, s.err
}

// azureProvisionerCall is what one newAzureProvisioner invocation was given.
type azureProvisionerCall struct {
	SubscriptionID, AzTenantID, FormaeTenantID, InstallationID, ResourceGroup, Location string
}

func installAzureProvisioner(t *testing.T, p *stubAzureProvisioner, got *azureProvisionerCall) {
	t.Helper()
	restore := newAzureProvisioner
	newAzureProvisioner = func(subscriptionID, azTenantID, formaeTenantID, installationID, resourceGroup, location string) (azureProvisioner, error) {
		if got != nil {
			*got = azureProvisionerCall{subscriptionID, azTenantID, formaeTenantID, installationID, resourceGroup, location}
		}
		return p, nil
	}
	t.Cleanup(func() { newAzureProvisioner = restore })
}

func seedAzureRun(t *testing.T) *controlPlane {
	t.Helper()
	cp := newControlPlane(t)
	cp.registerBody = `{"cloud":"azure","account":"` + testSubscription + `","azureTenantId":"` + testAzureTenant +
		`","azureClientId":"` + testAzureClient + `"}`
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	return cp
}

func azureLocalArgs(extra ...string) []string {
	args := []string{"azure", "--subscription", testSubscription, "--no-input",
		"--output-consumer", "machine", "--output-schema", "json"}
	return append(args, extra...)
}

func azureRegisterOnlyArgs() []string {
	return []string{"azure", "--subscription", testSubscription, "--tenant-id", testAzureTenant, "--client-id", testAzureClient,
		"--no-input", "--output-consumer", "machine", "--output-schema", "json"}
}

// The local path: credentials are usable, provx receives the server-produced
// installation coordinates and the subscription/location/resource-group
// verbatim, and the tenant registered comes from the provisioner's returned
// Result, never from command input, even when no hint was given at all. The
// verification itself happens inside provx/azure, against the subscription's
// actual Entra tenant read from ARM; the CLI performs none of its own and
// only asserts that it registers what came back, not what was asked for.
func TestTheRegisteredTenantComesFromTheProvisionerResult(t *testing.T) {
	stubAzureCredentialState(t, azureCredentialsUsable)
	stub := &stubAzureProvisioner{result: &provxazure.Result{
		TenantID: testAzureTenant, ClientID: testAzureClient, IdentityID: "id-1", ResourceGroup: "formae-ai", Location: "eastus",
	}}
	var call azureProvisionerCall
	installAzureProvisioner(t, stub, &call)
	cp := seedAzureRun(t)

	out, err := runConnect(t, azureLocalArgs()...)

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Equal(t, "azure", got["cloud"])
	assert.Equal(t, testSubscription, got["account"])
	assert.Equal(t, testAzureTenant, got["azureTenantId"], "the registered tenant comes from the provisioner's Result")
	assert.Equal(t, testAzureClient, got["azureClientId"])
	assert.NotContains(t, got, "roleArn")

	assert.Equal(t, 1, stub.calls)
	assert.Equal(t, testSubscription, call.SubscriptionID)
	assert.Equal(t, "", call.AzTenantID, "no hint was given; provx derives it")
	assert.Equal(t, "acme", call.FormaeTenantID, "the server-produced tenant travels verbatim")
	assert.Equal(t, contractInstallation, call.InstallationID)
	assert.Equal(t, "formae-ai", call.ResourceGroup, "the default resource group is used when none was given")
	assert.Equal(t, "eastus", call.Location, "the default location is used when none was given")

	posts := cp.posts()
	require.Len(t, posts, 1)
	assert.Contains(t, posts[0].Body, testAzureClient)
	assert.Contains(t, posts[0].Body, `"azureTenantId":"`+testAzureTenant+`"`,
		"the tenant actually registered with the control plane must be the provisioner's verified Result, not any other value")
}

// A --tenant-id given without --client-id is a provisioning-mode
// authentication hint: it must reach both the credential check and provx's
// New() verbatim.
func TestAzureTenantHintIsUsedWhenGiven(t *testing.T) {
	var gotHint string
	restore := usableCredentials
	usableCredentials = func(_ context.Context, _, tenantHint string) (azureCredentialState, error) {
		gotHint = tenantHint
		return azureCredentialsUsable, nil
	}
	t.Cleanup(func() { usableCredentials = restore })

	stub := &stubAzureProvisioner{result: &provxazure.Result{
		TenantID: testAzureTenant, ClientID: testAzureClient, IdentityID: "id-1",
	}}
	var call azureProvisionerCall
	installAzureProvisioner(t, stub, &call)
	seedAzureRun(t)

	out, err := runConnect(t, azureLocalArgs("--tenant-id", testAzureTenant)...)

	require.NoError(t, err, "out: %s", out)
	assert.Equal(t, testAzureTenant, gotHint, "the hint must reach the credential check")
	assert.Equal(t, testAzureTenant, call.AzTenantID, "the hint must reach provx's New() verbatim")
}

// Register-only says what it did not check, in those words, so nobody reads
// "registered" as "working".
func TestRegisterOnlyStatesTheCoordinatesAreUnverified(t *testing.T) {
	seedAzureRun(t)

	out, err := runConnect(t, azureRegisterOnlyArgs()...)

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Contains(t, fmt.Sprintf("%v", got["warnings"]), "validated for shape only")
}

// Register-only needs no credentials at all: needing them would defeat the
// one reason the mode exists.
func TestAzureRegisterOnlyNeedsNoCredentials(t *testing.T) {
	calls := 0
	restore := usableCredentials
	usableCredentials = func(context.Context, string, string) (azureCredentialState, error) {
		calls++
		return azureCredentialsUsable, nil
	}
	t.Cleanup(func() { usableCredentials = restore })
	seedAzureRun(t)

	out, err := runConnect(t, azureRegisterOnlyArgs()...)

	require.NoError(t, err, "out: %s", out)
	assert.Zero(t, calls, "register-only must never check credentials")
}

// stdinNeverRead fails the test the moment anything reads from stdin: a
// caller that built one fixed command line cannot answer a prompt, so
// nothing on a machine-mode/--no-input run may block waiting on it.
type stdinNeverRead struct{ t *testing.T }

func (s stdinNeverRead) Read([]byte) (int, error) {
	s.t.Helper()
	s.t.Fatal("a machine-mode/--no-input azure run read from stdin")
	return 0, io.EOF
}

// A machine-output or --no-input run never prompts, for every credential
// state: a caller that built one fixed command line did not consent to an
// interactive confirmation. isInteractive is pinned true here on purpose:
// without it, azureInteractiveRun's TTY check alone would already return
// false under `go test`, and the --no-input/machine-consumer gate this test
// exists to protect would never actually be exercised - proven by mutation,
// replacing azureInteractiveRun's body with `return isInteractive()` left
// this test green before this fix.
func TestAzureMachineModeNeverPrompts(t *testing.T) {
	interactiveTTY(t)

	states := []azureCredentialState{
		azureCredentialsUsable, azureCredentialsNeedsAuthentication, azureCredentialsLacksPermission, azureSubscriptionUnreachable,
	}
	for _, state := range states {
		for _, args := range [][]string{
			azureLocalArgs(),
			{"azure", "--subscription", testSubscription, "--output-consumer", "machine", "--output-schema", "json"},
		} {
			confirms := stubConfirms(t)
			stubAzureCredentialState(t, state)
			installAzureProvisioner(t, &stubAzureProvisioner{result: &provxazure.Result{
				TenantID: testAzureTenant, ClientID: testAzureClient,
			}}, nil)
			seedAzureRun(t)

			c := ConnectCmd()
			var out bytes.Buffer
			c.SetOut(&out)
			c.SetErr(&out)
			c.SetIn(stdinNeverRead{t})
			c.SetArgs(args)
			_ = c.Execute()

			assert.Empty(t, confirms.prompts, "state %v args %v prompted despite machine mode/--no-input", state, args)
		}
	}
}

// Provisioning succeeding and registration failing leaves the subscription
// trusting an installation the control plane does not know about, with no
// rollback and near-owner access standing. The failure must name the
// resource group, the identity, and its client id, and say so plainly.
func TestProvisionThenRegistrationFailureNamesTheSurvivingTrust(t *testing.T) {
	stubAzureCredentialState(t, azureCredentialsUsable)
	const identityID = "/subscriptions/x/resourceGroups/formae-ai/providers/Microsoft.ManagedIdentity/userAssignedIdentities/formae-ai-inst"
	installAzureProvisioner(t, &stubAzureProvisioner{result: &provxazure.Result{
		TenantID: testAzureTenant, ClientID: testAzureClient, IdentityID: identityID,
	}}, nil)
	cp := seedAzureRun(t)
	cp.registerStatus = 500
	cp.registerBody = `{"error":"boom"}`

	out, err := runConnect(t, azureLocalArgs()...)

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
	assert.Equal(t, identityID, details["identity"])
	assert.Equal(t, testAzureClient, details["clientId"])
	assert.NotEqual(t, details["resourceGroup"], details["identity"],
		"resource group and identity must be distinguishable, not both just \"contains formae-ai\"")
}

// A sovereign cloud is refused explicitly, before any control-plane or
// Azure call: a subscription id does not identify its cloud, and the
// issuer/authority/ARM endpoints connect pins are public-cloud specific.
func TestSovereignCloudIsRefused(t *testing.T) {
	t.Setenv("AZURE_ENVIRONMENT", "AzureUSGovernment")
	cp := seedAzureRun(t)

	out, err := runConnect(t, azureLocalArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "unsupported_partition", got["code"])
	assert.Empty(t, cp.requests(), "a sovereign cloud must be refused before any request")
}

// A 409 from the control plane is disambiguated by comparing the managed
// identity's client id, the way a role ARN or a workload identity provider
// is compared for the other two clouds: without a dedicated azure coordinate
// comparison, this would always compare two empty strings and misreport
// every conflict as already registered.
func TestAzureRegistrationConflictComparesTheManagedIdentity(t *testing.T) {
	t.Run("same client id is already_registered", func(t *testing.T) {
		cp := seedAzureRun(t)
		cp.registerStatus = 409
		cp.registerBody = `{"error":{"code":"cloud_connection_exists"}}`
		cp.connectionsBody = `{"results":[{"cloud":"azure","account":"` + testSubscription +
			`","azureTenantId":"` + testAzureTenant + `","azureClientId":"` + testAzureClient + `"}]}`

		out, err := runConnect(t, azureRegisterOnlyArgs()...)

		require.NoError(t, err, "out: %s", out)
		assert.Equal(t, "already_registered", decodeOut(t, out)["status"])
	})

	t.Run("a different client id is registration_conflict naming both", func(t *testing.T) {
		other := "33333333-3333-3333-3333-333333333333"
		cp := seedAzureRun(t)
		cp.registerStatus = 409
		cp.registerBody = `{"error":{"code":"cloud_connection_exists"}}`
		cp.connectionsBody = `{"results":[{"cloud":"azure","account":"` + testSubscription +
			`","azureTenantId":"` + testAzureTenant + `","azureClientId":"` + other + `"}]}`

		out, err := runConnect(t, azureRegisterOnlyArgs()...)

		require.Error(t, err)
		got := decodeOut(t, out)
		assert.Equal(t, "registration_conflict", got["code"])
		details, ok := got["details"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, other, details["registeredAzureClientId"])
		assert.Equal(t, testAzureClient, details["statedAzureClientId"])
	})
}
