// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Status command to query the status of one or more Forma commands.
package status

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/banner"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/nag"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/errfmt"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/statuswatch"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/logging"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/spf13/cobra"
)

// isTerminal, launchTUI, and printBanner are package-level vars so tests can stub them.
var (
	isTerminal  = tui.IsTerminal
	launchTUI   = launchStatusTUI
	printBanner = func(a *app.App) { a.PrintBanner() }
)

type StatusOutput string

const (
	StatusOutputDetailed StatusOutput = "detailed"
	StatusOutputSummary  StatusOutput = "summary"
)

type StatusOptions struct {
	OutputConsumer printer.Consumer
	OutputSchema   string
	Query          string
	Watch          bool
	OutputLayout   StatusOutput
	MaxResults     int
}

func CommandCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "command",
		Short: "Receive the status of previously executed commands",
		PreRun: func(command *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := &StatusOptions{}
			consumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")
			query, _ := command.Flags().GetString("query")
			maxResults, _ := command.Flags().GetInt("max-results")
			opts.Query = strings.TrimSpace(query)

			humanTTY := opts.OutputConsumer == printer.ConsumerHuman && isTerminal(os.Stdout)
			opts.MaxResults = resolveMaxResults(opts.Query, maxResults, humanTTY)

			opts.Watch, _ = command.Flags().GetBool("watch")
			outputLayout, _ := command.Flags().GetString("output-layout")
			opts.OutputLayout = StatusOutput(outputLayout)

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runStatus(app, opts)
		},
		Annotations: map[string]string{
			"examples": "formae status command --query 'status:InProgress' --max-results 10" +
				" | formae status command --query 'client:me command:apply'" +
				" | formae status command --query 'stack:prod status:Success'" +
				" | formae status command --watch",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().String("query", " ", "Query that allows to find past and current commands by their attributes. Use * as a wildcard anywhere (e.g. foo*, *foo, *foo*, foo*bar). ? and regex are not yet supported.")
	command.Flags().Bool("watch", false, "Continuously refresh and print the status until completion")
	command.Flags().String("output-layout", string(StatusOutputSummary), fmt.Sprintf("What to print as status output (%s | %s)", StatusOutputSummary, StatusOutputDetailed))
	command.Flags().Int("max-results", 10, "Maximum number of command results to return when using a query")
	cmd.AddConfigFlags(command)

	return command
}

func runStatus(app *app.App, opts *StatusOptions) error {
	err := validateStatusOptions(opts)
	if err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runStatusForHumans(app, opts)
	}
	return runStatusForMachines(app, opts)
}

func validateStatusOptions(options *StatusOptions) error {
	if options.OutputConsumer != printer.ConsumerHuman && options.OutputConsumer != printer.ConsumerMachine {
		return cmd.FlagErrorf("output consumer must be either 'human' or 'machine'")
	}
	if options.OutputConsumer == printer.ConsumerMachine {
		if options.OutputSchema != "json" && options.OutputSchema != "yaml" {
			return cmd.FlagErrorf("output schema must be either 'json' or 'yaml' for machine consumer")
		}
	}
	if options.OutputLayout != StatusOutputDetailed && options.OutputLayout != StatusOutputSummary {
		return cmd.FlagErrorf("output layout must be either 'detailed' or 'summary'")
	}

	return nil
}

// resolveMaxResults returns the correct page-size for the given context.
// When the caller is a human with a TTY (humanTTY == true), or when a query
// string is provided, the full flagValue is used so the multi-command view is
// populated. Otherwise (non-TTY or machine consumer) we collapse to 1 to
// preserve the pre-TUI behaviour of showing only the most-recent command.
func resolveMaxResults(query string, flagValue int, humanTTY bool) int {
	if humanTTY || strings.TrimSpace(query) != "" {
		return flagValue
	}
	return 1
}

// themeFor resolves the active theme from the app config.
// The name falls back to "formae" for nil configs (theme.New nil-guards internally).
func themeFor(a *app.App) *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

// launchStatusTUI starts the interactive status/watch TUI.
// The theme name comes from the CLI profile configuration (Config.Cli.Theme);
// unknown names fall back to "formae" inside theme.New.
func launchStatusTUI(a *app.App, opts *StatusOptions) error {
	th := themeFor(a)
	swOpts := statuswatch.Options{
		Query:      opts.Query,
		MaxResults: opts.MaxResults,
		Version:    formae.Version,
	}
	// If the query targets a single command by exact id (e.g. `status command
	// --query 'id:<ksuid>'`), drill straight into its detail view instead of
	// dropping the user in a one-row list they must "enter" into.
	if id := bareCommandID(opts.Query); id != "" {
		swOpts.FocusCommandID = id
	}
	model := statuswatch.New(th, a, swOpts)
	_, err := tui.Run(model, tui.DefaultRunOptions())
	return err
}

