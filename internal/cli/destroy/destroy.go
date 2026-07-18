// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package destroy

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/nag"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/prompter"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/errfmt"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/simview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/statuswatch"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/logging"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// ansiEscape matches ANSI SGR escape sequences for stripping display colors.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// isTerminal, launchSimView, launchWatch, and destroyFn are package-level vars so tests can stub them.
var (
	isTerminal = tui.IsTerminal

	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		model := simview.New(th, sim, opts)
		final, err := tui.Run(model, tui.DefaultRunOptions())
		if err != nil {
			return simview.DecisionAborted, err
		}
		return final.(simview.Model).Decision(), nil
	}

	launchWatch = func(a *app.App, commandID string) error {
		th := themeFor(a)
		model := statuswatch.New(th, a, statuswatch.Options{
			Query:          "id:" + commandID,
			FocusCommandID: commandID,
			ExitWhenDone:   true,
		})
		_, err := tui.Run(model, tui.DefaultRunOptions())
		return err
	}

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		return a.Destroy(opts.FormaFile, opts.Query, opts.Properties, simulate)
	}
)

// themeFor resolves the active theme from the app config.
// The name falls back to "formae" for nil configs (theme.New nil-guards internally).
func themeFor(a *app.App) *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

// legacyWidth is a package-level var so tests can stub it. Returns 100 for
// non-TTY output (piped/redirected) or the real terminal width for TTY.
var legacyWidth = func(w io.Writer) int {
	if !isTerminal(w) {
		return 100
	}
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return 100
}

// OnDependents defines the behavior when resources depend on those being deleted.
type OnDependents string

const (
	// OnDependentsAbort aborts the delete if dependent resources exist.
	OnDependentsAbort OnDependents = "abort"
	// OnDependentsCascade deletes dependent resources along with the target.
	OnDependentsCascade OnDependents = "cascade"
)

type DestroyOptions struct {
	FormaFile      string
	Query          string
	OutputConsumer printer.Consumer
	OutputSchema   string
	Watch          bool
	StatusOutput   status.StatusOutput
	Simulate       bool
	Yes            bool
	OnDependents   OnDependents
	Properties     map[string]string
}

func DestroyCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy all resources included with a forma",
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := &DestroyOptions{}
			opts.FormaFile = command.Flags().Arg(0)
			query, _ := command.Flags().GetString("query")
			opts.Query = strings.TrimSpace(query)
			outputConsumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(outputConsumer)
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")
			opts.Simulate, _ = command.Flags().GetBool("simulate")
			opts.Watch, _ = command.Flags().GetBool("watch")
			statusOutput, _ := command.Flags().GetString("status-output-layout")
			opts.StatusOutput = status.StatusOutput(statusOutput)
			opts.Yes, _ = command.Flags().GetBool("yes")
			onDependents, _ := command.Flags().GetString("on-dependents")
			opts.OnDependents = OnDependents(onDependents)
			opts.Properties = cmd.PropertiesFromCmd(command)

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runDestroy(app, opts)
		},
		Annotations: map[string]string{
			"type": "Forma",
			"examples": "formae destroy forma.pkl" +
				" | formae destroy --query 'stack:test-* managed:false'" +
				" | formae destroy --query 'type:AWS::S3::Bucket stack:scratch' --yes",
			"args": "<forma file>",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("query", " ", "Query that allows to find resources by their attributes. Only used when no forma file is provided. Use * as a wildcard anywhere (e.g. foo*, *foo, *foo*, foo*bar). ? and regex are not yet supported.")
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	command.Flags().Bool("simulate", false, "Simulate the command rather than make actual changes")
	command.Flags().Bool("watch", false, "Continuously refresh and print the status until completion")
	command.Flags().String("status-output-layout", string(status.StatusOutputSummary), fmt.Sprintf("What to print as status output (%s | %s)", status.StatusOutputSummary, status.StatusOutputDetailed))
	command.Flags().Bool("yes", false, "Allow the command to run without any confirmations")
	command.Flags().String("on-dependents", "abort", "Behavior when resources depend on those being deleted (abort | cascade)")
	cmd.AddConfigFlags(command)

	return command
}

