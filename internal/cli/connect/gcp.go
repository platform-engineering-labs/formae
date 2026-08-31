// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/oox/gcpname"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// gcpOptions is everything `connect gcp` decides from, read once off the flags.
type gcpOptions struct {
	Project                  string
	WorkloadIdentityProvider string
	NoInput                  bool
	// AllowLogin lets a machine-output caller opt into the interactive
	// sign-in. It exists because "machine output" conflates two different
	// consumers: a script that built one fixed command line and cannot answer
	// a browser, and an agent running on the operator's own machine with the
	// operator sitting in front of it. Only the first should be refused.
	AllowLogin bool

	ConfigFlag  string
	ProfileFlag string
}

// gcpMode is the path a GCP connect run takes.
//
// There are two, not three: GCP has no console path to offer. CloudFormation's
// quick-create URL is what makes the AWS link path possible, and GCP retired
// its only console-deployable template service, so the credential path is
// primary here rather than the fallback it is on AWS.
type gcpMode int

const (
	gcpModeLocal gcpMode = iota
	gcpModeRegisterOnly
)

// runConnectGCPFn is the seam structure tests observe the dispatch through.
var runConnectGCPFn func(cc *cobra.Command, opts gcpOptions) error

func init() { runConnectGCPFn = runConnectGCP }

func gcpCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "gcp",
		Short:         "Connect a GCP project",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cc *cobra.Command, args []string) error {
			opts, err := readGCPOptions(cc)
			if err != nil {
				return err
			}
			return runConnectGCPFn(cc, opts)
		},
	}
	c.Flags().String("project", "", "GCP project id to connect (always explicit, never inferred from ambient credentials)")
	c.Flags().String("workload-identity-provider", "",
		"Trust already exists (federation you provisioned yourself): validate the coordinate and register only")
	c.Flags().Bool("no-input", false,
		"Disable prompts this command renders; on its own it also declines the Google sign-in (see --allow-login)")
	c.Flags().Bool("allow-login", false,
		"Permit the Google sign-in to open a browser even with machine output; for a caller running beside the operator")
	clicmd.AddOutputFlags(c)
	c.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	return c
}

func readGCPOptions(cc *cobra.Command) (gcpOptions, error) {
	sel, err := readSelection(cc)
	if err != nil {
		return gcpOptions{}, err
	}
	opts := gcpOptions{ConfigFlag: sel.ConfigFlag, ProfileFlag: sel.ProfileFlag}
	opts.Project, _ = cc.Flags().GetString("project")
	opts.WorkloadIdentityProvider, _ = cc.Flags().GetString("workload-identity-provider")
	opts.NoInput, _ = cc.Flags().GetBool("no-input")
	opts.AllowLogin, _ = cc.Flags().GetBool("allow-login")
	return opts, nil
}

// decideGCPMode validates the flag set and picks the path.
func decideGCPMode(opts gcpOptions) (gcpMode, error) {
	if opts.Project == "" {
		return 0, clicmd.FlagErrorf("--project is required; the project is always explicit and never inferred from ambient credentials")
	}
	if opts.WorkloadIdentityProvider != "" {
		return gcpModeRegisterOnly, nil
	}
	return gcpModeLocal, nil
}

// runConnectGCP validates the flag set, picks the path, and runs it.
func runConnectGCP(cc *cobra.Command, opts gcpOptions) error {
	consumer, schema, err := resolveOutputOrHuman(cc)
	if err != nil {
		return err
	}

	mode, err := decideGCPMode(opts)
	if err != nil {
		var fe *clicmd.FlagError
		if errors.As(err, &fe) {
			return err
		}
		return report(cc.OutOrStdout(), consumer, schema, err, gcpFallbackMessage)
	}

	if err := runGCPMode(cc, mode, opts, consumer, schema); err != nil {
		return report(cc.OutOrStdout(), consumer, schema, err, gcpFallbackMessage)
	}
	return nil
}

const gcpFallbackMessage = "formae could not connect the project; run it without --output-consumer machine to see why"

func runGCPMode(cc *cobra.Command, mode gcpMode, opts gcpOptions, consumer printer.Consumer, schema string) error {
	switch mode {
	case gcpModeRegisterOnly:
		return runGCPRegisterOnly(cc, opts, consumer, schema)
	case gcpModeLocal:
		return runGCPLocal(cc, opts, consumer, schema)
	default:
		return errors.New("this connect path is not implemented yet")
	}
}