// bareCommandID returns the command id when query is exactly "id:<value>" with
// no wildcards or additional terms; otherwise it returns "".
func bareCommandID(query string) string {
	rest, ok := strings.CutPrefix(strings.TrimSpace(query), "id:")
	if !ok || rest == "" || strings.ContainsAny(rest, " *?") {
		return ""
	}
	return rest
}

func runStatusForHumans(a *app.App, opts *StatusOptions) error {
	// Human + TTY → interactive TUI (owns the whole screen; banner suppressed).
	if isTerminal(os.Stdout) {
		return launchTUI(a, opts)
	}

	// Human + non-TTY → existing print-and-exit path, completely unchanged.
	printBanner(a)

	status, nags, err := a.GetCommandsStatus(opts.Query, opts.MaxResults, false)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	// Render summary or detailed layout via the lipgloss print function.
	_, _ = fmt.Print(renderStatusList(themeFor(a), status, opts.OutputLayout == StatusOutputDetailed, termWidth(os.Stdout)))

	// print nags
	nag.MaybePrintNags(themeFor(a), nags)

	if opts.Watch {
		return WatchCommandsStatus(a, opts.Query, opts.MaxResults, opts.OutputLayout)
	}

	return nil
}

func runStatusForMachines(app *app.App, opts *StatusOptions) error {
	status, _, err := app.GetCommandsStatus(opts.Query, opts.MaxResults, false)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	p := printer.NewMachineReadablePrinter[apimodel.ListCommandStatusResponse](os.Stdout, opts.OutputSchema)
	err = p.Print(status)
	if err != nil {
		return err
	}

	return nil
}

func AgentCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "agent",
		Short: "Receive the agent status",
		RunE: func(command *cobra.Command, args []string) error {
			_consumer, _ := command.Flags().GetString("output-consumer")
			consumer := printer.Consumer(_consumer)
			schema, _ := command.Flags().GetString("output-schema")
			watch, _ := command.Flags().GetBool("watch")
			switch consumer {
			case printer.ConsumerMachine:
				if schema != "json" && schema != "yaml" {
					return fmt.Errorf("unsupported schema: %s", schema)
				}
			}

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			if consumer == printer.ConsumerHuman {
				app.PrintBanner()
			}

			stats, nags, err := app.Stats()
			if err != nil {
				return err
			}

			// if machine consumer, create machine printer, print and return nil
			if consumer == printer.ConsumerMachine {
				p := printer.NewMachineReadablePrinter[apimodel.Stats](os.Stdout, schema)
				err = p.Print(stats)
				if err != nil {
					return err
				}
				return nil
			}

			fmt.Println(renderAgentStats(themeFor(app), *stats, termWidth(os.Stdout)))

			if consumer != printer.ConsumerMachine && !watch {
				nag.MaybePrintNags(themeFor(app), nags)
			}

			if watch && consumer == printer.ConsumerHuman { // machine consumer can't watch
				nag.MaybePrintNags(themeFor(app), nags)
				return watchStats(app)
			}

			return nil
		},
		Annotations:   map[string]string{},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().Bool("watch", false, "Continuously refresh and print the status until completion")
	cmd.AddConfigFlags(command)

	return command
}

func renderCommandsStatus(a *app.App, status *apimodel.ListCommandStatusResponse, outputLayout StatusOutput) error {
	_, _ = fmt.Println(renderStatusList(themeFor(a), status, outputLayout == StatusOutputDetailed, termWidth(os.Stdout)))
	return nil
}

func prepareScreen(what string) {
	banner.ClearScreen()
	banner.PrintBanner()
	fmt.Printf("Watching %s (refreshing every 2s)...\n\n", what)
}

func WatchCommandsStatus(app *app.App, query string, n int, outputLayout StatusOutput) error {
	var nags []string
	var status *apimodel.ListCommandStatusResponse
	var err error
	for {
		time.Sleep(2 * time.Second)

		prepareScreen("commands status")
		status, nags, err = app.GetCommandsStatus(query, n, true)
		if err != nil {
			return err
		}

		// render detailed or summary
		err = renderCommandsStatus(app, status, outputLayout)
		if err != nil {
			return err
		}

		allFinished := true
		for _, cmdStatus := range status.Commands {
			if cmdStatus.State != "Success" && cmdStatus.State != "Failed" && cmdStatus.State != "Canceled" {
				allFinished = false
				break
			}
		}

		if allFinished {
			break
		}
	}

	nag.MaybePrintNags(themeFor(app), nags)

	return nil
}

func watchStats(app *app.App) error {
	for {
		time.Sleep(2 * time.Second)

		prepareScreen("agent status")
		stats, _, err := app.Stats()
		if err != nil {
			return err
		}

		fmt.Println(renderAgentStats(themeFor(app), *stats, termWidth(os.Stdout)))
	}
}

func StatusCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Various status retrieval commands",
		Annotations: map[string]string{
			"type": "Information",
			"examples": "formae status agent" +
				" | formae status command --query 'status:InProgress'" +
				" | formae status command --query 'client:me'",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.AddCommand(
		CommandCmd(),
		AgentCmd())

	return command
}
