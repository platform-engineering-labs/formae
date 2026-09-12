// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package command

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/spf13/cobra"
)

// LogCmd presents recorded intent separately from the live status watcher.
func LogCmd() *cobra.Command {
	var query string
	var count int
	var oneline bool
	command := &cobra.Command{
		Use: "log", Short: "Show recorded command intent and outcomes",
		Long: "Show the most recent user commands and their recorded inputs. Results are bounded by --max-count; scheduler observations are not included.",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if count < 1 || count > datastore.MaxFormaCommandsQueryLimit {
				return cmd.FlagErrorf("--max-count must be between 1 and %d", datastore.MaxFormaCommandsQueryLimit)
			}
			consumer, schema, err := cmd.ResolveOutput(command)
			if err != nil {
				return err
			}
			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}
			result, _, err := app.GetCommandsStatusScoped(strings.TrimSpace(query), count, false, apimodel.CommandScopeAgent)
			if err != nil {
				return err
			}
			if consumer == printer.ConsumerMachine {
				return printer.NewMachineReadablePrinter[apimodel.ListCommandStatusResponse](command.OutOrStdout(), schema).Print(result)
			}
			return renderCommandLog(command.OutOrStdout(), result.Commands, oneline)
		},
		SilenceErrors: true,
	}
	command.Flags().StringVar(&query, "query", "", "Filter recorded commands (for example stack:production user:me status:Failed)")
	command.Flags().IntVarP(&count, "max-count", "n", 50, "Maximum recent commands to return (1–200)")
	command.Flags().BoolVar(&oneline, "oneline", false, "Compact output; omit input-property details")
	cmd.AddOutputFlags(command)
	cmd.AddConfigFlags(command)
	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)
	return command
}

func logText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
}

func renderCommandLog(w io.Writer, commands []apimodel.Command, oneline bool) error {
	var out bytes.Buffer
	for _, command := range commands {
		user := ""
		if command.Subject != "" {
			user = command.SubjectName
			if user == "" {
				user = command.Subject
			}
		}
		if oneline {
			fmt.Fprintf(&out, "%s %s", logText(command.CommandID), logText(command.State))
			if user != "" {
				fmt.Fprintf(&out, " [%s]", logText(user))
			}
			fmt.Fprintf(&out, " %s\n", logText(command.Message))
			continue
		}
		fmt.Fprintf(&out, "Command: %s\n", logText(command.CommandID))
		if user != "" {
			fmt.Fprintf(&out, "User: %s\n", logText(user))
		}
		fmt.Fprintf(&out, "Message: %s\n", logText(command.Message))
		if len(command.InputProperties) == 0 {
			fmt.Fprintln(&out, "Input properties: unavailable")
		} else if strings.TrimSpace(string(command.InputProperties)) == "{}" {
			fmt.Fprintln(&out, "Input properties: none (explicit empty)")
		} else {
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, command.InputProperties, "  ", "  "); err != nil {
				return fmt.Errorf("invalid recorded inputs for command %s: %w", command.CommandID, err)
			}
			fmt.Fprintf(&out, "Input properties: %s\n", pretty.String())
		}
		if summary := components.AcceptanceSummary(&command); summary != "" {
			fmt.Fprintln(&out, summary)
		}
		fmt.Fprintf(&out, "Outcome: %s\n\n", logText(command.State))
	}
	_, err := w.Write(out.Bytes())
	return err
}
