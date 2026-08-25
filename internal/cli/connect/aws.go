// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// runConnectAWSFn is the entrypoint the aws subcommand and the form dispatch
// share. A seam so structure tests can observe the dispatch; restored via
// t.Cleanup. Assigned in init because the form dispatch refers back to it,
// which a var initializer would read as a cycle.
var runConnectAWSFn func(cc *cobra.Command, opts options) error

func init() { runConnectAWSFn = runConnectAWS }

func awsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "aws",
		Short:         "Connect an AWS account",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cc *cobra.Command, args []string) error {
			opts, err := readOptions(cc)
			if err != nil {
				return err
			}
			return runConnectAWSFn(cc, opts)
		},
	}
	c.Flags().String("account", "", "AWS account id to connect (12 digits; always explicit, never inferred)")
	c.Flags().Bool("quick-create", false, "Emit the CloudFormation console link; interactively, finish by registering in the same sitting")
	c.Flags().Bool("provider-exists", false, "The account was connected to formae before: the shared OIDC identity provider already exists, so the stack creates the role only")
	c.Flags().String("role-arn", "", "Trust already exists (an applied quick-create stack, or a role you made yourself): validate and register only")
	c.Flags().String("profile-aws", "", "Provision directly with a local AWS shared-config profile, then register")
	c.Flags().String("region", "", "AWS region for the local path (flag > profile region > us-east-1; quick-create has no region input)")
	c.Flags().Bool("no-input", false, "Disable prompts; requires --account and one of --quick-create, --profile-aws, --role-arn")
	clicmd.AddOutputFlags(c)
	c.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	c.AddCommand(profilesCmd())
	return c
}

// readOptions turns the parsed flags into the options the flow decides from,
// including the persistent --config/--profile selection.
func readOptions(cc *cobra.Command) (options, error) {
	opts, err := readSelection(cc)
	if err != nil {
		return options{}, err
	}
	opts.Account, _ = cc.Flags().GetString("account")
	opts.QuickCreate, _ = cc.Flags().GetBool("quick-create")
	opts.ProviderExists, _ = cc.Flags().GetBool("provider-exists")
	opts.ProfileAWS, _ = cc.Flags().GetString("profile-aws")
	opts.RoleArn, _ = cc.Flags().GetString("role-arn")
	opts.Region, _ = cc.Flags().GetString("region")
	opts.NoInput, _ = cc.Flags().GetBool("no-input")
	return opts, nil
}

// runConnectAWS validates the flag set, picks the path, and runs it.
func runConnectAWS(cc *cobra.Command, opts options) error {
	consumer, schema, err := resolveOutputOrHuman(cc)
	if err != nil {
		// The output flags decide how a failure is rendered, so a failure to
		// read them cannot be rendered that way.
		return err
	}

	m, err := decideMode(opts, isInteractive())
	if err != nil {
		var fe *clicmd.FlagError
		if errors.As(err, &fe) {
			// An argv mistake shows usage, never an envelope: a consumer builds
			// one fixed command line and gets the exit status.
			return err
		}
		return report(cc.OutOrStdout(), consumer, schema, err, awsFallbackMessage)
	}
	if m == modeForm && consumer == printer.ConsumerMachine {
		// Machine mode never reaches a form: flags are consent.
		return errors.New("machine output cannot drive the interactive form; use --no-input with --account " +
			"and one of --quick-create, --profile-aws, --role-arn")
	}

	if err := runMode(cc, m, opts, consumer, schema); err != nil {
		return report(cc.OutOrStdout(), consumer, schema, err, awsFallbackMessage)
	}
	return nil
}

// awsFallbackMessage is the internal-code text an undeclared error gets on
// the aws paths.
const awsFallbackMessage = "formae could not connect the account; run it without --output-consumer machine to see why"

// runMode runs the decided path. The paths land slice by slice; one that has
// not landed yet says so rather than pretending.
func runMode(cc *cobra.Command, m mode, opts options, consumer printer.Consumer, schema string) error {
	switch m {
	case modeForm:
		return runConnectForm(cc, opts, "aws")
	case modeRegisterOnly:
		return runRegisterOnly(cc, opts, consumer, schema)
	case modeQuickCreate:
		return runQuickCreate(cc, opts, consumer, schema)
	case modeLocal:
		return runLocal(cc, opts, consumer, schema)
	default:
		return errors.New("this connect path is not implemented yet")
	}
}

// interactiveRun reports whether a run may prompt: a TTY, no --no-input, and
// a human consumer.
func interactiveRun(opts options, consumer printer.Consumer) bool {
	return !opts.NoInput && consumer != printer.ConsumerMachine && isInteractive()
}

// resolveOutputOrHuman resolves the output flags where they exist. The form
// dispatch re-enters this run through the parent command, which carries no
// output flags: that path is human by construction, because machine mode
// never reaches a form.
func resolveOutputOrHuman(cc *cobra.Command) (printer.Consumer, string, error) {
	if cc.Flags().Lookup("output-consumer") == nil {
		return printer.ConsumerHuman, "json", nil
	}
	return clicmd.ResolveOutput(cc)
}

// report renders err and returns it so the process still exits non-zero.
//
// Every error raised while the command runs becomes an envelope, not only the
// ones the flow declares: a consumer parses one protocol or it parses none,
// and the paths where that matters most are the degraded ones nobody
// anticipated. An error we did not declare is reported as internal rather
// than given a code that would imply we understood it. fallbackMessage is
// the text for that case; each caller names its own, since "could not connect
// the account" is wrong for a run that only reads.
func report(w io.Writer, consumer printer.Consumer, schema string, err error, fallbackMessage string) error {
	if consumer != printer.ConsumerMachine {
		return err
	}
	handled, perr := printer.PrintFailure(w, schema, err)
	if perr != nil {
		return perr
	}
	if !handled {
		// err.Error() deliberately does not travel: an undeclared error here
		// can quote configuration source, and a Pkl failure quotes the line it
		// failed on — which can be the line holding an inline password.
		if _, perr := printer.PrintFailure(w, schema, printer.Fail(printer.CodeInternal,
			fallbackMessage, nil)); perr != nil {
			return perr
		}
	}
	return err
}
