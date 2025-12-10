// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventory

import (
	"fmt"
	"os"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/logging"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/spf13/cobra"
)

type InventoryOptions struct {
	Query          string
	OutputConsumer printer.Consumer
	OutputSchema   string
	MaxResults     int
}

func validateInventoryOptions(opts *InventoryOptions) error {
	if opts.MaxResults < 0 {
		return fmt.Errorf("max-results must be 0 (unlimited) or a positive number")
	}
	if opts.OutputConsumer != printer.ConsumerHuman && opts.OutputConsumer != printer.ConsumerMachine {
		return fmt.Errorf("output-consumer must be 'human' or 'machine'")
	}
	if opts.OutputConsumer == printer.ConsumerMachine {
		if opts.OutputSchema != "json" && opts.OutputSchema != "yaml" {
			return fmt.Errorf("output-schema must be 'json' or 'yaml' for machine consumer")
		}
	}

	return nil
}

func resourcesCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "resources",
		Short: "Query inventory of resources",
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := &InventoryOptions{}
			consumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			query, _ := command.Flags().GetString("query")
			opts.Query = strings.TrimSpace(query)
			opts.MaxResults, _ = command.Flags().GetInt("max-results")
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runResources(app, opts)
		},
		Annotations: map[string]string{
			"examples": "{{.Name}} {{.Command}} inventory resources --query 'type:AWS::S3::Bucket' --max-results 50",
		},
		SilenceErrors: true,
	}

	command.Flags().String("query", "", "Query that allows to find resources by their attributes")
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command output (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().Int("max-results", 10, "Maximum number of resources to display in the table (0 = unlimited)")
	command.Flags().String("config", "", "Path to config file")

	return command
}

func runResources(app *app.App, opts *InventoryOptions) error {
	if err := validateInventoryOptions(opts); err != nil {
		return err
	}

	if opts.OutputConsumer == printer.ConsumerMachine {
		return runResourcesForMachines(app, opts)
	}
	return runResourcesForHumans(app, opts)
}

type inventory struct {
	Targets   []pkgmodel.Target   `json:"Targets,omitempty"`
	Resources []pkgmodel.Resource `json:"Resources,omitempty"`
}

func runResourcesForMachines(app *app.App, opts *InventoryOptions) error {
	forma, _, err := app.ExtractResources(opts.Query)
	if err != nil {
		return err
	}

	resources := &inventory{
		Targets:   forma.Targets,
		Resources: forma.Resources,
	}
	p := printer.NewMachineReadablePrinter[inventory](os.Stdout, opts.OutputSchema)
	return p.Print(resources)
}

func runResourcesForHumans(app *app.App, opts *InventoryOptions) error {
	display.PrintBanner()

	forma, _, err := app.ExtractResources(opts.Query)
	if err != nil {
		return err
	}

	p := printer.NewHumanReadablePrinter[pkgmodel.Forma](os.Stdout)
	return p.Print(forma, printer.PrintOptions{MaxResults: opts.MaxResults})
}

func InventoryCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "inventory",
		Short: "Inventory management",
		Annotations: map[string]string{
			"type": "Inventory",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	resources := resourcesCmd()
	targets := targetsCmd()
	command.AddCommand(resources)
	command.AddCommand(targets)

	return command
}

func targetsCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "targets",
		Short: "Query inventory of targets",
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := &InventoryOptions{}
			consumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			query, _ := command.Flags().GetString("query")
			opts.Query = strings.TrimSpace(query)
			opts.MaxResults, _ = command.Flags().GetInt("max-results")
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runTargets(app, opts)
		},
		Annotations: map[string]string{
			"examples": "{{.Name}} {{.Command}} inventory targets --query 'discoverable:true' --max-results 50",
		},
		SilenceErrors: true,
	}

	command.Flags().String("query", "", "Query that allows to find targets by their attributes (e.g., 'namespace:AWS', 'discoverable:true', 'label:prod-us-east-1')")
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command output (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().Int("max-results", 10, "Maximum number of targets to display in the table (0 = unlimited)")
	command.Flags().String("config", "", "Path to config file")

	return command
}

func runTargets(app *app.App, opts *InventoryOptions) error {
	if err := validateInventoryOptions(opts); err != nil {
		return err
	}

	if opts.OutputConsumer == printer.ConsumerMachine {
		return runTargetsForMachines(app, opts)
	}
	return runTargetsForHumans(app, opts)
}

func runTargetsForMachines(app *app.App, opts *InventoryOptions) error {
	targets, _, err := app.ExtractTargets(opts.Query)
	if err != nil {
		return err
	}

	p := printer.NewMachineReadablePrinter[[]*pkgmodel.Target](os.Stdout, opts.OutputSchema)
	return p.Print(&targets)
}

func runTargetsForHumans(app *app.App, opts *InventoryOptions) error {
	display.PrintBanner()

	targets, _, err := app.ExtractTargets(opts.Query)
	if err != nil {
		return err
	}

	p := printer.NewHumanReadablePrinter[[]*pkgmodel.Target](os.Stdout)
	return p.Print(&targets, printer.PrintOptions{MaxResults: opts.MaxResults})
}
