// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package apply

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/nag"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/errfmt"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/simview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/statuswatch"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ansiEscape matches ANSI SGR escape sequences for stripping display colors.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Package-level seams — replaced in tests to avoid TTY / network calls.
var (
	isInteractive = tui.IsInteractive
	runConfirm    = components.RunConfirm
)

// errDescriptionAborted is a sentinel returned by maybePrintDescription when
// the user declines the R3 acknowledgment. The call site converts this to a
// clean nil return (matching the operation-confirm decline behaviour).
var errDescriptionAborted = errors.New("description acknowledgment declined")

// isTerminal, launchSimView, launchWatch, and applyFn are package-level vars so tests can stub them.
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

	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		return a.Apply(opts.FormaFile, opts.Properties, opts.Mode, simulate, opts.Force)
	}
)

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

// themeFor resolves the active theme from the app config.
// The name falls back to "formae" for nil configs (theme.New nil-guards internally).
func themeFor(a *app.App) *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

type ApplyCommand struct {
	FormaFile string
}

type ApplyOptions struct {
	OutputConsumer printer.Consumer
	FormaFile      string
	Mode           pkgmodel.FormaApplyMode
	Force          bool
	Yes            bool
	Simulate       bool
	OutputSchema   string
	StatusOutput   status.StatusOutput
	Watch          bool
	Properties     map[string]string
}

func ApplyCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "apply",
		Short: "Apply a forma",
		RunE: func(command *cobra.Command, args []string) error {
			opts := &ApplyOptions{}
			consumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.FormaFile = command.Flags().Arg(0)
			mode, _ := command.Flags().GetString("mode")
			opts.Mode = pkgmodel.FormaApplyMode(mode)
			opts.Force, _ = command.Flags().GetBool("force")
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")
			opts.Simulate, _ = command.Flags().GetBool("simulate")
			opts.Watch, _ = command.Flags().GetBool("watch")
			statusOutput, _ := command.Flags().GetString("status-output-layout")
			opts.StatusOutput = status.StatusOutput(statusOutput)
			opts.Yes, _ = command.Flags().GetBool("yes")
			opts.Properties = cmd.PropertiesFromCmd(command)

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runApply(app, opts)
		},
		Annotations: map[string]string{
			"type":     "Forma",
			"examples": "{{.Name}} {{.Command}} --mode reconcile forma.pkl  |  {{.Name}} {{.Command}} --mode patch forma.pkl",
			"args":     "<forma file>",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("mode", "", "Apply mode (reconcile | patch). This flag is required.")
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	command.Flags().Bool("simulate", false, "Simulate the command rather than make actual changes")
	command.Flags().Bool("force", false, "Overwrite any changes since the last reconcile without prompting. Only applicable in 'reconcile' mode.")
	command.Flags().Bool("watch", false, "Continuously refresh and print the status until completion")
	command.Flags().String("status-output-layout", string(status.StatusOutputSummary), fmt.Sprintf("What to print as status output (%s | %s)", status.StatusOutputSummary, status.StatusOutputDetailed))
	command.Flags().Bool("yes", false, "Allow the command to run without any confirmations")
	cmd.AddConfigFlags(command)

	return command
}

func runApply(app *app.App, opts *ApplyOptions) error {
	err := validateApplyOptions(opts)
	if err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runApplyForHumans(app, opts)
	}
	return runApplyForMachines(app, opts)
}

