// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// The decision core is pure: no I/O, no TTY, no control plane. These tests pin
// how the flag set picks a path and what it refuses.

const testAccount = "123456789012"

func flagError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var fe *cmd.FlagError
	assert.True(t, errors.As(err, &fe), "expected a FlagError, got %T: %v", err, err)
}

func failureCode(t *testing.T, err error, code printer.Code) {
	t.Helper()
	require.Error(t, err)
	var f *printer.Failure
	require.True(t, errors.As(err, &f), "expected a declared failure, got %T: %v", err, err)
	assert.Equal(t, code, f.Code)
}

// Combining trust flags is an error, never a precedence.
func TestDecideMode_TrustFlagsAreMutuallyExclusive(t *testing.T) {
	tests := []struct {
		name string
		opts options
	}{
		{name: "quick-create and profile-aws", opts: options{QuickCreate: true, ProfileAWS: "dev"}},
		{name: "quick-create and role-arn", opts: options{QuickCreate: true, RoleArn: "arn:aws:iam::123456789012:role/r"}},
		{name: "profile-aws and role-arn", opts: options{ProfileAWS: "dev", RoleArn: "arn:aws:iam::123456789012:role/r"}},
		{name: "all three", opts: options{QuickCreate: true, ProfileAWS: "dev", RoleArn: "arn:aws:iam::123456789012:role/r"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Account = testAccount
			_, err := decideMode(tc.opts, true)
			flagError(t, err)
			assert.Contains(t, err.Error(), "mutually exclusive")
		})
	}
}

func TestDecideMode_NoInputRequiresAccountAndExactlyOneTrustFlag(t *testing.T) {
	_, err := decideMode(options{NoInput: true}, false)
	flagError(t, err)
	assert.Contains(t, err.Error(), "--account")
	assert.Contains(t, err.Error(), "--quick-create")

	_, err = decideMode(options{NoInput: true, QuickCreate: true}, false)
	flagError(t, err)
	assert.Contains(t, err.Error(), "--account")
	assert.NotContains(t, err.Error(), "one of")

	_, err = decideMode(options{NoInput: true, Account: testAccount}, false)
	flagError(t, err)
	assert.Contains(t, err.Error(), "one of")

	mode, err := decideMode(options{NoInput: true, Account: testAccount, QuickCreate: true}, false)
	require.NoError(t, err)
	assert.Equal(t, modeQuickCreate, mode)
}

// --provider-exists answers a question only quick-create asks.
func TestDecideMode_ProviderExistsIsRefusedOffQuickCreate(t *testing.T) {
	for _, opts := range []options{
		{Account: testAccount, RoleArn: "arn:aws:iam::" + testAccount + ":role/r", ProviderExists: true},
		{Account: testAccount, ProfileAWS: "dev", ProviderExists: true},
		{Account: testAccount, ProviderExists: true},
	} {
		_, err := decideMode(opts, true)
		flagError(t, err)
		assert.Contains(t, err.Error(), "--quick-create")
	}

	mode, err := decideMode(options{Account: testAccount, QuickCreate: true, ProviderExists: true}, true)
	require.NoError(t, err)
	assert.Equal(t, modeQuickCreate, mode)
}

func TestDecideMode_AccountMustBeTwelveDigits(t *testing.T) {
	for _, account := range []string{"123", "1234567890123", "12345678901a", "  123456789012", "-23456789012"} {
		_, err := decideMode(options{Account: account, QuickCreate: true}, true)
		flagError(t, err)
		assert.Contains(t, err.Error(), "12 digits")
	}
}

func TestDecideMode_PicksThePathTheFlagsName(t *testing.T) {
	mode, err := decideMode(options{Account: testAccount, QuickCreate: true}, true)
	require.NoError(t, err)
	assert.Equal(t, modeQuickCreate, mode)

	mode, err = decideMode(options{Account: testAccount, ProfileAWS: "dev"}, true)
	require.NoError(t, err)
	assert.Equal(t, modeLocal, mode)

	mode, err = decideMode(options{Account: testAccount, RoleArn: "arn:aws:iam::" + testAccount + ":role/r"}, true)
	require.NoError(t, err)
	assert.Equal(t, modeRegisterOnly, mode)

	mode, err = decideMode(options{}, true)
	require.NoError(t, err)
	assert.Equal(t, modeForm, mode)
}

