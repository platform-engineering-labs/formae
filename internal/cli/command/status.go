// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package command

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	"github.com/platform-engineering-labs/formae/internal/logging"
)

// statusHeaderCommand is the TUI header shown for `command status` — the
// verb the user actually typed, distinct from the deprecated `status command`
// alias's own header (internal/cli/status.compatHeaderCommand).
const statusHeaderCommand = "command status"

// newStatusOptions builds the StatusOptions for `command status` from its
// optional positional id argument. It is the single place that decides the
// single-command semantics (id, query, FailIfNotFound) and the TUI header, so
// it can be exercised directly by tests without a live agent.
func newStatusOptions(args []string) *status.StatusOptions {
	opts := &status.StatusOptions{Single: true, MaxResults: 1, HeaderCommand: statusHeaderCommand}

	if len(args) == 1 {
		opts.CommandID = args[0]
		opts.Query = "id:" + args[0]
		// An explicitly-named id must exist: fail loudly rather than
		// silently printing "(no commands)" for a typo'd or unknown id.
		opts.FailIfNotFound = true
	}

	return opts
}

// StatusCmd is `command status`: it returns a single command by definition,
// so it takes an optional positional id instead of a query, and offers
// neither --query nor --max-results. With no id it returns the most recently
// executed command.
func StatusCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "status [id]",
		Short: "Receive the status of a single command",
		Args:  cobra.MaximumNArgs(1),
		PreRun: func(command *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := newStatusOptions(args)

			consumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			outputLayout, _ := command.Flags().GetString("output-layout")
			opts.OutputLayout = status.StatusOutput(outputLayout)

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return status.RunStatus(app, opts)
		},
		Annotations: map[string]string{
			"examples": "formae command status" +
				" | formae command status 3Hrx15wROBJnYK2T5oEXKErKMVf",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().String("output-layout", string(status.StatusOutputSummary), fmt.Sprintf("What to print as status output (%s | %s)", status.StatusOutputSummary, status.StatusOutputDetailed))
	cmd.AddConfigFlags(command)

	return command
}
