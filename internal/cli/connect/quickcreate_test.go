// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
)

const (
	testInstallation = "3HzFPXfPDGhwLJJVtaHbmFs6vLa"
	testSubject      = "fai:acme/" + testInstallation
	testRoleName     = "formae-connect-" + testInstallation
)

func testSetup() cloudapi.CloudConnectionSetup {
	return cloudapi.CloudConnectionSetup{
		CloudSubject:  testSubject,
		CloudRoleName: testRoleName,
		Issuer:        ProductionIssuer,
	}
}

func defaultPlatform(t *testing.T) connectPlatform {
	t.Helper()
	clearConnectEnv(t)
	p, err := resolveConnectPlatform()
	require.NoError(t, err)
	return p
}

// The two console links, byte for byte. Everything after #/stacks/create/review
// rides in the URL fragment, so the whole ?templateURL=...&stackName=... query
// grammar is assembled inside the fragment string.
func TestBuildQuickCreatePlan_GoldenURLs(t *testing.T) {
	plan := buildQuickCreatePlan(defaultPlatform(t), testSetup(), testAccount, testInstallation, options{})

	assert.Equal(t,
		"https://us-east-1.console.aws.amazon.com/cloudformation/home?region=us-east-1#/stacks/create/review"+
			"?templateURL=https%3A%2F%2Fformae-connect-templates.s3.us-east-1.amazonaws.com"+
			"%2Fformae-oidc-provider.yaml%3FversionId%3DGCkhsMGjUAKV_qs7m7uYl6j5879bUjgu"+
			"&stackName=formae-oidc-provider",
		plan.ProviderStackURL)

	assert.Equal(t,
		"https://us-east-1.console.aws.amazon.com/cloudformation/home?region=us-east-1#/stacks/create/review"+
			"?templateURL=https%3A%2F%2Fformae-connect-templates.s3.us-east-1.amazonaws.com"+
			"%2Fformae-connect-role.yaml%3FversionId%3DNpQAD3Vxf_JcswPJ4VuSSBoUp0gY2.uq"+
			"&param_ExpectedAccountId="+testAccount+
			"&param_RoleName=formae-connect-"+testInstallation+
			"&param_Subject=fai%3Aacme%2F"+testInstallation+
			"&stackName=formae-connect-"+testInstallation,
		plan.RoleStackURL)
}

func TestBuildQuickCreatePlan_CarriesTheFactsTheEmitNeeds(t *testing.T) {
	plan := buildQuickCreatePlan(defaultPlatform(t), testSetup(), testAccount, testInstallation, options{})

	assert.Equal(t, "formae-connect-"+testInstallation, plan.RoleStackName)
	assert.Equal(t, "arn:aws:iam::"+testAccount+":role/"+testRoleName, plan.ExpectedRoleArn)
	assert.Equal(t, providerTemplateSHA256, plan.ProviderDigest)
	assert.Equal(t, roleTemplateSHA256, plan.RoleDigest)
	assert.Contains(t, plan.SkipStepOne, "skip step 1")
	assert.Contains(t, plan.CapabilityNote, "CAPABILITY_NAMED_IAM")
}

// The overridden template base flows into both links: the pair moves together.
func TestBuildQuickCreatePlan_UsesTheOverriddenBase(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("FORMAE_CONNECT_ISSUER", "https://oidc.test.example")
	t.Setenv("FORMAE_CONNECT_TEMPLATE_BASE", "https://templates.test.example")
	p, err := resolveConnectPlatform()
	require.NoError(t, err)

	plan := buildQuickCreatePlan(p, testSetup(), testAccount, testInstallation, options{})

	assert.Contains(t, plan.ProviderStackURL, "templates.test.example")
	assert.Contains(t, plan.RoleStackURL, "templates.test.example")
}

// The resume hint carries the original profile selection verbatim: a fresh
// shell may have a different active profile.
func TestResumeCommand_RoundTripsTheSelection(t *testing.T) {
	assert.Equal(t,
		"formae connect aws --account "+testAccount+" --role-arn <RoleArn stack output>",
		resumeCommand(options{}, testAccount))

	assert.Equal(t,
		"formae connect --profile staging aws --account "+testAccount+" --role-arn <RoleArn stack output>",
		resumeCommand(options{ProfileFlag: "staging"}, testAccount))

	assert.Equal(t,
		"formae connect --config /tmp/x.pkl aws --account "+testAccount+" --role-arn <RoleArn stack output>",
		resumeCommand(options{ConfigFlag: "/tmp/x.pkl"}, testAccount))
}

// A pasted resume command must survive the shell: selections containing
// spaces or metacharacters are quoted, and embedded single quotes escape.
func TestResumeCommand_QuotesUnsafeSelections(t *testing.T) {
	assert.Equal(t,
		"formae connect --config '/tmp/my configs/x.pkl' aws --account "+testAccount+" --role-arn <RoleArn stack output>",
		resumeCommand(options{ConfigFlag: "/tmp/my configs/x.pkl"}, testAccount))

	assert.Equal(t,
		`formae connect --config '/tmp/it'\''s.pkl' aws --account `+testAccount+" --role-arn <RoleArn stack output>",
		resumeCommand(options{ConfigFlag: "/tmp/it's.pkl"}, testAccount))

	assert.Equal(t,
		"formae connect --profile 'two words' aws --account "+testAccount+" --role-arn <RoleArn stack output>",
		resumeCommand(options{ProfileFlag: "two words"}, testAccount))
}

func TestBuildQuickCreatePlan_ResumeCommandRidesThePlan(t *testing.T) {
	plan := buildQuickCreatePlan(defaultPlatform(t), testSetup(), testAccount, testInstallation,
		options{ProfileFlag: "staging"})

	assert.Contains(t, plan.ResumeCommand, "--profile staging")
	assert.Contains(t, plan.ResumeCommand, "--account "+testAccount)
}
