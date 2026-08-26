// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
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
	// Template 0.2.0: the shared OIDC provider rides in the role template
	// behind CreateProvider, so quick-create emits exactly one link.
	roleTemplateKey       = "formae-connect-role.yaml"
	roleTemplateVersionID = "xxcZ8mU5TG82MLf5iyJrlmnB4i2wOZ_f"
	roleTemplateSHA256    = "12fe0ff79a73387c2e591fb0348558406f5b085917d879b91dc9b21de2306ee2"

	quickCreateConsole = "https://us-east-1.console.aws.amazon.com/cloudformation/home?region=us-east-1#/stacks/create/review"
)

// quickCreatePlan is everything the emit step prints and the machine document
// carries. Self-contained: a consumer needs no second command to act on it.
type quickCreatePlan struct {
	StackURL        string
	StackName       string // formae-connect-<installation KSUID>
	ExpectedRoleArn string
	TemplateDigest  string
	CreateProvider  bool
	ResumeCommand   string
	ProviderNote    string
	CapabilityNote  string
	Warnings        []string
}

// buildQuickCreatePlan assembles the console link and the facts around it.
// The console URL grammar is a query string carried inside the URL fragment:
// everything after #/stacks/create/review is one opaque string to the server
// and the console's client-side router reads it whole. CreateProvider is
// always explicit in the link, so the emitted URL never depends on the
// template's default.
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
	stack := "formae-connect-" + installationID
	createProvider := !opts.ProviderExists
	return quickCreatePlan{
		StackURL: quickCreateConsole +
			"?templateURL=" + url.QueryEscape(templateURL(roleTemplateKey, roleTemplateVersionID)) +
			frag(stack, map[string]string{
				"Subject":           setup.CloudSubject,
				"RoleName":          setup.CloudRoleName,
				"ExpectedAccountId": account,
				"CreateProvider":    fmt.Sprintf("%t", createProvider),
			}),
		StackName:       stack,
		ExpectedRoleArn: "arn:aws:iam::" + account + ":role/" + setup.CloudRoleName,
		TemplateDigest:  roleTemplateSHA256,
		CreateProvider:  createProvider,
		ResumeCommand:   resumeCommand(opts, account),
		ProviderNote: "If this account was connected to formae before, re-run with --provider-exists: " +
			"the shared identity provider already exists and the stack should create the role only.",
		CapabilityNote: "The stack requires the CAPABILITY_NAMED_IAM acknowledgement in the console.",
	}
}

// runQuickCreate is the --quick-create path: read the coordinates, assemble
// the console link, emit it, and — interactively — finish in the same
// sitting: Enter registers the expected ARN once the stack is applied, a
// pasted RoleArn wins when it differs. Non-interactively nothing is
// registered; the user comes back with --role-arn.
func runQuickCreate(cc *cobra.Command, opts options, consumer printer.Consumer, schema string) error {
	if opts.Account == "" {
		return clicmd.FlagErrorf("--quick-create requires --account")
	}

	s, err := openSession(cc.Context(), opts)
	if err != nil {
		return err
	}

	warnings := s.Warnings
	elsewhere := connectedElsewhere(s.Setup.AccountsConnectedHint, opts.Account, s.InstallationID)
	if len(elsewhere) > 0 {
		warnings = append(warnings, multiInstallationWarning(opts.Account, elsewhere))
	}

	plan := buildQuickCreatePlan(s.Platform, s.Setup, opts.Account, s.InstallationID, opts)
	plan.Warnings = warnings

	if consumer == printer.ConsumerMachine {
		return emitLinks(cc.OutOrStdout(), schema, linksDocument(plan, opts.Account, s.InstallationID, warnings))
	}
	if !interactiveRun(opts, consumer) {
		return printLinksHuman(cc.OutOrStdout(), plan)
	}

	// The in-sitting completion: consent, settle the provider question,
	// print the link, wait for Enter or a pasted RoleArn, validate it exactly
	// like --role-arn, and register.
	th := clicmd.ResolveConfiguredTheme(cc)
	if err := confirmInteractive(th, "aws", "account", opts.Account, s.Setup.CloudSubject, permissionsProvisioned, elsewhere); err != nil {
		return err
	}
	if !opts.ProviderExists && accountInHint(s.Setup.AccountsConnectedHint, opts.Account) {
		exists, err := confirmProviderExistsFn(th, opts.Account)
		if err != nil {
			return err
		}
		if exists {
			opts.ProviderExists = true
			plan = buildQuickCreatePlan(s.Platform, s.Setup, opts.Account, s.InstallationID, opts)
			plan.Warnings = warnings
		}
	}
	if err := printLinksHuman(cc.OutOrStdout(), plan); err != nil {
		return err
	}

	pasted, err := promptRoleArnFn(th, plan.ExpectedRoleArn)
	if err != nil {
		// An interrupt is a pause, not a loss: the resume command finishes
		// the session from a fresh shell.
		_, _ = fmt.Fprintln(cc.OutOrStdout(), "\nResume later with:\n  "+plan.ResumeCommand)
		return err
	}
	if pasted == "" {
		// Enter means "it applied and the output matches what you printed":
		// register the expected ARN.
		pasted = plan.ExpectedRoleArn
	}
	parsed, err := parseRoleArn(pasted, opts.Account)
	if err != nil {
		return err
	}
	if w := warnOnNameMismatch(parsed.RoleName, s.Setup.CloudRoleName); w != "" {
		warnings = append(warnings, w)
	}

	status, err := s.register(cc.Context(), opts.Account, parsed.Arn)
	if err != nil {
		return err
	}
	return printRegisteredHuman(cc.OutOrStdout(), true, th, registeredDocument(status, opts.Account, parsed.Arn, warnings), s.InstallationID)
}

// printLinksHuman renders the plan as one console step.
func printLinksHuman(w io.Writer, plan quickCreatePlan) error {
	intro := "One CloudFormation stack establishes the trust. Open the link, review, and create the stack (" + plan.StackName + "):"
	notes := []string{plan.CapabilityNote}
	if plan.CreateProvider {
		notes = append(notes, plan.ProviderNote)
	}
	lines := []string{
		intro,
		"  " + plan.StackURL,
		"  template sha256: " + plan.TemplateDigest,
		"",
	}
	lines = append(lines, notes...)
	lines = append(lines,
		"",
		"When the stack is applied, its RoleArn output should be:",
		"  "+plan.ExpectedRoleArn,
		"",
		"Finish by registering it:",
		"  "+plan.ResumeCommand,
	)
	for _, warning := range plan.Warnings {
		lines = append(lines, "", "warning: "+warning)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// resumeCommand prints the exact command that finishes an interrupted session,
// carrying the original --profile/--config selection verbatim: a fresh shell
// may have a different active profile.
func resumeCommand(opts options, account string) string {
	sel := ""
	switch {
	case opts.ProfileFlag != "":
		sel = " --profile " + shellQuote(opts.ProfileFlag)
	case opts.ConfigFlag != "":
		sel = " --config " + shellQuote(opts.ConfigFlag)
	}
	return "formae connect" + sel + " aws --account " + account + " --role-arn <RoleArn stack output>"
}

// shellQuote makes a value safe to paste into a POSIX shell. Plain
// flag-safe values pass through untouched so the common case stays
// readable; anything else is single-quoted with embedded quotes escaped.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`&|;()<>*?[]#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