func validateDestroyOptions(opts *DestroyOptions) error {
	if opts.FormaFile == "" && opts.Query == "" {
		return cmd.FlagErrorf("either a forma file needs to be provided, or --query must be specified")
	}
	if opts.FormaFile != "" && opts.Query != "" {
		return cmd.FlagErrorf("either a forma file needs to be provided, or --query must be specified, but not both")
	}
	if opts.OutputConsumer != printer.ConsumerHuman && opts.OutputConsumer != printer.ConsumerMachine {
		return cmd.FlagErrorf("output consumer must be either 'human' or 'machine'")
	}
	if opts.OutputConsumer == printer.ConsumerMachine {
		if opts.OutputSchema != "json" && opts.OutputSchema != "yaml" {
			return cmd.FlagErrorf("output schema must be either 'json' or 'yaml' for machine consumer")
		}
	}
	if opts.OnDependents != OnDependentsAbort && opts.OnDependents != OnDependentsCascade {
		return cmd.FlagErrorf("--on-dependents must be either 'abort' or 'cascade'")
	}

	return nil
}

func runDestroy(app *app.App, opts *DestroyOptions) error {
	err := validateDestroyOptions(opts)
	if err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runDestroyForHumans(app, opts)
	}
	return runDestroyForMachines(app, opts)
}

func runDestroyForHumans(app *app.App, opts *DestroyOptions) error {
	app.PrintBanner()

	// Interactive path: human + TTY + no --yes flag.
	if !opts.Yes && isTerminal(os.Stdout) {
		return runDestroyInteractive(app, opts)
	}
	return runDestroyLegacy(app, opts)
}

// runDestroyInteractive implements the new TTY destroy flow: simview preview → watch.
// Unlike apply there is no drift phase; the simview renders the cascade warning
// banner and the direct-vs-dependent confirm footer when cascade rows exist, and
// the interactive confirmation substitutes for --on-dependents=cascade.
func runDestroyInteractive(a *app.App, opts *DestroyOptions) error {
	th := themeFor(a)

	res, _, err := destroyFn(a, opts, true)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	if !res.Simulation.ChangesRequired {
		msg := "The specified forma does not have any resources that need to be destroyed."
		if opts.FormaFile == "" {
			msg = "The specified query does not match any resources that can be destroyed."
		}
		panel := components.Panel(th, th.Palette.Border, "formae destroy", []string{
			"No resources to destroy",
			"",
			msg,
		}, 80)
		fmt.Println(panel)
		return nil
	}

	source := opts.FormaFile
	if source == "" {
		source = "query: " + opts.Query
	}

	decision, err := launchSimView(th, &res.Simulation, simview.Options{
		Kind:         simview.KindDestroy,
		Source:       source,
		SimulateOnly: opts.Simulate,
		Description:  res.Description,
	})
	if err != nil {
		return err
	}

	if opts.Simulate {
		return nil
	}

	if decision == simview.DecisionAborted {
		fmt.Print(display.Grey("Destroy aborted.") + "\n")
		return nil
	}

	// Confirmed: run the real destroy.
	realRes, nags, err := destroyFn(a, opts, false)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	// Print one-line scrollback record.
	raw := components.PromptForOperations(&res.Simulation.Command)
	summary := ""
	if raw != "" {
		stripped := ansiEscape.ReplaceAllString(raw, "")
		summary = strings.SplitN(stripped, "\n\n", 2)[0]
		summary = strings.ReplaceAll(summary, "\n", " ")
	}
	fmt.Printf("Confirmed: %s — command %s submitted\n", summary, realRes.CommandID)

	// Watch the command to completion (D4: watch-by-default on TTY path).
	if err := launchWatch(a, realRes.CommandID); err != nil {
		return err
	}

	// Hint for users who detached with q before the command finished.
	fmt.Printf("\nRun the following command to check status:\n\n  formae status command --query='id:%s' --watch\n", realRes.CommandID)

	nag.MaybePrintNags(th, nags)

	return nil
}

