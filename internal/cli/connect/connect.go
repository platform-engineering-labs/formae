// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"errors"

	"github.com/spf13/cobra"

	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// isInteractive reports whether the command runs on a TTY. A seam so the
// dispatch between the form and the flag paths is testable without one.
var isInteractive = tui.IsInteractive

// ConnectCmd connects a cloud account to a hosted installation.
func ConnectCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "connect",
		Short: "Connect a cloud account to your hosted installation",
		Annotations: map[string]string{
			"type":     "Auth",
			"examples": "{{.Name}} {{.Command}}|{{.Name}} {{.Command}} aws --account 123456789012 --quick-create",
		},
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			// Bare connect is the interactive form, cloud asked first.
			return runConnectForm(c)
		},
	}
	// --config/--profile are persistent so the resume hint's flag placement
	// (`formae connect --profile <p> aws ...`) parses, and so the bare form
	// and every cloud subcommand share one selection. Their exclusivity is
	// validated in the run path, because MarkFlagsMutuallyExclusive annotates
	// a single command's own flag set.
	command.PersistentFlags().String("config", "", "Path to config file")
	command.PersistentFlags().String("profile", "", "Named profile to use (see `formae profile list`)")
	command.AddCommand(awsCmd())
	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	return command
}

// formValues is what the interactive form fills. Cloud is asked first: it is
// the discriminator every later question depends on.
type formValues struct {
	Cloud      string // "aws" is the only option today; the select still renders
	Account    string
	How        string // "quick-create" | "profile" | "role-arn"
	ProfileAWS string
	RoleArn    string
	Confirmed  bool
}

// runConnectFormFn builds and runs the interactive form for the given values.
// A seam so the dispatcher is testable without a TTY; the form itself lands
// with the interactive slice, so until then the body says so.
var runConnectFormFn = func(th *theme.Theme, v *formValues, awsProfiles []string) error {
	return errors.New("the interactive connect form is not yet implemented; " +
		"run `formae connect aws` with --account and one of --quick-create, --profile-aws, --role-arn")
}

// runConnectForm is the bare-connect dispatcher: it requires a TTY, runs the
// form through the seam, and hands the answers to the same run the flag paths
// use. Machine mode never reaches it — flags are consent.
func runConnectForm(c *cobra.Command) error {
	opts, err := readSelection(c)
	if err != nil {
		return err
	}
	if !isInteractive() {
		return errors.New("connecting interactively requires a TTY; use `formae connect aws --no-input` " +
			"with --account and one of --quick-create, --profile-aws, --role-arn")
	}

	th := clicmd.ResolveConfiguredTheme(c)
	v := &formValues{Cloud: "aws"}
	if err := runConnectFormFn(th, v, awsProfileChoices()); err != nil {
		return err
	}

	opts.Account = v.Account
	switch v.How {
	case "quick-create":
		opts.QuickCreate = true
	case "profile":
		opts.ProfileAWS = v.ProfileAWS
	case "role-arn":
		opts.RoleArn = v.RoleArn
	}
	return runConnectAWSFn(c, opts)
}

// readSelection reads the shared --config/--profile selection and validates
// its exclusivity.
func readSelection(c *cobra.Command) (options, error) {
	configFlag, _ := c.Flags().GetString("config")
	profileFlag, _ := c.Flags().GetString("profile")
	if configFlag != "" && profileFlag != "" {
		return options{}, clicmd.FlagErrorf("--config and --profile are one selection; pass at most one")
	}
	return options{ConfigFlag: configFlag, ProfileFlag: profileFlag}, nil
}

// awsProfileChoices returns the shared-config profiles the form offers. The
// enumeration lands with the local path; until then the form has no choices
// to offer.
func awsProfileChoices() []string { return nil }
