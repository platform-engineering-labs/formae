// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cancel

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/prompter"
	"github.com/platform-engineering-labs/formae/internal/cli/renderer"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	"github.com/platform-engineering-labs/formae/internal/logging"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

type CancelOptions struct {
	Query          string
	Force          bool
	Yes            bool
	Watch          bool
	StatusOutput   status.StatusOutput
	OutputConsumer printer.Consumer
	OutputSchema   string
}

func CancelCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel in-progress commands",
		Long: `Cancel commands that are currently in progress.

If no query is provided, cancels the most recent command.
If a query is provided, cancels all in-progress commands matching the query.

Note: Only commands in 'InProgress' state can be canceled.
Commands that are already executing resources will complete those resources
before transitioning to 'Canceled' state to avoid orphaned resources.

Use --force to abandon in-progress work and drive the command to a terminal
'Canceled' state immediately, instead of waiting for in-progress resources to
finish. This is an escape hatch for operations that will not complete (e.g. a
plugin stuck in an unbounded poll loop). With --force:
  - Cloud-side operations may keep running after the command is canceled.
  - Update/Delete operations are self-healing: the synchronizer reconciles
    formae's state against actual cloud state on its next cycle.
  - A still-running Create may orphan a cloud resource that formae cannot track
    (it has no native id yet). You may need to clean it up manually, or let
    discovery pick it up.`,
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := &CancelOptions{}
			query, _ := command.Flags().GetString("query")
			opts.Query = strings.TrimSpace(query)
			opts.Force, _ = command.Flags().GetBool("force")
			opts.Yes, _ = command.Flags().GetBool("yes")
			opts.Watch, _ = command.Flags().GetBool("watch")
			statusOutput, _ := command.Flags().GetString("status-output-layout")
			opts.StatusOutput = status.StatusOutput(statusOutput)
			outputConsumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(outputConsumer)
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			return runCancel(app, opts)
		},
		Annotations: map[string]string{
			"type": "Command",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("query", "", "Query to select commands to cancel. If not provided, cancels the most recent command. Use * as a wildcard anywhere (e.g. foo*, *foo, *foo*, foo*bar). ? and regex are not yet supported.")
	command.Flags().Bool("force", false, "Abandon in-progress work and drive the command to a terminal 'Canceled' state immediately, instead of waiting for in-progress resources to finish. Cloud-side operations may continue: Update/Delete are reconciled by the synchronizer, but a still-running Create may orphan a resource that needs manual cleanup.")
	command.Flags().Bool("yes", false, "Allow the command to run without any confirmations")
	command.Flags().BoolP("watch", "w", false, "Watch the status of canceled commands until they complete")
	command.Flags().String("status-output-layout", string(status.StatusOutputSummary), fmt.Sprintf("What to print as status output (%s | %s)", status.StatusOutputSummary, status.StatusOutputDetailed))
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "yaml", "The schema to use for the result output (json | yaml)")
	cmd.AddConfigFlags(command)

	return command
}

func Validate(opts *CancelOptions) error {
	if opts.OutputConsumer != printer.ConsumerHuman && opts.OutputConsumer != printer.ConsumerMachine {
		return cmd.FlagErrorf("output consumer must be either 'human' or 'machine'")
	}
	if opts.OutputConsumer == printer.ConsumerMachine {
		if opts.OutputSchema != "json" && opts.OutputSchema != "yaml" {
			return cmd.FlagErrorf("output schema must be either 'json' or 'yaml' for machine consumer")
		}
	}

	return nil
}

func runCancel(app *app.App, opts *CancelOptions) error {
	err := Validate(opts)
	if err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runCancelForHumans(app, opts)
	}
	return runCancelForMachines(app, opts)
}

func runCancelForHumans(app *app.App, opts *CancelOptions) error {
	app.PrintBanner()

	// A plain cancel is safe (it waits for in-progress resources to finish). A
	// --force cancel is a destructive escape hatch: it abandons in-progress work,
	// can leave cloud-side operations running, and may orphan resources. Confirm
	// before proceeding unless --yes was given.
	if opts.Force && !opts.Yes {
		target := "the most recent in-progress command"
		if opts.Query != "" {
			target = fmt.Sprintf("all in-progress commands matching %q", opts.Query)
		}
		prompt := fmt.Sprintf(
			"%s\n\n"+
				"This abandons in-progress work and drives %s straight to 'Canceled' "+
				"without waiting for running resources to finish.\n\n"+
				"  • Cloud-side operations already in flight may keep running.\n"+
				"  • A still-running Create can orphan a resource formae cannot track — manual cleanup may be needed.\n"+
				"  • Update/Delete operations are reconciled by the synchronizer on its next cycle.\n\n"+
				"Only use this for operations that will not complete on their own.\n\n"+
				"Force-cancel anyway?",
			display.Gold("Warning: --force is a destructive escape hatch."),
			target,
		)
		if !prompter.NewBasicPrompter().Confirm(prompt, false) {
			fmt.Print(display.Red("\nCommand aborted\n"))
			return nil
		}
	}

	res, err := app.CancelCommand(opts.Query, opts.Force)
	if err != nil {
		msg, renderErr := renderer.RenderErrorMessage(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	p := printer.NewHumanReadablePrinter[apimodel.CancelCommandResponse](os.Stdout)
	err = p.Print(res, printer.PrintOptions{})
	if err != nil {
		return err
	}

	// If no commands were canceled, nothing to watch
	if res == nil || len(res.CommandIDs) == 0 {
		return nil
	}

	if opts.Watch {
		fmt.Println() // Add spacing before watch output

		// For single command, watch by ID
		if len(res.CommandIDs) == 1 {
			query := fmt.Sprintf("id:%s", res.CommandIDs[0])
			return status.WatchCommandsStatus(app, query, 1, opts.StatusOutput)
		}

		// For multiple commands, watch without filter to see all recent commands
		// (which will include all the canceling/canceled commands)
		return status.WatchCommandsStatus(app, "", len(res.CommandIDs), opts.StatusOutput)
	}

	// Show how query the status of the canceled commands
	if len(res.CommandIDs) == 1 {
		query := fmt.Sprintf("id:%s", res.CommandIDs[0])
		fmt.Printf("\nRun the following command to check the status of this command:\n\n  %s%s%s\n",
			display.Grey("formae status command --query='"), display.LightBlue(query), display.Grey("'"))
	} else {
		// For multiple commands, list individual command IDs
		fmt.Printf("\nRun the following commands to check the status of each canceled command:\n\n")
		for _, cmdID := range res.CommandIDs {
			fmt.Printf("  %s%s%s\n",
				display.Grey("formae status command --query='id:"), display.LightBlue(cmdID), display.Grey("'"))
		}
	}

	return nil
}

func runCancelForMachines(app *app.App, opts *CancelOptions) error {
	res, err := app.CancelCommand(opts.Query, opts.Force)
	if err != nil {
		return fmt.Errorf("error canceling commands: %v", err)
	}

	printer := printer.NewMachineReadablePrinter[apimodel.CancelCommandResponse](os.Stdout, opts.OutputSchema)

	return printer.Print(res)
}
