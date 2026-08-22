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

	provxaws "github.com/platform-engineering-labs/oox/provx/aws"
)

// The provisioner seam: everything around it is real — caller verification,
// setup read, registration — while provx itself is stubbed, so no test here
// touches the network or real AWS.

func localArgs() []string {
	return []string{"aws", "--account", testAccount, "--profile-aws", "test", "--no-input",
		"--output-consumer", "machine", "--output-schema", "json"}
}

// stubProvisioner drives the seam without provx's IAM client.
type stubProvisioner struct {
	result *provxaws.Result
	err    error
	calls  int
}

func (s *stubProvisioner) Create(_ context.Context) (*provxaws.Result, error) {
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

// seedLocalRun stands up everything around the seam: an AWS profile with a
// region, an STS endpoint answering for the account, a control plane with a
// hosted profile, and credentials.
func seedLocalRun(t *testing.T, stsAccount string) *controlPlane {
	t.Helper()
	seedAWSConfig(t, "[profile test]\nregion = eu-west-1\n")
	fakeSTS(t, stsAccount, "arn:aws:iam::"+stsAccount+":user/dev")
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	return cp
}

// With a provisioner installed, the local path runs end to end: STS verifies
// the caller, provx receives the server-produced coordinates verbatim, and
// the resulting role ARN is registered. Whatever provx had to detach or
// delete to converge rides the registered document as warnings.
func TestContractLocalPathProvisionsAndRegisters(t *testing.T) {
	provisionedArn := "arn:aws:iam::" + testAccount + ":role/" + contractRoleName
	stub := &stubProvisioner{result: &provxaws.Result{
		RoleArn:          provisionedArn,
		DetachedPolicies: []string{"arn:aws:iam::aws:policy/SomeDetachedPolicy"},
		DeletedInline:    []string{"stray-inline"},
	}}
	var gotSubject, gotRole, gotIssuer string
	installProvisioner(t, stub, &gotSubject, &gotRole, &gotIssuer)

	cp := seedLocalRun(t, testAccount)

	out, err := runConnect(t, localArgs()...)

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Equal(t, "registered", got["phase"])
	assert.Equal(t, "registered_unverified", got["status"])
	assert.Equal(t, provisionedArn, got["roleArn"])
	warnings := fmt.Sprintf("%v", got["warnings"])
	assert.Contains(t, warnings, "SomeDetachedPolicy", "detached policies surface as warnings")
	assert.Contains(t, warnings, "stray-inline", "deleted inline policies surface as warnings")

	assert.Equal(t, 1, stub.calls)
	assert.Equal(t, "fai:acme/"+contractInstallation, gotSubject, "the server-produced subject travels verbatim")
	assert.Equal(t, contractRoleName, gotRole)
	assert.Equal(t, "https://oidc.cloud.formae.ai", gotIssuer)

	posts := cp.posts()
	require.Len(t, posts, 1)
	assert.Contains(t, posts[0].Body, provisionedArn)
}

// The same warnings reach a human reader as prose.
func TestContractLocalPathWarningsReachHumanOutput(t *testing.T) {
	stub := &stubProvisioner{result: &provxaws.Result{
		RoleArn:          "arn:aws:iam::" + testAccount + ":role/" + contractRoleName,
		DetachedPolicies: []string{"arn:aws:iam::aws:policy/SomeDetachedPolicy"},
	}}
	installProvisioner(t, stub, nil, nil, nil)

	seedLocalRun(t, testAccount)

	out, err := runConnect(t, "aws", "--account", testAccount, "--profile-aws", "test", "--no-input")

	require.NoError(t, err, "out: %s", out)
	assert.Contains(t, out, "warning:")
	assert.Contains(t, out, "SomeDetachedPolicy")
}

// An account mismatch stops the run before the provisioner is constructed.
func TestContractLocalPathMismatchPrecedesProvisioning(t *testing.T) {
	stub := &stubProvisioner{result: &provxaws.Result{RoleArn: "unused"}}
	installProvisioner(t, stub, nil, nil, nil)

	cp := seedLocalRun(t, "999999999999")

	out, err := runConnect(t, localArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "account_mismatch", got["code"])
	assert.Zero(t, stub.calls, "no IAM work may start for a mismatched account")
	assert.Empty(t, cp.posts())
}

// Each typed provx error maps to its declared code; wrapping (Create wraps
// with the phase it failed in) must not defeat the classification.
func TestContractLocalPathTypedErrorsMapToDeclaredCodes(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		code     string
		fragment string
	}{
		{
			name:     "account mismatch",
			err:      &provxaws.AccountMismatchError{Expected: testAccount, Actual: "999999999999"},
			code:     "account_mismatch",
			fragment: "different account",
		},
		{
			name:     "role collision",
			err:      fmt.Errorf("connector role: %w", &provxaws.RoleCollisionError{RoleName: contractRoleName}),
			code:     "role_collision",
			fragment: "--role-arn",
		},
		{
			name:     "provider conflict",
			err:      fmt.Errorf("oidc provider: %w", &provxaws.ProviderConflictError{Reason: "audience differs"}),
			code:     "provider_conflict",
			fragment: "identity provider",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubProvisioner{err: tc.err}
			installProvisioner(t, stub, nil, nil, nil)

			cp := seedLocalRun(t, testAccount)

			out, err := runConnect(t, localArgs()...)

			require.Error(t, err)
			got := decodeOut(t, out)
			assert.Equal(t, tc.code, got["code"])
			assert.Contains(t, got["message"], tc.fragment)
			assert.Empty(t, cp.posts(), "a failed provision registers nothing")
		})
	}
}

// A provisioner failure that is not typed is provision_failed with the honest
// what-stands message: re-running converges, so the message says so.
func TestContractLocalPathUntypedProvisionFailure(t *testing.T) {
	stub := &stubProvisioner{err: errors.New("iam said no")}
	installProvisioner(t, stub, nil, nil, nil)

	cp := seedLocalRun(t, testAccount)

	out, err := runConnect(t, localArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "provision_failed", got["code"])
	assert.Contains(t, got["message"], "re-running")
	assert.Empty(t, cp.posts(), "a failed provision registers nothing")
}