// gcpMayPrompt reports whether this run may open a browser on the operator's
// behalf.
//
// --allow-login is the explicit answer and wins outright, including under
// machine output. Without it the heuristic stands: a TTY, no --no-input, and a
// human consumer.
//
// The two are separate because "machine output" says how results are rendered,
// not whether a person is present. An agent running on the operator's own
// machine consumes machine output and still has someone there to complete a
// browser sign-in; a CI script does not. Reading the render format as an
// answer to that question made the sign-in unreachable from the interface
// most people use, which is the opposite of doing it for them.
//
// --allow-login also overrides --no-input, because the two govern different
// things: --no-input means this command renders no prompts of its own and
// reads nothing from stdin, while the sign-in is a browser gcloud opens. A
// caller can coherently want both, and the agent does - it cannot answer a
// terminal form but the person in front of it can click a consent screen.
func gcpMayPrompt(opts gcpOptions, consumer printer.Consumer) bool {
	if opts.AllowLogin {
		return true
	}
	if opts.NoInput {
		return false
	}
	return consumer != printer.ConsumerMachine && isInteractive()
}

// runGCPRegisterOnly validates the supplied coordinate and registers it.
//
// It deliberately holds no Google credentials, which is the entire reason this
// mode exists: it serves the organisation that will not hand a CLI
// provisioning rights and stands its own federation up with Terraform or
// gcloud. So it validates the coordinate's grammar and nothing else, and says
// so in its output rather than implying the connection was checked.
func runGCPRegisterOnly(cc *cobra.Command, opts gcpOptions, consumer printer.Consumer, schema string) error {
	name, err := gcpname.Parse(opts.WorkloadIdentityProvider)
	if err != nil {
		return printer.Fail(printer.CodeProvisionFailed,
			fmt.Sprintf("--workload-identity-provider is not a workload identity provider resource name: %v", err), nil)
	}

	s, err := openSession(cc.Context(), options{ConfigFlag: opts.ConfigFlag, ProfileFlag: opts.ProfileFlag})
	if err != nil {
		return err
	}

	warnings := append([]string{}, s.Warnings...)
	elsewhere := connectedElsewhere(s.Setup.AccountsConnectedHint, "gcp", opts.Project, s.InstallationID)
	if len(elsewhere) > 0 {
		warnings = append(warnings, multiInstallationWarning(opts.Project, elsewhere))
	}
	warnings = append(warnings, unverifiedCoordinateWarning)

	status, err := s.registerConnection(cc.Context(), cloudapi.CloudConnectionRegistration{
		Cloud:                    "gcp",
		Account:                  opts.Project,
		WorkloadIdentityProvider: name.String(),
	})
	if err != nil {
		return err
	}

	return emitGCPRegistered(cc, consumer, schema, status, opts.Project, name.String(), warnings, s.InstallationID)
}

// unverifiedCoordinateWarning states exactly what register-only did not check,
// so nobody reads "registered" as "working".
const unverifiedCoordinateWarning = "the coordinate was validated for shape only: formae did not check that this provider " +
	"exists, that it trusts the formae issuer, or that it grants this installation access. The first use is where a wrong one shows up"

// gcpLoginOutput is where the sign-in's own progress goes.
//
// Under machine output stdout carries the document and nothing else, so the
// sign-in reports on stderr instead. The caller that opts into the browser is
// the same one parsing stdout - the agent beside the operator - and gcloud's
// chatter ahead of the JSON is the difference between a connect it can read
// and one it cannot. In a human run stdout is prose already, so it stays put.
func gcpLoginOutput(cc *cobra.Command, consumer printer.Consumer) io.Writer {
	if consumer == printer.ConsumerMachine {
		return cc.ErrOrStderr()
	}
	return cc.OutOrStdout()
}