// runDestroyLegacy is the pre-existing human destroy flow (non-TTY / --yes / legacy).
// Byte-identical to the old runDestroyForHumans minus the banner (which is now in
// runDestroyForHumans), except the cascade abort block which renders a styled
// panel (the issue-mandated D5 exception to the legacy-paths-unchanged rule).
func runDestroyLegacy(app *app.App, opts *DestroyOptions) error {
	if opts.FormaFile != "" {
		fmt.Print(display.Gold("Destroying resources defined by forma:\n ") + display.Green("File: ") + fmt.Sprintf("%s\n\n", opts.FormaFile))
	} else {
		fmt.Print(display.Gold("Destroying resources defined by query:\n ") + display.Green("Query: ") + fmt.Sprintf("%s\n\n", opts.Query))
	}

	res, _, err := destroyFn(app, opts, true)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	if !res.Simulation.ChangesRequired {
		var msg string
		if opts.FormaFile != "" {
			msg = display.Grey("The specified forma does not have any resources that need to be destroyed.")
		} else {
			msg = display.Grey("The specified query does not match any resources that can be destroyed.")
		}

		fmt.Printf("%s\n\n%s\n\n",
			display.Gold("No resources to destroy:"),
			msg)
		return nil
	}

	// Check for cascade deletes
	hasCascades := hasCascadeDeletes(&res.Simulation.Command)

	// If --yes is specified with --on-dependents=abort and there are cascades, abort.
	// The styled panel renders even on the --yes path — the issue-mandated D5
	// exception to the legacy-paths-unchanged rule (destroy-cascade mockup VIEW 2).
	if opts.Yes && hasCascades && opts.OnDependents == OnDependentsAbort {
		th := themeFor(app)

		var lines []string
		for _, ru := range res.Simulation.Command.ResourceUpdates {
			if ru.IsCascade {
				lines = append(lines, fmt.Sprintf("%s (%s)", ru.ResourceLabel, ru.ResourceType))
				lines = append(lines, fmt.Sprintf("  will be deleted because it depends on %s", ru.CascadeSource))
			}
		}
		for _, tu := range res.Simulation.Command.TargetUpdates {
			if tu.IsCascade {
				lines = append(lines, fmt.Sprintf("%s (target)", tu.TargetLabel))
				lines = append(lines, fmt.Sprintf("  will be deleted because it depends on %s", tu.CascadeSource))
			}
		}
		lines = append(lines, "", "To proceed, use --on-dependents=cascade")

		fmt.Println(components.Panel(th, th.Palette.Warning, "Command Aborted", lines, 80))
		return fmt.Errorf("cascade deletes detected, aborting (use --on-dependents=cascade to proceed)")
	}

	// don't show anything if --yes is specified
	if !opts.Yes {
		// Show warning about cascades before simulation output
		if hasCascades {
			fmt.Printf("%s\n\n", display.Gold("Warning: This operation will cascade delete additional resources."))
		}

		th := themeFor(app)
		width := legacyWidth(os.Stdout)
		_, _ = fmt.Println(simview.RenderSimulationPlain(th, &res.Simulation, width))
	}

	if opts.Simulate {
		fmt.Print(display.Grey("Command will not continue - simulation only\n"))
		return nil
	}

	// confirm with the user before proceeding (unless --yes is specified)
	prompter := prompter.NewBasicPrompter()
	prompt := components.PromptForOperations(&res.Simulation.Command)
	if !opts.Yes && !prompter.Confirm(prompt, false) {
		fmt.Print(display.Red("\nCommand aborted\n"))
		return nil
	}

	var nags []string
	res, nags, err = destroyFn(app, opts, false)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	fmt.Printf("\n%s\n", display.Gold("The asynchronous command has started on the formae agent."))

	if opts.Watch {
		query := fmt.Sprintf("id:%s", res.CommandID)
		return status.WatchCommandsStatus(app, query, 1, opts.StatusOutput)
	}

	fmt.Printf("\nRun the following command to check the status of this command:\n\n  %s%s%s\n",
		display.Grey("formae status command --query='id:"), display.LightBlue(res.CommandID), display.Grey("'"))

	nag.MaybePrintNags(themeFor(app), nags)

	return nil
}

func runDestroyForMachines(app *app.App, opts *DestroyOptions) error {
	if opts.Simulate {
		res, _, err := destroyFn(app, opts, true)
		if err != nil {
			return fmt.Errorf("error simlating destroy command: %v", err)
		}
		printer := printer.NewMachineReadablePrinter[apimodel.Simulation](os.Stdout, opts.OutputSchema)

		return printer.Print(&res.Simulation)
	}
	res, _, err := destroyFn(app, opts, false)
	if err != nil {
		return fmt.Errorf("error destroying forma: %v", err)
	}
	printer := printer.NewMachineReadablePrinter[apimodel.CommandID](os.Stdout, opts.OutputSchema)

	return printer.Print(&apimodel.CommandID{CommandID: res.CommandID})
}

// hasCascadeDeletes checks if any resource or target updates in the command are cascade deletes
func hasCascadeDeletes(cmd *apimodel.Command) bool {
	for _, ru := range cmd.ResourceUpdates {
		if ru.IsCascade {
			return true
		}
	}
	for _, tu := range cmd.TargetUpdates {
		if tu.IsCascade {
			return true
		}
	}
	return false
}
