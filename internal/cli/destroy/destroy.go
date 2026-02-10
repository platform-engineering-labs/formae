// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package destroy

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/nag"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/prompter"
	"github.com/platform-engineering-labs/formae/internal/cli/renderer"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	"github.com/platform-engineering-labs/formae/internal/logging"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

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
			"type":     "Forma",
			"examples": "{{.Name}} {{.Command}} forma.pkl",
			"args":     "<forma file>",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("query", " ", "Query that allows to find resources by their attributes. Only used when no forma file is provided.")
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	command.Flags().Bool("simulate", false, "Simulate the command rather than make actual changes")
	command.Flags().Bool("watch", false, "Continuously refresh and print the status until completion")
	command.Flags().String("status-output-layout", string(status.StatusOutputSummary), fmt.Sprintf("What to print as status output (%s | %s)", status.StatusOutputSummary, status.StatusOutputDetailed))
	command.Flags().Bool("yes", false, "Allow the command to run without any confirmations")
	command.Flags().String("on-dependents", "abort", "Behavior when resources depend on those being deleted (abort | cascade)")
	command.Flags().String("config", "", "Path to config file")

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
	display.PrintBanner()

	if opts.FormaFile != "" {
		fmt.Print(display.Gold("Destroying resources defined by forma:\n ") + display.Green("File: ") + fmt.Sprintf("%s\n\n", opts.FormaFile))
	} else {
		fmt.Print(display.Gold("Destroying resources defined by query:\n ") + display.Green("Query: ") + fmt.Sprintf("%s\n\n", opts.Query))
	}

	res, _, err := app.Destroy(opts.FormaFile, opts.Query, opts.Properties, true)
	if err != nil {
		msg, renderErr := renderer.RenderErrorMessage(err)
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

	// If --yes is specified with --on-dependents=abort and there are cascades, abort
	if opts.Yes && hasCascades && opts.OnDependents == OnDependentsAbort {
		fmt.Printf("\n%s\n\n", display.Red("Error: This operation would cascade delete additional resources."))
		fmt.Printf("%s\n\n", display.Grey("The following resources depend on resources being deleted and would also be deleted:"))

		for _, ru := range res.Simulation.Command.ResourceUpdates {
			if ru.IsCascade {
				fmt.Printf("  %s %s (depends on %s)\n",
					display.Red("•"),
					display.LightBlue(ru.ResourceLabel),
					display.Grey(ru.CascadeSource))
			}
		}

		fmt.Printf("\n%s\n", display.Grey("To proceed with cascade deletes, use --on-dependents=cascade"))
		return fmt.Errorf("cascade deletes detected, aborting (use --on-dependents=cascade to proceed)")
	}

	// don't show anything if --yes is specified
	if !opts.Yes {
		// Show warning about cascades before simulation output
		if hasCascades {
			fmt.Printf("%s\n\n", display.Gold("Warning: This operation will cascade delete additional resources."))
		}

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
	res, nags, err = app.Destroy(opts.FormaFile, opts.Query, opts.Properties, false)
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

func runDestroyForMachines(app *app.App, opts *DestroyOptions) error {
	if opts.Simulate {
		res, _, err := app.Destroy(opts.FormaFile, opts.Query, opts.Properties, true)
		if err != nil {
			return fmt.Errorf("error simlating destroy command: %v", err)
		}
		printer := printer.NewMachineReadablePrinter[apimodel.Simulation](os.Stdout, opts.OutputSchema)

		return printer.Print(&res.Simulation)
	}
	res, _, err := app.Destroy(opts.FormaFile, opts.Query, opts.Properties, false)
	if err != nil {
		return fmt.Errorf("error destroying forma: %v", err)
	}
	printer := printer.NewMachineReadablePrinter[apimodel.CommandID](os.Stdout, opts.OutputSchema)

	return printer.Print(&apimodel.CommandID{CommandID: res.CommandID})
}

// hasCascadeDeletes checks if any resource updates in the command are cascade deletes
func hasCascadeDeletes(cmd *apimodel.Command) bool {
	for _, ru := range cmd.ResourceUpdates {
		if ru.IsCascade {
			return true
		}
	}
	return false
}
