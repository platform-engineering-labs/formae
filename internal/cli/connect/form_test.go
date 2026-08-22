// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"errors"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// The interactive slice, driven through the seams: the input form, the
// confirmations, and the in-sitting quick-create wait. No real TTY runs here;
// what is tested is that the answers produce the same runs the flag paths
// produce, and that nothing registers without the consents.

func interactiveTTY(t *testing.T) {
	t.Helper()
	restore := isInteractive
	isInteractive = func() bool { return true }
	t.Cleanup(func() { isInteractive = restore })
}

// formStub records what the form was offered and fills the values a user
// would have typed.
type formStub struct {
	called   int
	prefill  formValues
	profiles []string
	fill     func(v *formValues)
	err      error
}

func stubTheForm(t *testing.T, stub *formStub) {
	t.Helper()
	restore := runConnectFormFn
	runConnectFormFn = func(_ *theme.Theme, v *formValues, awsProfiles []string) error {
		stub.called++
		stub.prefill = *v
		stub.profiles = awsProfiles
		if stub.err != nil {
			return stub.err
		}
		if stub.fill != nil {
			stub.fill(v)
		}
		return nil
	}
	t.Cleanup(func() { runConnectFormFn = restore })
}

// confirmStub scripts the confirmation answers and records the prompts.
type confirmStub struct {
	prompts []string
	answers []bool
}

func stubConfirms(t *testing.T, answers ...bool) *confirmStub {
	t.Helper()
	stub := &confirmStub{answers: answers}
	restore := confirmFn
	confirmFn = func(_ *theme.Theme, title, description string) (bool, error) {
		stub.prompts = append(stub.prompts, title+"\n"+description)
		if len(stub.answers) == 0 {
			return true, nil
		}
		answer := stub.answers[0]
		stub.answers = stub.answers[1:]
		return answer, nil
	}
	t.Cleanup(func() { confirmFn = restore })
	return stub
}

func stubRoleArnPrompt(t *testing.T, arn string, err error) *int {
	t.Helper()
	calls := new(int)
	restore := promptRoleArnFn
	promptRoleArnFn = func(_ *theme.Theme, _ string) (string, error) {
		*calls++
		return arn, err
	}
	t.Cleanup(func() { promptRoleArnFn = restore })
	return calls
}

// Bare connect on a TTY runs the form, and the form's answers produce the
// same registration the flag path produces.
func TestFormAnswersDriveTheRegisterPath(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	stubConfirms(t, true)
	stub := &formStub{fill: func(v *formValues) {
		v.Cloud = "aws"
		v.Account = testAccount
		v.How = "role-arn"
		v.RoleArn = contractRoleArn
	}}
	stubTheForm(t, stub)

	out, err := runConnect(t)

	require.NoError(t, err, "out: %s", out)
	assert.Equal(t, 1, stub.called)
	posts := cp.posts()
	require.Len(t, posts, 1)
	assert.Contains(t, posts[0].Body, contractRoleArn)
	assert.Contains(t, out, "registered")
	assert.NotContains(t, out, "schemaVersion", "the form path is human output")
}

// A flag answers its question: the form receives the value pre-filled and the
// question is skipped (the form builder hides answered groups; the dispatch
// pins the pre-fill).
func TestFormFlagsPrefillTheirQuestions(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stub := &formStub{err: errors.New("stop here")}
	stubTheForm(t, stub)

	_, err := runConnect(t, "aws", "--account", testAccount)

	require.Error(t, err)
	assert.Equal(t, 1, stub.called)
	assert.Equal(t, testAccount, stub.prefill.Account, "the flag value pre-fills the form")
	assert.Equal(t, "aws", stub.prefill.Cloud, "entering through the aws subcommand answers the cloud question")
	assert.Empty(t, cp.requests())
}

