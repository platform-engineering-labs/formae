// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package apply

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/nag"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/prompter"
	"github.com/platform-engineering-labs/formae/internal/cli/renderer"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

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
	command.Flags().String("config", "", "Path to config file")

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
		return fmt.Errorf("forma file is required")
	}
	if opts.Mode == "" {
		return fmt.Errorf("the --mode flag is required")
	}
	if opts.Mode != pkgmodel.FormaApplyModeReconcile && opts.Mode != pkgmodel.FormaApplyModePatch {
		return fmt.Errorf("invalid mode: %s. Should be either reconcile or patch", opts.Mode)
	}
	if opts.OutputConsumer != printer.ConsumerHuman && opts.OutputConsumer != printer.ConsumerMachine {
		return fmt.Errorf("output consumer must be either 'human' or 'machine'")
	}
	if opts.OutputConsumer == printer.ConsumerMachine {
		if opts.OutputSchema != "json" && opts.OutputSchema != "yaml" {
			return fmt.Errorf("output schema must be either 'json' or 'yaml' for machine consumer")
		}
	}

	return nil
}

func runApplyForHumans(app *app.App, opts *ApplyOptions) error {
	display.PrintBanner()

	// always simulate first for humans
	res, _, err := app.Apply(opts.FormaFile, opts.Properties, opts.Mode, true, opts.Force)
	if err != nil {
		msg, renderErr := renderer.RenderErrorMessage(err)
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
		_ = maybePrintDescription(res.Description)

		p := printer.NewHumanReadablePrinter[apimodel.Simulation](os.Stdout)
		err = p.Print(&res.Simulation, printer.PrintOptions{})
		if err != nil {
			return fmt.Errorf("error printing simulation: %v", err)
		}
	}

	if opts.Simulate {
		fmt.Print(display.Grey("Command will not continue - simulation only\n"))
		return nil
	}

	// confirm with the user before proceeding (unless --yes is specified)
	prompter := prompter.NewBasicPrompter()
	prompt := renderer.PromptForOperations(&res.Simulation.Command)
	if !opts.Yes && !prompter.Confirm(prompt, false) {
		fmt.Print(display.Red("\nCommand aborted\n"))
		return nil
	}

	var nags []string
	res, nags, err = app.Apply(opts.FormaFile, opts.Properties, opts.Mode, false, opts.Force)
	if err != nil {
		msg, renderErr := renderer.RenderErrorMessage(err)
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

	nag.MaybePrintNags(nags)

	return nil
}

func runApplyForMachines(app *app.App, opts *ApplyOptions) error {
	if opts.Simulate {
		res, _, err := app.Apply(opts.FormaFile, opts.Properties, opts.Mode, true, opts.Force)
		if err != nil {
			return fmt.Errorf("error simlating apply command: %v", err)
		}
		printer := printer.NewMachineReadablePrinter[apimodel.Simulation](os.Stdout, opts.OutputSchema)

		return printer.Print(&res.Simulation)
	}
	res, _, err := app.Apply(opts.FormaFile, opts.Properties, opts.Mode, false, opts.Force)
	if err != nil {
		return fmt.Errorf("error applying forma: %v", err)
	}
	printer := printer.NewMachineReadablePrinter[apimodel.CommandID](os.Stdout, opts.OutputSchema)

	return printer.Print(&apimodel.CommandID{CommandID: res.CommandID})
}

func maybePrintDescription(description apimodel.Description) error {
	if description.Confirm && description.Text != "" {
		prompter := prompter.NewBasicPrompter()
		err := prompter.PressEnterToContinue(description.Text)
		if err != nil {
			return fmt.Errorf("error prompting the user to continue: %v", err)
		}
	}
	return nil
}
