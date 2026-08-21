// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package command holds the `formae command` noun: the status of previously
// executed commands, split into a single-result `status` subcommand and a
// multi-result `list` subcommand. It supersedes the old `formae status
// command` verb, which remains as a deprecated alias in internal/cli/status
// for one release.
package command

import (
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
)

// CommandCmd is the `formae command` noun, grouping `status` (a single
// command by id, or the most recent one) and `list` (query-driven, many
// results).
func CommandCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "command",
		Short: "Receive the status of previously executed commands",
		Annotations: map[string]string{
			"type": "Information",
			"examples": "formae command status" +
				" | formae command status 3Hrx15wROBJnYK2T5oEXKErKMVf" +
				" | formae command list --query 'client:me'",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.AddCommand(
		StatusCmd(),
		ListCmd())

	return command
}