// Without a TTY there is no form to fall back to, and the error says what to
// pass instead.
func TestDecideMode_NoTrustFlagAndNoTTYNamesNoInput(t *testing.T) {
	_, err := decideMode(options{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--no-input")
	var fe *cmd.FlagError
	assert.False(t, errors.As(err, &fe), "a missing TTY is not an argv mistake")
}

func TestParseRoleArn_AcceptsAWellFormedCommercialArn(t *testing.T) {
	parsed, err := parseRoleArn("arn:aws:iam::"+testAccount+":role/formae-connect-abc", testAccount)
	require.NoError(t, err)
	assert.Equal(t, testAccount, parsed.Account)
	assert.Equal(t, "formae-connect-abc", parsed.RoleName)
	assert.Equal(t, "arn:aws:iam::"+testAccount+":role/formae-connect-abc", parsed.Arn)
}

// The role name compared against cloudRoleName is the final path component.
func TestParseRoleArn_TakesTheFinalPathComponentAsTheName(t *testing.T) {
	parsed, err := parseRoleArn("arn:aws:iam::"+testAccount+":role/teams/platform/my-role", testAccount)
	require.NoError(t, err)
	assert.Equal(t, "my-role", parsed.RoleName)
}

func TestParseRoleArn_RefusesMalformedArns(t *testing.T) {
	for _, arn := range []string{
		"",
		"not-an-arn",
		"arn:aws:s3:::bucket",
		"arn:aws:iam::" + testAccount + ":user/someone",
		"arn:aws:iam::" + testAccount + ":role/",
		"arn:aws:iam:eu-west-1:" + testAccount + ":role/r",
	} {
		_, err := parseRoleArn(arn, testAccount)
		failureCode(t, err, printer.CodeUnsupportedPartition)
	}
}

func TestParseRoleArn_RefusesNonCommercialPartitions(t *testing.T) {
	for _, arn := range []string{
		"arn:aws-us-gov:iam::" + testAccount + ":role/r",
		"arn:aws-cn:iam::" + testAccount + ":role/r",
	} {
		_, err := parseRoleArn(arn, testAccount)
		failureCode(t, err, printer.CodeUnsupportedPartition)
		assert.Contains(t, err.Error(), "commercial")
	}
}

func TestParseRoleArn_RefusesAnArnNamingAnotherAccount(t *testing.T) {
	_, err := parseRoleArn("arn:aws:iam::999999999999:role/r", testAccount)
	failureCode(t, err, printer.CodeAccountMismatch)
}

// A role name that differs from the server's cloudRoleName is a warning, not a
// refusal: the human said this is the role, and the CLI registers what it was
// told while saying what it noticed.
func TestWarnOnNameMismatch(t *testing.T) {
	assert.Empty(t, warnOnNameMismatch("formae-connect-a", "formae-connect-a"))

	warning := warnOnNameMismatch("my-own-role", "formae-connect-a")
	assert.Contains(t, warning, "my-own-role")
	assert.Contains(t, warning, "formae-connect-a")
}

// connectedElsewhere warns only about the same AWS account on a different
// installation: a GCP account string equal to a 12-digit AWS id is not this
// account, and the installation being connected is not "elsewhere".
// Any aws hint entry for the account means the provider very likely exists —
// including this installation's own prior connection. Other clouds never
// match.
func TestAccountInHint(t *testing.T) {
	hint := []cloudapi.ConnectedAccount{
		{Cloud: "gcp", Account: testAccount, InstallationID: "other"},
		{Cloud: "aws", Account: "999999999999", InstallationID: "other"},
	}
	assert.False(t, accountInHint(hint, testAccount))

	hint = append(hint, cloudapi.ConnectedAccount{Cloud: "aws", Account: testAccount, InstallationID: testInstallation})
	assert.True(t, accountInHint(hint, testAccount))
}

func TestConnectedElsewhere(t *testing.T) {
	self := "3HzFPXfPDGhwLJJVtaHbmFs6vLa"
	other := "2ZaBcDeFgHiJkLmNoPqRsTuVwXy"
	hint := []cloudapi.ConnectedAccount{
		{Cloud: "aws", Account: testAccount, InstallationID: self, InstallationName: "prod"},
		{Cloud: "aws", Account: testAccount, InstallationID: other, InstallationName: "staging"},
		{Cloud: "gcp", Account: testAccount, InstallationID: other},
		{Cloud: "aws", Account: "999999999999", InstallationID: other},
	}

	got := connectedElsewhere(hint, testAccount, self)

	require.Len(t, got, 1)
	assert.Equal(t, other, got[0].InstallationID)
	assert.Equal(t, "staging", got[0].InstallationName)

	assert.Empty(t, connectedElsewhere(nil, testAccount, self))
}
