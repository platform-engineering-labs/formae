// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"net/url"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
)

// The published template coordinates: content-addressed keys AND pinned S3
// versionIds (Object Lock protects a version, not a key; the publish test in
// the infrastructure repo proves a same-key rewrite cannot change what these
// fetch). The SHA-256 digests are printed as an audit aid.
//
// Filled from the infrastructure repo's pinned publication before this slice
// ships; the golden-URL tests assemble from whatever they hold, so swapping
// the values is a constants-only change. A releasecheck-tagged gate test
// fails the release pipeline while any placeholder remains.
const (
	providerStackName         = "formae-oidc-provider"
	providerTemplateKey       = "formae-oidc-provider.yaml"
	providerTemplateVersionID = "PINNED_AT_PUBLICATION"
	providerTemplateSHA256    = "PINNED_AT_PUBLICATION"

	roleTemplateKey       = "formae-connect-role.yaml"
	roleTemplateVersionID = "PINNED_AT_PUBLICATION"
	roleTemplateSHA256    = "PINNED_AT_PUBLICATION"

	quickCreateConsole = "https://us-east-1.console.aws.amazon.com/cloudformation/home?region=us-east-1#/stacks/create/review"
)

// quickCreatePlan is everything the emit step prints and the machine document
// carries. Self-contained: a consumer needs no second command to act on it.
type quickCreatePlan struct {
	ProviderStackURL string
	RoleStackURL     string
	RoleStackName    string // formae-connect-<installation KSUID>
	ExpectedRoleArn  string
	ProviderDigest   string
	RoleDigest       string
	ResumeCommand    string
	SkipStepOne      string
	CapabilityNote   string
	Warnings         []string
}

// buildQuickCreatePlan assembles the two console links and the facts around
// them. The console URL grammar is a query string carried inside the URL
// fragment: everything after #/stacks/create/review is one opaque string to
// the server and the console's client-side router reads it whole.
func buildQuickCreatePlan(p connectPlatform, setup cloudapi.CloudConnectionSetup,
	account, installationID string, opts options) quickCreatePlan {

	templateURL := func(key, versionID string) string {
		return p.TemplateBase + "/" + key + "?versionId=" + url.QueryEscape(versionID)
	}
	frag := func(stackName string, params map[string]string) string {
		v := url.Values{}
		v.Set("stackName", stackName)
		for k, val := range params {
			v.Set("param_"+k, val)
		}
		return "&" + v.Encode()
	}
	roleStack := "formae-connect-" + installationID
	return quickCreatePlan{
		ProviderStackURL: quickCreateConsole +
			"?templateURL=" + url.QueryEscape(templateURL(providerTemplateKey, providerTemplateVersionID)) +
			frag(providerStackName, nil),
		RoleStackURL: quickCreateConsole +
			"?templateURL=" + url.QueryEscape(templateURL(roleTemplateKey, roleTemplateVersionID)) +
			frag(roleStack, map[string]string{
				"Subject":           setup.CloudSubject,
				"RoleName":          setup.CloudRoleName,
				"ExpectedAccountId": account,
			}),
		RoleStackName:   roleStack,
		ExpectedRoleArn: "arn:aws:iam::" + account + ":role/" + setup.CloudRoleName,
		ProviderDigest:  providerTemplateSHA256,
		RoleDigest:      roleTemplateSHA256,
		ResumeCommand:   resumeCommand(opts, account),
		SkipStepOne:     "If this account was connected before, the identity provider already exists — skip step 1.",
		CapabilityNote:  "Both stacks require the CAPABILITY_NAMED_IAM acknowledgement in the console.",
	}
}

// resumeCommand prints the exact command that finishes an interrupted session,
// carrying the original --profile/--config selection verbatim: a fresh shell
// may have a different active profile.
func resumeCommand(opts options, account string) string {
	sel := ""
	switch {
	case opts.ProfileFlag != "":
		sel = " --profile " + opts.ProfileFlag
	case opts.ConfigFlag != "":
		sel = " --config " + opts.ConfigFlag
	}
	return "formae connect" + sel + " aws --account " + account + " --role-arn <RoleArn stack output>"
}
