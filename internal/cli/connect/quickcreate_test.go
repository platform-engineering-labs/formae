// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"net/url"
	"strings"
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

// The single console link, byte for byte up to the pinned versionId (spliced
// from the constant so pin swaps stay constants-only changes). Everything
// after #/stacks/create/review rides in the URL fragment, so the whole
// ?templateURL=...&stackName=... query grammar is assembled inside the
// fragment string. CreateProvider is always explicit — the emitted link never
// depends on the template's default.
func TestBuildQuickCreatePlan_GoldenURL(t *testing.T) {
	plan := buildQuickCreatePlan(defaultPlatform(t), testSetup(), testAccount, testInstallation, options{})

	tplURL := "https://formae-connect-templates.s3.us-east-1.amazonaws.com/formae-connect-role.yaml?versionId=" +
		url.QueryEscape(roleTemplateVersionID)
	assert.Equal(t,
		"https://us-east-1.console.aws.amazon.com/cloudformation/home?region=us-east-1#/stacks/create/review"+
			"?templateURL="+url.QueryEscape(tplURL)+
			"&param_CreateProvider=true"+
			"&param_ExpectedAccountId="+testAccount+
			"&param_RoleName=formae-connect-"+testInstallation+
			"&param_Subject=fai%3Aacme%2F"+testInstallation+
			"&stackName=formae-connect-"+testInstallation,
		plan.StackURL)
}

// --provider-exists flips exactly the CreateProvider parameter.
func TestBuildQuickCreatePlan_ProviderExistsFlipsTheParameter(t *testing.T) {
	base := buildQuickCreatePlan(defaultPlatform(t), testSetup(), testAccount, testInstallation, options{})
	flipped := buildQuickCreatePlan(defaultPlatform(t), testSetup(), testAccount, testInstallation,
		options{ProviderExists: true})

	assert.False(t, base.CreateProvider == flipped.CreateProvider)
	assert.True(t, base.CreateProvider)
	assert.False(t, flipped.CreateProvider)
	assert.Equal(t,
		strings.Replace(base.StackURL, "param_CreateProvider=true", "param_CreateProvider=false", 1),
		flipped.StackURL)
}

func TestBuildQuickCreatePlan_CarriesTheFactsTheEmitNeeds(t *testing.T) {
	plan := buildQuickCreatePlan(defaultPlatform(t), testSetup(), testAccount, testInstallation, options{})

	assert.Equal(t, "formae-connect-"+testInstallation, plan.StackName)
	assert.Equal(t, "arn:aws:iam::"+testAccount+":role/"+testRoleName, plan.ExpectedRoleArn)
	assert.Equal(t, roleTemplateSHA256, plan.TemplateDigest)
	assert.Contains(t, plan.ProviderNote, "--provider-exists")
	assert.Contains(t, plan.CapabilityNote, "CAPABILITY_NAMED_IAM")
}

// The overridden template base flows into the link.
func TestBuildQuickCreatePlan_UsesTheOverriddenBase(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("FORMAE_CONNECT_ISSUER", "https://oidc.test.example")
	t.Setenv("FORMAE_CONNECT_TEMPLATE_BASE", "https://templates.test.example")
	p, err := resolveConnectPlatform()
	require.NoError(t, err)

	plan := buildQuickCreatePlan(p, testSetup(), testAccount, testInstallation, options{})

	assert.Contains(t, plan.StackURL, "templates.test.example")
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

// The human emit is one step: a single link plus the provider note, never the
// old two-step layout.
func TestPrintLinksHuman_SingleStep(t *testing.T) {
	plan := buildQuickCreatePlan(defaultPlatform(t), testSetup(), testAccount, testInstallation, options{})
	var out strings.Builder

	require.NoError(t, printLinksHuman(&out, plan))

	got := out.String()
	assert.Equal(t, 1, strings.Count(got, "#/stacks/create/review"), "exactly one console link")
	assert.Contains(t, got, plan.StackURL)
	assert.Contains(t, got, "--provider-exists")
	assert.NotContains(t, got, "Step 1")
	assert.NotContains(t, got, "Step 2")
	assert.NotContains(t, got, "skip step 1")
}

// Registration success states the registration and nothing more: the
// unverified nuance lives in the docs, not the happy-path output. The
// outcome line rides the shared ack idiom (styled on a TTY, plain piped).
func TestPrintRegisteredHuman_NoUnverifiedLine(t *testing.T) {
	var out strings.Builder
	v := registeredDocument(statusRegisteredUnverified, testAccount, "arn:aws:iam::"+testAccount+":role/r",
		[]string{"careful"})

	require.NoError(t, printRegisteredHuman(&out, false, nil, v, testInstallation))

	assert.Contains(t, out.String(), "✓ registered aws account "+testAccount)
	assert.Contains(t, out.String(), "! warning: careful")
	assert.NotContains(t, out.String(), "not verified")
	assert.NotContains(t, out.String(), "declared by you")
}