func validateApplyOptions(opts *ApplyOptions) error {
	if opts.FormaFile == "" {
		return cmd.FlagErrorf("forma file is required")
	}
	if opts.Mode == "" {
		return cmd.FlagErrorf("the --mode flag is required")
	}
	if opts.Mode != pkgmodel.FormaApplyModeReconcile && opts.Mode != pkgmodel.FormaApplyModePatch {
		return cmd.FlagErrorf("invalid mode: %s. Should be either reconcile or patch", opts.Mode)
	}
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

func runApplyForHumans(a *app.App, opts *ApplyOptions) error {
	a.PrintBanner()

	// Interactive path: human + TTY + no --yes flag.
	if !opts.Yes && isTerminal(os.Stdout) {
		return runApplyInteractive(a, opts)
	}
	return runApplyLegacy(a, opts)
}

// runApplyInteractive implements the new TTY apply flow: simview preview → watch.
func runApplyInteractive(a *app.App, opts *ApplyOptions) error {
	th := themeFor(a)

	res, _, err := applyFn(a, opts, true)
	if err != nil {
		if reconcileErr, ok := err.(*apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]); ok {
			return runDriftFlow(a, th, opts, reconcileErr.Data)
		}
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	if !res.Simulation.ChangesRequired {
		panel := components.Panel(th, th.Palette.Border, "formae apply", []string{
			"No changes needed",
			"",
			"The specified forma resources are up to date.",
		}, 80)
		fmt.Println(panel)
		return nil
	}

	decision, err := launchSimView(th, &res.Simulation, simview.Options{
		Kind:         simview.KindApply,
		Mode:         string(opts.Mode),
		Source:       opts.FormaFile,
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
		fmt.Print(display.Grey("Apply aborted.") + "\n")
		return nil
	}

	// Confirmed: run the real apply.
	realRes, nags, err := applyFn(a, opts, false)
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

	nag.MaybePrintNags(themeFor(a), nags)

	return nil
}

// runApplyLegacy is the pre-existing human apply flow (non-TTY / --yes / legacy).
// Byte-identical to the old runApplyForHumans minus the banner (which is now in
// runApplyForHumans).
func runApplyLegacy(a *app.App, opts *ApplyOptions) error {
	// always simulate first for humans
	res, _, err := applyFn(a, opts, true)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	if !res.Simulation.ChangesRequired {
		fmt.Printf("%s\n\n%s\n\n",
			display.Gold("No changes needed:"),
			display.Grey("The specified forma resources are up to date."))
		return nil
	}

	// don't show anything if --yes is specified
	if !opts.Yes {
		if err := maybePrintDescription(themeFor(a), res.Description, opts.Yes); err != nil {
			if errors.Is(err, errDescriptionAborted) {
				fmt.Print(display.Red("\nCommand aborted\n"))
				return nil
			}
			return err
		}

		th := themeFor(a)
		width := legacyWidth(os.Stdout)
		_, _ = fmt.Print(simview.RenderSimulationPlain(th, &res.Simulation, width))
	}

	if opts.Simulate {
		fmt.Print(display.Grey("Command will not continue - simulation only\n"))
		return nil
	}

	// confirm with the user before proceeding (unless --yes is specified)
	if !opts.Yes {
		if !isInteractive() {
			return fmt.Errorf("interactive input requires a TTY — pass --yes")
		}
		prompt := components.PromptForOperations(&res.Simulation.Command)
		ok, err := runConfirm(themeFor(a), prompt, "")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Print(display.Red("\nCommand aborted\n"))
			return nil
		}
	}

	var nags []string
	res, nags, err = applyFn(a, opts, false)
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
		return status.WatchCommandsStatus(a, query, 1, opts.StatusOutput)
	}

	fmt.Printf("\nRun the following command to check the status of this command:\n\n  %s%s%s\n",
		display.Grey("formae status command --query='id:"), display.LightBlue(res.CommandID), display.Grey("'"))

	nag.MaybePrintNags(themeFor(a), nags)

	return nil
}

func runApplyForMachines(app *app.App, opts *ApplyOptions) error {
	if opts.Simulate {
		res, _, err := applyFn(app, opts, true)
		if err != nil {
			return fmt.Errorf("error simlating apply command: %v", err)
		}
		printer := printer.NewMachineReadablePrinter[apimodel.Simulation](os.Stdout, opts.OutputSchema)

		return printer.Print(&res.Simulation)
	}
	res, _, err := applyFn(app, opts, false)
	if err != nil {
		return fmt.Errorf("error applying forma: %v", err)
	}
	printer := printer.NewMachineReadablePrinter[apimodel.CommandID](os.Stdout, opts.OutputSchema)

	return printer.Print(&apimodel.CommandID{CommandID: res.CommandID})
}

// maybePrintDescription prints description.Text (when non-empty) and, when
// description.Confirm is set, requires an explicit user acknowledgment before
// the operation confirm. This is the R3 safety gate: it must remain a DISTINCT
// step that runs BEFORE the operation confirm.
func maybePrintDescription(th *theme.Theme, description apimodel.Description, yes bool) error {
	if description.Text == "" {
		return nil
	}

	// Print the description text styled via the theme's body role.
	_, _ = fmt.Println(th.Styles.Body.Render(description.Text))

	if !description.Confirm {
		return nil
	}

	// Confirm==true: require an explicit acknowledgment.
	if yes {
		// --yes intentionally skips the ack (documented behaviour).
		return nil
	}
	if !isInteractive() {
		return fmt.Errorf("interactive input requires a TTY — pass --yes")
	}
	ok, err := runConfirm(th, "Acknowledge and continue?", description.Text)
	if err != nil {
		return err
	}
	if !ok {
		return errDescriptionAborted
	}
	return nil
}
