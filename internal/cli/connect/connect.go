// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"errors"
	"strings"

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
			opts, err := readSelection(c)
			if err != nil {
				return err
			}
			return runConnectForm(c, opts, "")
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
	command.AddCommand(gcpCmd())
	command.AddCommand(listCmd())
	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	return command
}

// formValues is what the interactive form fills. Cloud is asked first: it is
// the discriminator every later question depends on.
type formValues struct {
	Cloud      string // "aws" or "gcp"
	Account    string // AWS
	Project    string // GCP
	How        string // "quick-create" | "profile" | "role-arn"
	ProfileAWS string
	RoleArn    string
	Confirmed  bool
}

// runConnectFormFn builds and runs the interactive form for the given values.
// A seam so the dispatcher is testable without a TTY.
var runConnectFormFn = func(th *theme.Theme, v *formValues, awsProfiles []string) error {
	return buildConnectForm(th, v, awsProfiles).Run()
}

// runConnectForm is the interactive dispatcher: it requires a TTY, runs the
// form through the seam with every flag-answered question pre-filled, and
// hands the answers to the same run the flag paths use. Machine mode never
// reaches it — flags are consent.
//
// cloud pins the cloud question: bare connect asks it first, while entering
// through `connect aws` has already answered it.
func runConnectForm(c *cobra.Command, opts options, cloud string) error {
	if !isInteractive() {
		return errors.New("connecting interactively requires a TTY; use `formae connect aws --no-input` " +
			"with --account and one of --quick-create, --profile-aws, --role-arn")
	}

	th := clicmd.ResolveConfiguredTheme(c)
	v := &formValues{Cloud: cloud, Account: opts.Account}
	if err := runConnectFormFn(th, v, awsProfileChoices()); err != nil {
		return err
	}

	if v.Cloud == "gcp" {
		// GCP has one path, so there is nothing else to ask: the project is
		// the whole answer, and the local path takes it from here.
		return runConnectGCPFn(c, gcpOptions{
			Project:     strings.TrimSpace(v.Project),
			ConfigFlag:  opts.ConfigFlag,
			ProfileFlag: opts.ProfileFlag,
		})
	}

	opts.Account = strings.TrimSpace(v.Account)
	if err := validateAccount(opts.Account); err != nil {
		return err
	}
	switch v.How {
	case "quick-create":
		opts.QuickCreate = true
	case "profile":
		opts.ProfileAWS = v.ProfileAWS
	case "role-arn":
		opts.RoleArn = v.RoleArn
	default:
		// A form that filled no path cannot dispatch; re-entering it would
		// loop instead of asking anything new.
		return errors.New("the form completed without choosing how to establish trust")
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

// awsProfileChoices returns the shared-config profiles the form offers. An
// unreadable shared config costs the form its choices, not the run: the
// profile question falls back to free entry.
func awsProfileChoices() []string {
	profiles, _ := listAWSProfiles()
	return profiles
}