// runGCPLocal is the default path: formae obtains credentials, verifies the
// project, provisions the federation, and registers what it created.
func runGCPLocal(cc *cobra.Command, opts gcpOptions, consumer printer.Consumer, schema string) error {
	ctx := cc.Context()

	if err := ensureCredentials(ctx, gcpLoginOutput(cc, consumer), gcpMayPrompt(opts, consumer)); err != nil {
		return err
	}

	s, err := openSession(ctx, options{ConfigFlag: opts.ConfigFlag, ProfileFlag: opts.ProfileFlag})
	if err != nil {
		return err
	}

	warnings := append([]string{}, s.Warnings...)
	elsewhere := connectedElsewhere(s.Setup.AccountsConnectedHint, "gcp", opts.Project, s.InstallationID)
	if len(elsewhere) > 0 {
		warnings = append(warnings, multiInstallationWarning(opts.Project, elsewhere))
		// Two installations on one project share one trust domain, because
		// each holds near-owner and can rewrite the other's access. Say so
		// where the decision is being made.
		warnings = append(warnings, sharedTrustDomainWarning)
	}

	// The consent AWS has taken on all three of its paths since it shipped,
	// and the design's own requirement that the confirmation say what is being
	// granted in those terms. Provisioning hands a near-owner grant to another
	// party's installation; an interactive run stops and says so before any of
	// it happens.
	//
	// Gated on the strict interactivity test rather than on whether a sign-in
	// was permitted: --allow-login says a person can complete a browser flow,
	// not that this run can render a terminal prompt and read the answer.
	if !opts.NoInput && consumer != printer.ConsumerMachine && isInteractive() {
		th := clicmd.ResolveConfiguredTheme(cc)
		if err := confirmInteractive(th, "gcp", "project", opts.Project, s.Setup.CloudSubject,
			permissionsProvisionedGCP, elsewhere); err != nil {
			return err
		}
	}

	result, err := provisionGCP(ctx, opts.Project, s.Setup.CloudSubject, s.Platform.Issuer)
	if err != nil {
		return err
	}

	// The coordinate must belong to the project the operator named. Nothing in
	// the control plane can enforce this: its account field is an opaque
	// string and it cannot resolve a project id to a number. It is the direct
	// analogue of the rule AWS does enforce, that a role ARN's account matches
	// the stated account.
	if err := assertProviderBelongsToProject(result.ProviderName, result.ProjectNumber); err != nil {
		return err
	}

	status, err := s.registerConnection(ctx, cloudapi.CloudConnectionRegistration{
		Cloud:                    "gcp",
		Account:                  opts.Project,
		WorkloadIdentityProvider: result.ProviderName,
	})
	if err != nil {
		// Provisioning succeeded and registration did not, so the project now
		// grants access to an installation the control plane does not know
		// about. Re-running converges; saying so beats leaving it to be found.
		return fmt.Errorf("the project now trusts this installation, but the connection was not registered; "+
			"re-run this command to finish: %w", err)
	}

	return emitGCPRegistered(cc, consumer, schema, status, opts.Project, result.ProviderName, warnings, s.InstallationID)
}

const sharedTrustDomainWarning = "installations connected to one project share a trust domain: each is granted enough " +
	"access to change the project's IAM, including the other's"

func emitGCPRegistered(cc *cobra.Command, consumer printer.Consumer, schema, status, project, provider string,
	warnings []string, installationID string) error {
	v := gcpRegisteredDocument(status, project, provider, warnings)
	if consumer == printer.ConsumerMachine {
		return emitRegistered(cc.OutOrStdout(), schema, v)
	}
	return printRegisteredHuman(cc.OutOrStdout(), isInteractive(), clicmd.ResolveConfiguredTheme(cc), v, installationID)
}

// assertProviderBelongsToProject refuses a coordinate whose project number is
// not the one the stated project resolves to.
//
// Cross-project federation, where the trust-hosting project differs from the
// managed one, is legitimate in GCP and out of scope here: it is refused
// rather than silently accepted, because accepting it would register a
// connection whose two halves name different projects with nothing recording
// that they were meant to.
func assertProviderBelongsToProject(providerName, projectNumber string) error {
	name, err := gcpname.Parse(providerName)
	if err != nil {
		return printer.Fail(printer.CodeProvisionFailed,
			fmt.Sprintf("provisioning returned a provider name that is not canonical: %v", err), nil)
	}
	if name.ProjectNumber != projectNumber {
		return printer.Fail(printer.CodeAccountMismatch,
			"the workload identity provider belongs to a different project than the one named",
			map[string]any{"providerProjectNumber": name.ProjectNumber, "statedProjectNumber": projectNumber})
	}
	return nil
}
