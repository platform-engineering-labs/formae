// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/logging"
)

// defaultListMaxResults is `command list`'s --max-results default.
const defaultListMaxResults = 50

// resolveListMaxResults turns the raw --max-results flag value into the page
// size actually sent to the server:
//   - 0 means "no limit was meaningfully specified" and resolves to the
//     server-side ceiling (datastore.MaxFormaCommandsQueryLimit). This must
//     happen here, before the statuswatch TUI model is constructed: its
//     constructor rewrites any MaxResults <= 0 to 10, so 0 would otherwise
//     silently become a 10-result page instead of "as many as the server
//     allows".
//   - a negative value is a usage error, not something to default away.
//   - any positive value passes through unchanged (the server itself clamps
//     to the ceiling).
func resolveListMaxResults(n int) (int, error) {
	if n < 0 {
		return 0, cmd.FlagErrorf("--max-results must not be negative")
	}
	if n == 0 {
		return datastore.MaxFormaCommandsQueryLimit, nil
	}
	return n, nil
}

// listHeaderCommand is the TUI header shown for `command list` — the verb
// the user actually typed, distinct from the deprecated `status command`
// alias's own header (internal/cli/status.compatHeaderCommand).
const listHeaderCommand = "command list"

// newListOptions builds the starting StatusOptions for `command list`,
// carrying the TUI header so it can be exercised directly by tests without a
// live agent. RunE fills in the remaining flag-derived fields on top.
func newListOptions() *status.StatusOptions {
	return &status.StatusOptions{HeaderCommand: listHeaderCommand}
}

// ListCmd is `command list`: a multi-result view over past and current
// commands. It is the direct successor of the old `status command` verb.
//
// With no --query it lists the agent's most recent user commands, newest
// first, across every client (`--query 'client:me'` narrows it to this
// client's own). Only user-initiated commands are ever listed; scheduler
// bookkeeping is not part of the user-facing command history.
func ListCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List previously executed commands",
		PreRun: func(command *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := newListOptions()

			consumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			query, _ := command.Flags().GetString("query")
			opts.Query = strings.TrimSpace(query)

			maxResults, _ := command.Flags().GetInt("max-results")
			resolved, err := resolveListMaxResults(maxResults)
			if err != nil {
				return err
			}
			opts.MaxResults = resolved

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
			"examples": "formae command list" +
				" | formae command list --query 'status:InProgress' --max-results 10" +
				" | formae command list --query 'client:me command:apply'" +
				" | formae command list --query 'stack:prod status:Success'",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().String("query", "", "Query that allows to find past and current commands by their attributes. Use * as a wildcard anywhere (e.g. foo*, *foo, *foo*, foo*bar). ? and regex are not yet supported.")
	command.Flags().String("output-layout", string(status.StatusOutputSummary), fmt.Sprintf("What to print as status output (%s | %s)", status.StatusOutputSummary, status.StatusOutputDetailed))
	command.Flags().Int("max-results", defaultListMaxResults, "Maximum number of command results to return")
	cmd.AddConfigFlags(command)

	return command
}
