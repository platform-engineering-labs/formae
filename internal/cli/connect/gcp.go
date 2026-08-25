// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"errors"
	"fmt"

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
	c.Flags().Bool("no-input", false, "Disable prompts; requires --project, and will not sign in for you")
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
// behalf. Machine output and --no-input both mean no: a caller that built one
// fixed command line did not consent to an interactive sign-in.
func gcpMayPrompt(opts gcpOptions, consumer printer.Consumer) bool {
	return !opts.NoInput && consumer != printer.ConsumerMachine && isInteractive()
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
	elsewhere := connectedElsewhere(s.Setup.AccountsConnectedHint, opts.Project, s.InstallationID)
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

// runGCPLocal is the default path: formae obtains credentials, verifies the
// project, provisions the federation, and registers what it created.
func runGCPLocal(cc *cobra.Command, opts gcpOptions, consumer printer.Consumer, schema string) error {
	ctx := cc.Context()

	if err := ensureCredentials(ctx, cc.OutOrStdout(), gcpMayPrompt(opts, consumer)); err != nil {
		return err
	}

	s, err := openSession(ctx, options{ConfigFlag: opts.ConfigFlag, ProfileFlag: opts.ProfileFlag})
	if err != nil {
		return err
	}

	warnings := append([]string{}, s.Warnings...)
	elsewhere := connectedElsewhere(s.Setup.AccountsConnectedHint, opts.Project, s.InstallationID)
	if len(elsewhere) > 0 {
		warnings = append(warnings, multiInstallationWarning(opts.Project, elsewhere))
		// Two installations on one project share one trust domain, because
		// each holds near-owner and can rewrite the other's access. Say so
		// where the decision is being made.
		warnings = append(warnings, sharedTrustDomainWarning)
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
