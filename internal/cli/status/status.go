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

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/nag"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/renderer"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/spf13/cobra"
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

			if opts.Query == "" || opts.OutputConsumer == printer.ConsumerMachine {
				opts.MaxResults = 1
			} else {
				opts.MaxResults = maxResults
			}

			opts.Watch, _ = command.Flags().GetBool("watch")
			outputLayout, _ := command.Flags().GetString("output-layout")
			opts.OutputLayout = StatusOutput(outputLayout)

			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			return runStatus(app, opts)
		},
		Annotations: map[string]string{
			"examples": "{{.Name}} status {{.Command}} --query 'state:inprogress' --max-results 10 |  {{.Name}} status {{.Command}} --watch",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().String("query", " ", "Query that allows to find past and current commands by their attributes")
	command.Flags().Bool("watch", false, "Continuously refresh and print the status until completion")
	command.Flags().String("output-layout", string(StatusOutputSummary), fmt.Sprintf("What to print as status output (%s | %s)", StatusOutputSummary, StatusOutputDetailed))
	command.Flags().Int("max-results", 10, "Maximum number of command results to return when using a query")

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
		return fmt.Errorf("output consumer must be either 'human' or 'machine'")
	}
	if options.OutputConsumer == printer.ConsumerMachine {
		if options.OutputSchema != "json" && options.OutputSchema != "yaml" {
			return fmt.Errorf("output schema must be either 'json' or 'yaml' for machine consumer")
		}
	}
	if options.OutputLayout != StatusOutputDetailed && options.OutputLayout != StatusOutputSummary {
		return fmt.Errorf("output layout must be either 'detailed' or 'summary'")
	}

	return nil
}

func runStatusForHumans(app *app.App, opts *StatusOptions) error {
	display.PrintBanner()

	status, nags, err := app.GetCommandsStatus(opts.Query, opts.MaxResults, false)
	if err != nil {
		msg, renderErr := renderer.RenderErrorMessage(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	// printer for summary or detailed
	p := printer.NewHumanReadablePrinter[apimodel.ListCommandStatusResponse](os.Stdout)
	if opts.OutputLayout == StatusOutputDetailed {
		err = p.Print(status, printer.PrintOptions{Summary: false})
	} else {
		err = p.Print(status, printer.PrintOptions{Summary: true})
	}
	if err != nil {
		return err
	}

	// print nags
	nag.MaybePrintNags(nags)

	if opts.Watch {
		return WatchCommandsStatus(app, opts.Query, opts.MaxResults, opts.OutputLayout)
	}

	return nil
}

func runStatusForMachines(app *app.App, opts *StatusOptions) error {
	status, _, err := app.GetCommandsStatus(opts.Query, opts.MaxResults, false)
	if err != nil {
		msg, renderErr := renderer.RenderErrorMessage(err)
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
			case printer.ConsumerHuman:
				display.PrintBanner()
			case printer.ConsumerMachine:
				if schema != "json" && schema != "yaml" {
					return fmt.Errorf("unsupported schema: %s", schema)
				}
			}

			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
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

			err = renderStats(stats)
			if err != nil {
				return err
			}

			if consumer != printer.ConsumerMachine && !watch {
				nag.MaybePrintNags(nags)
			}

			if watch && consumer == printer.ConsumerHuman { // machine consumer can't watch
				nag.MaybePrintNags(nags)
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

	return command
}

func renderCommandsStatus(status *apimodel.ListCommandStatusResponse, outputLayout StatusOutput) error {
	p := printer.NewHumanReadablePrinter[apimodel.ListCommandStatusResponse](os.Stdout)
	var err error
	if outputLayout == StatusOutputDetailed {
		err = p.Print(status, printer.PrintOptions{Summary: false})
	} else {
		err = p.Print(status, printer.PrintOptions{Summary: true})
	}
	if err != nil {
		return err
	}

	return nil
}

func renderStats(stats *apimodel.Stats) error {
	p := printer.NewHumanReadablePrinter[apimodel.Stats](os.Stdout)
	err := p.Print(stats, printer.PrintOptions{})
	if err != nil {
		return err
	}

	return nil
}

func prepareScreen(what string) {
	display.ClearScreen()
	display.PrintBanner()
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
		err = renderCommandsStatus(status, outputLayout)
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

	nag.MaybePrintNags(nags)

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

		err = renderStats(stats)
		if err != nil {
			return err
		}
	}
}

func StatusCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Various status retrieval commands",
		Annotations: map[string]string{
			"type":     "Information",
			"examples": "{{.Name}} {{.Command}} agent  |  {{.Name}} {{.Command}} command --query 'state:inprogress'",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.AddCommand(
		CommandCmd(),
		AgentCmd())

	return command
}
