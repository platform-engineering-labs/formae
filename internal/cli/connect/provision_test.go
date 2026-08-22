// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The provisioner seam: everything around it is real — caller verification,
// setup read, registration — while the provx constructor itself lands with
// its module. Until it does, the local path declares exactly that.

func localArgs() []string {
	return []string{"aws", "--account", testAccount, "--profile-aws", "test", "--no-input",
		"--output-consumer", "machine", "--output-schema", "json"}
}

// Without the provx integration the local path is a declared failure, not an
// absent branch: the code is provision_failed and the message says what to
// use meanwhile. Nothing is provisioned and nothing is registered.
func TestContractLocalPathDeclaresThePendingProvxIntegration(t *testing.T) {
	require.Nil(t, newProvisioner, "this test pins the pre-integration state; delete it when provx lands")

	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, localArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "provision_failed", got["code"])
	assert.Contains(t, got["message"], "provx", "the message names what the path waits on")
	assert.Empty(t, cp.posts(), "nothing may be registered while provisioning is unavailable")
}

// stubProvisioner drives the seam without provx.
type stubProvisioner struct {
	result *provisionResult
	err    error
	calls  int
}

func (s *stubProvisioner) Create(_ context.Context) (*provisionResult, error) {
	s.calls++
	return s.result, s.err
}

// installProvisioner points the seam at a stub for one test.
func installProvisioner(t *testing.T, p *stubProvisioner, wantSubject, wantRole, wantIssuer *string) {
	t.Helper()
	restore := newProvisioner
	newProvisioner = func(_ context.Context, caller verifiedCaller, subject, roleName, issuer string) (provisioner, error) {
		if wantSubject != nil {
			*wantSubject = subject
		}
		if wantRole != nil {
			*wantRole = roleName
		}
		if wantIssuer != nil {
			*wantIssuer = issuer
		}
		return p, nil
	}
	t.Cleanup(func() { newProvisioner = restore })
}

// With a provisioner installed, the local path runs end to end: STS verifies
// the caller, provx receives the server-produced coordinates verbatim, and
// the resulting role ARN is registered.
func TestContractLocalPathProvisionsAndRegisters(t *testing.T) {
	provisionedArn := "arn:aws:iam::" + testAccount + ":role/" + contractRoleName
	stub := &stubProvisioner{result: &provisionResult{
		RoleArn:          provisionedArn,
		DetachedPolicies: []string{"arn:aws:iam::aws:policy/SomeDetachedPolicy"},
	}}
	var gotSubject, gotRole, gotIssuer string
	installProvisioner(t, stub, &gotSubject, &gotRole, &gotIssuer)

	seedAWSConfig(t, "[profile test]\nregion = eu-west-1\n")
	fakeSTS(t, testAccount, "arn:aws:iam::"+testAccount+":user/dev")
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, localArgs()...)

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Equal(t, "registered", got["phase"])
	assert.Equal(t, "registered_unverified", got["status"])
	assert.Equal(t, provisionedArn, got["roleArn"])
	warnings := fmt.Sprintf("%v", got["warnings"])
	assert.Contains(t, warnings, "SomeDetachedPolicy", "detached policies surface as warnings")

	assert.Equal(t, 1, stub.calls)
	assert.Equal(t, "fai:acme/"+contractInstallation, gotSubject, "the server-produced subject travels verbatim")
	assert.Equal(t, contractRoleName, gotRole)
	assert.Equal(t, "https://oidc.cloud.formae.ai", gotIssuer)

	posts := cp.posts()
	require.Len(t, posts, 1)
	assert.Contains(t, posts[0].Body, provisionedArn)
}

// An account mismatch stops the run before the provisioner is constructed.
func TestContractLocalPathMismatchPrecedesProvisioning(t *testing.T) {
	stub := &stubProvisioner{result: &provisionResult{RoleArn: "unused"}}
	installProvisioner(t, stub, nil, nil, nil)

	seedAWSConfig(t, "[profile test]\nregion = eu-west-1\n")
	fakeSTS(t, "999999999999", "arn:aws:iam::999999999999:user/dev")
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, localArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "account_mismatch", got["code"])
	assert.Zero(t, stub.calls, "no IAM work may start for a mismatched account")
	assert.Empty(t, cp.posts())
}

// A provisioner failure that is not typed is provision_failed with the honest
// what-stands message: re-running converges, so the message says so.
func TestContractLocalPathUntypedProvisionFailure(t *testing.T) {
	stub := &stubProvisioner{err: errors.New("iam said no")}
	installProvisioner(t, stub, nil, nil, nil)

	seedAWSConfig(t, "[profile test]\nregion = eu-west-1\n")
	fakeSTS(t, testAccount, "arn:aws:iam::"+testAccount+":user/dev")
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, localArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "provision_failed", got["code"])
	assert.Contains(t, got["message"], "re-running")
	assert.Empty(t, cp.posts(), "a failed provision registers nothing")
}