// An invalid flag value fails before the TTY check and before any form.
func TestFormInvalidFlagValuesFailBeforeTheForm(t *testing.T) {
	interactiveTTY(t)
	stub := &formStub{}
	stubTheForm(t, stub)

	_, err := runConnect(t, "aws", "--account", "123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "12 digits")
	assert.Zero(t, stub.called, "a bad flag value never reaches the form")
}

// The confirmation renders the account, the subject, and the fixed permission
// line — "as applied" for a role the user brought themselves.
func TestFormConfirmationRendersTheFactsForRoleArn(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	confirms := stubConfirms(t, true)

	out, err := runConnect(t, "aws", "--account", testAccount, "--role-arn", contractRoleArn)

	require.NoError(t, err, "out: %s", out)
	require.Len(t, confirms.prompts, 1)
	assert.Contains(t, confirms.prompts[0], testAccount)
	assert.Contains(t, confirms.prompts[0], "fai:acme/"+contractInstallation)
	assert.Contains(t, confirms.prompts[0], "not verified by the CLI")
	require.Len(t, cp.posts(), 1)
}

// Declining the confirmation registers nothing.
func TestFormDecliningTheConfirmationRegistersNothing(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	stubConfirms(t, false)

	_, err := runConnect(t, "aws", "--account", testAccount, "--role-arn", contractRoleArn)

	require.Error(t, err)
	assert.Empty(t, cp.posts())
}

// Quick-create interactive: consent states the provisioned permissions, the
// links print, the wait accepts a pasted RoleArn, and the run registers it.
func TestFormQuickCreateWaitsAndRegistersThePastedArn(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	confirms := stubConfirms(t, true)
	waits := stubRoleArnPrompt(t, contractRoleArn, nil)

	out, err := runConnect(t, "aws", "--account", testAccount, "--quick-create")

	require.NoError(t, err, "out: %s", out)
	require.Len(t, confirms.prompts, 1)
	assert.Contains(t, confirms.prompts[0], "PowerUserAccess + IAM management")
	assert.Equal(t, 1, *waits)
	assert.Contains(t, out, "formae-oidc-provider", "the links print before the wait")
	assert.Contains(t, out, "registered")
	posts := cp.posts()
	require.Len(t, posts, 1)
	assert.Contains(t, posts[0].Body, contractRoleArn)
}

// A pasted ARN is validated exactly like --role-arn.
func TestFormQuickCreateValidatesThePastedArn(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	stubConfirms(t, true)
	stubRoleArnPrompt(t, "arn:aws:iam::999999999999:role/r", nil)

	_, err := runConnect(t, "aws", "--account", testAccount, "--quick-create")

	require.Error(t, err)
	assert.Empty(t, cp.posts(), "a mismatched paste registers nothing")
}

// Ctrl-C mid-wait prints the resume hint: the session is interrupted, not
// lost.
func TestFormQuickCreateCtrlCPrintsTheResumeHint(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	stubConfirms(t, true)
	stubRoleArnPrompt(t, "", huh.ErrUserAborted)

	out, err := runConnect(t, "aws", "--account", testAccount, "--quick-create")

	require.Error(t, err)
	assert.Contains(t, out, "--role-arn <RoleArn stack output>", "the resume command prints on interrupt")
	assert.Empty(t, cp.posts())
}

// The multi-installation warning is an explicit confirmation of its own, and
// declining it stops the run.
func TestFormMultiInstallationRequiresExplicitConfirmation(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	cp.setupBody = defaultSetupBody(t, []map[string]any{{
		"cloud":            "aws",
		"account":          testAccount,
		"installationId":   otherInstallation,
		"installationName": "staging",
		"tenantName":       "acme",
		"orgName":          "acme-inc",
	}})
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	confirms := stubConfirms(t, false)

	_, err := runConnect(t, "aws", "--account", testAccount, "--role-arn", contractRoleArn)

	require.Error(t, err)
	require.Len(t, confirms.prompts, 1, "the run stops at the declined warning")
	assert.Contains(t, confirms.prompts[0], "staging")
	assert.Empty(t, cp.posts())
}

// Machine mode never reaches a form: flags are consent.
func TestFormMachineModeNeverReachesTheForm(t *testing.T) {
	interactiveTTY(t)
	stub := &formStub{}
	stubTheForm(t, stub)

	_, err := runConnect(t, "aws", "--output-consumer", "machine", "--output-schema", "json")

	require.Error(t, err)
	assert.Zero(t, stub.called)
	assert.Contains(t, err.Error(), "--no-input")
}

// The form builder skips answered questions and offers the enumerated
// profiles.
func TestBuildConnectFormSkipsAnsweredQuestions(t *testing.T) {
	full := &formValues{Cloud: "aws", Account: testAccount, How: "role-arn", RoleArn: contractRoleArn}
	form := buildConnectForm(theme.New("formae"), full, []string{"dev"})
	assert.NotNil(t, form, "a fully answered form still builds (it asks nothing)")

	empty := &formValues{}
	form = buildConnectForm(theme.New("formae"), empty, []string{"dev"})
	assert.NotNil(t, form)
}
