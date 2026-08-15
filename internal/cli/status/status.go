// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Status command to query the status of one or more Forma commands.
package status

import (
	"fmt"
	"io"
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

// isTerminal, launchTUI, printBanner, and fetchCommandsStatus are package-level
// vars so tests can stub them.
var (
	isTerminal  = tui.IsTerminal
	launchTUI   = launchStatusTUI
	printBanner = func(a *app.App) { a.PrintBanner() }
	// fetchCommandsStatus is a seam so tests can drive the running/terminal
	// decision (interactive vs static) without a live agent.
	fetchCommandsStatus = func(a *app.App, query string, maxResults int) (*apimodel.ListCommandStatusResponse, []string, error) {
		return a.GetCommandsStatus(query, maxResults, false)
	}
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
	OutputLayout   StatusOutput
	MaxResults     int
	// Single marks a query as targeting exactly one command (the `command
	// status` path): on a TTY it re-attaches the live view while the match is
	// still running and prints a static summary once it's terminal, rather
	// than always opening the multi-command browse list.
	Single bool
	// CommandID optionally names the single command Single mode targets. It
	// may be supplied up front (an explicit `command status <id>`) or
	// discovered from the fetched result (a bare `command status` with no
	// argument) so the TUI can focus it.
	CommandID string
}

// RunStatus is the shared entry point the `command status` and `command
// list` subcommands (internal/cli/command) call into: it validates opts and
// dispatches to the human or machine rendering path.
func RunStatus(app *app.App, opts *StatusOptions) error {
	return runStatus(app, opts)
}

// ThemeFor exposes themeFor to callers outside this package (the `agent
// status` subcommand reuses it to theme the stats panels).
func ThemeFor(a *app.App) *theme.Theme {
	return themeFor(a)
}

// RenderAgentStats exposes renderAgentStats to callers outside this package
// (the `agent status` subcommand reuses it rather than re-implementing the
// panel layout).
func RenderAgentStats(th *theme.Theme, stats apimodel.Stats, width int) string {
	return renderAgentStats(th, stats, width)
}

// TermWidth exposes termWidth to callers outside this package.
func TermWidth(w io.Writer) int {
	return termWidth(w)
}

// WatchStats exposes watchStats to callers outside this package (the `agent
// status --watch` path reuses the same refresh loop).
func WatchStats(app *app.App) error {
	return watchStats(app)
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
			opts.MaxResults = maxResults

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
				" | formae status command --query 'stack:prod status:Success'",
		},
		// This verb moved to `formae command list` (and `formae command
		// status` for a single result). Kept as a working alias for one
		// release so existing scripts and muscle memory keep functioning.
		Deprecated:    "use `formae command list` instead",
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().String("query", "", "Query that allows to find past and current commands by their attributes. Use * as a wildcard anywhere (e.g. foo*, *foo, *foo*, foo*bar). ? and regex are not yet supported.")
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
	// Surface connection / auth / version-mismatch errors as ordinary CLI
	// errors before the alt-screen TUI takes over the terminal.
	if err := a.Preflight(); err != nil {
		return err
	}
	th := themeFor(a)
	swOpts := statuswatch.Options{
		Query:      opts.Query,
		MaxResults: opts.MaxResults,
		Version:    formae.Version,
	}
	// Single mode (`command status`) already knows which command it targets —
	// either the caller supplied it up front, or runStatusForHumans discovered
	// it from the fetch below — so drill straight into its detail view instead
	// of dropping the user in a one-row list they must "enter" into.
	if opts.Single && opts.CommandID != "" {
		swOpts.FocusCommandID = opts.CommandID
	}
	model := statuswatch.New(th, a, swOpts)
	_, err := tui.Run(model, tui.DefaultRunOptions())
	return err
}

func runStatusForHumans(a *app.App, opts *StatusOptions) error {
	// Human + TTY:
	//   - A browse query (`command list`) always opens the interactive list so
	//     you can navigate and drill into past commands.
	//   - Single mode (`command status`) is the "re-attach" path: open the live
	//     view only while the match is still running; once it's terminal, print
	//     a static one-shot summary instead of taking over the screen.
	if isTerminal(os.Stdout) {
		if opts.Single {
			status, nags, err := fetchCommandsStatus(a, opts.Query, opts.MaxResults)
			if err != nil {
				msg, renderErr := errfmt.Render(err)
				if renderErr != nil {
					return fmt.Errorf("error rendering error message: %v", renderErr)
				}
				return fmt.Errorf("%s", msg)
			}
			if !anyCommandRunning(status) {
				printBanner(a)
				_, _ = fmt.Print(renderStatusList(themeFor(a), status, opts.OutputLayout == StatusOutputDetailed, termWidth(os.Stdout)))
				nag.MaybePrintNags(themeFor(a), nags)
				return nil
			}
			// Still running: capture the matched command's id (it may not have
			// been supplied up front) so launchTUI can focus it.
			if opts.CommandID == "" && len(status.Commands) == 1 {
				opts.CommandID = status.Commands[0].CommandID
			}
		}
		return launchTUI(a, opts)
	}

	// Human + non-TTY → print-and-exit.
	printBanner(a)

	status, nags, err := fetchCommandsStatus(a, opts.Query, opts.MaxResults)
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

	return nil
}

// anyCommandRunning reports whether any command in the status response is still
// in progress (not Success/Failed/Canceled). It decides whether a human TTY
// status query opens the live watch view or prints a static summary.
func anyCommandRunning(status *apimodel.ListCommandStatusResponse) bool {
	if status == nil {
		return false
	}
	for _, c := range status.Commands {
		switch c.State {
		case "Success", "Failed", "Canceled":
			// terminal
		default:
			return true
		}
	}
	return false
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
		Annotations: map[string]string{},
		// This verb moved to `formae agent status`. Kept as a working alias
		// for one release so existing scripts and muscle memory keep
		// functioning.
		Deprecated:    "use `formae agent status` instead",
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

func prepareScreen(a *app.App, what string) {
	banner.ClearScreen()
	printBanner(a)
	fmt.Printf("Watching %s (refreshing every 2s)...\n\n", what)
}

func WatchCommandsStatus(app *app.App, query string, n int, outputLayout StatusOutput) error {
	var nags []string
	var status *apimodel.ListCommandStatusResponse
	var err error
	for {
		time.Sleep(2 * time.Second)

		prepareScreen(app, "commands status")
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

		prepareScreen(app, "agent status")
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
		// This noun split into `formae command` (status/list) and `formae
		// agent status`. Kept as a working alias for one release so existing
		// scripts and muscle memory keep functioning. Cobra does not
		// propagate Deprecated to children, so `status command` and `status
		// agent` each carry their own Deprecated string too (see CommandCmd
		// and AgentCmd).
		Deprecated:    "use `formae command` or `formae agent status` instead",
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.AddCommand(
		CommandCmd(),
		AgentCmd())

	return command
}
