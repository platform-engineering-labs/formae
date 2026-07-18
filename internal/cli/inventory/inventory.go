// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventory

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/inventoryview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/logging"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/spf13/cobra"
)

// isTerminal and launchInventoryTUI are package-level vars so tests can stub them.
var (
	isTerminal = tui.IsTerminal
	// launchInventoryTUI starts the interactive inventory TUI.
	// The theme name comes from the CLI profile configuration (Config.Cli.Theme);
	// unknown names fall back to "formae" inside theme.New.
	launchInventoryTUI = func(a *app.App, focus inventoryview.Tab, opts *InventoryOptions) error {
		themeName := ""
		if a != nil && a.Config != nil {
			themeName = a.Config.Cli.Theme
		}
		th := theme.New(themeName)
		maxRows := opts.MaxResults
		if !opts.MaxResultsSet {
			maxRows = 200
		}
		model := inventoryview.New(th, a, inventoryview.Options{
			FocusTab: focus,
			Query:    opts.Query,
			MaxRows:  maxRows,
			Now:      time.Now,
		})
		finalModel, err := tui.Run(model, tui.DefaultRunOptions())
		if err != nil {
			return err
		}
		// Print nags to stderr after exit (D9).
		if iv, ok := finalModel.(inventoryview.Model); ok {
			for _, nag := range iv.Nags() {
				fmt.Fprintln(os.Stderr, nag)
			}
		}
		return nil
	}
)

type InventoryOptions struct {
	Query          string
	OutputConsumer printer.Consumer
	OutputSchema   string
	MaxResults     int
	MaxResultsSet  bool // true when --max-results was explicitly set by the caller
}

func validateInventoryOptions(opts *InventoryOptions) error {
	if opts.MaxResults < 0 {
		return cmd.FlagErrorf("max-results must be 0 (unlimited) or a positive number")
	}
	if opts.OutputConsumer != printer.ConsumerHuman && opts.OutputConsumer != printer.ConsumerMachine {
		return cmd.FlagErrorf("output-consumer must be 'human' or 'machine'")
	}
	if opts.OutputConsumer == printer.ConsumerMachine {
		if opts.OutputSchema != "json" && opts.OutputSchema != "yaml" {
			return cmd.FlagErrorf("output-schema must be 'json' or 'yaml' for machine consumer")
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
			opts.MaxResultsSet = command.Flags().Changed("max-results")
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runResources(app, opts)
		},
		Annotations: map[string]string{
			"examples": "formae inventory resources --query 'type:AWS::S3::Bucket'" +
				" | formae inventory resources --query 'type:GCP::Compute::* stack:prod'" +
				" | formae inventory resources --query 'target:eu target:us managed:false'" +
				" | formae inventory resources --max-results 50",
		},
		SilenceErrors: true,
	}

	command.Flags().String("query", "", "Query that allows to find resources by their attributes. Use * as a wildcard anywhere (e.g. foo*, *foo, *foo*, foo*bar). ? and regex are not yet supported.")
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command output (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().Int("max-results", 10, "Maximum number of resources to display in the table (0 = unlimited)")
	cmd.AddConfigFlags(command)

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
	forma, _, err := app.ExtractResources(opts.Query, false)
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
	// Human + TTY → interactive TUI (owns the whole screen; banner suppressed).
	if isTerminal(os.Stdout) {
		return launchInventoryTUI(app, inventoryview.TabResources, opts)
	}

	// Human + non-TTY → lipgloss print-and-exit path.
	app.PrintBanner()

	forma, _, err := app.ExtractResources(opts.Query, false)
	if err != nil {
		return err
	}

	maxResults := opts.MaxResults
	if !opts.MaxResultsSet {
		maxResults = 10
	}
	th := themeForInventory(app)
	_, _ = fmt.Println(renderInventoryResources(th, forma, maxResults, inventoryTermWidth(os.Stdout)))
	return nil
}

func InventoryCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "inventory",
		Short: "Inventory management",
		Annotations: map[string]string{
			"type": "Inventory",
		},
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
			opts.MaxResultsSet = command.Flags().Changed("max-results")
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runResources(app, opts)
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	// Add the same flag set as the resources subcommand (bare inventory behaves
	// like resources with focus=TabResources).
	command.Flags().String("query", "", "Query that allows to find resources by their attributes. Use * as a wildcard anywhere (e.g. foo*, *foo, *foo*, foo*bar). ? and regex are not yet supported.")
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command output (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().Int("max-results", 10, "Maximum number of resources to display in the table (0 = unlimited)")
	cmd.AddConfigFlags(command)

	resources := resourcesCmd()
	targets := targetsCmd()
	stacks := stacksCmd()
	policies := policiesCmd()
	command.AddCommand(resources)
	command.AddCommand(targets)
	command.AddCommand(stacks)
	command.AddCommand(policies)

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
			opts.MaxResultsSet = command.Flags().Changed("max-results")
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runTargets(app, opts)
		},
		Annotations: map[string]string{
			"examples": "formae inventory targets --query 'discoverable:true'" +
				" | formae inventory targets --query 'namespace:AWS label:prod-*'" +
				" | formae inventory targets --max-results 50",
		},
		SilenceErrors: true,
	}

	command.Flags().String("query", "", "Query that allows to find targets by their attributes (e.g., 'namespace:AWS', 'discoverable:true', 'label:prod-us-east-1'). Use * as a wildcard anywhere (e.g. foo*, *foo, *foo*, foo*bar). ? and regex are not yet supported.")
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command output (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().Int("max-results", 10, "Maximum number of targets to display in the table (0 = unlimited)")
	cmd.AddConfigFlags(command)

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
	targets, _, err := app.ExtractTargets(opts.Query, false)
	if err != nil {
		return err
	}

	p := printer.NewMachineReadablePrinter[[]*pkgmodel.Target](os.Stdout, opts.OutputSchema)
	return p.Print(&targets)
}

func runTargetsForHumans(app *app.App, opts *InventoryOptions) error {
	// Human + TTY → interactive TUI (owns the whole screen; banner suppressed).
	if isTerminal(os.Stdout) {
		return launchInventoryTUI(app, inventoryview.TabTargets, opts)
	}

	// Human + non-TTY → lipgloss print-and-exit path.
	app.PrintBanner()

	targets, _, err := app.ExtractTargets(opts.Query, false)
	if err != nil {
		return err
	}

	maxResults := opts.MaxResults
	if !opts.MaxResultsSet {
		maxResults = 10
	}
	th := themeForInventory(app)
	_, _ = fmt.Println(renderInventoryTargets(th, targets, maxResults, inventoryTermWidth(os.Stdout)))
	return nil
}

func stacksCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "stacks",
		Short: "Query inventory of stacks",
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := &InventoryOptions{}
			consumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.MaxResults, _ = command.Flags().GetInt("max-results")
			opts.MaxResultsSet = command.Flags().Changed("max-results")
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runStacks(app, opts)
		},
		Annotations: map[string]string{
			"examples": "formae inventory stacks" +
				" | formae inventory stacks --max-results 50",
		},
		SilenceErrors: true,
	}

	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command output (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().Int("max-results", 10, "Maximum number of stacks to display in the table (0 = unlimited)")
	cmd.AddConfigFlags(command)

	return command
}

func runStacks(app *app.App, opts *InventoryOptions) error {
	if err := validateInventoryOptions(opts); err != nil {
		return err
	}

	if opts.OutputConsumer == printer.ConsumerMachine {
		return runStacksForMachines(app, opts)
	}
	return runStacksForHumans(app, opts)
}

func policiesCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "policies",
		Short: "Query inventory of standalone policies",
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := &InventoryOptions{}
			consumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.MaxResults, _ = command.Flags().GetInt("max-results")
			opts.MaxResultsSet = command.Flags().Changed("max-results")
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runPolicies(app, opts)
		},
		Annotations: map[string]string{
			"examples": "formae inventory policies" +
				" | formae inventory policies --max-results 50",
		},
		SilenceErrors: true,
	}

	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command output (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().Int("max-results", 10, "Maximum number of policies to display in the table (0 = unlimited)")
	cmd.AddConfigFlags(command)

	return command
}

func runPolicies(app *app.App, opts *InventoryOptions) error {
	if err := validateInventoryOptions(opts); err != nil {
		return err
	}

	if opts.OutputConsumer == printer.ConsumerMachine {
		return runPoliciesForMachines(app, opts)
	}
	return runPoliciesForHumans(app, opts)
}

func runPoliciesForMachines(app *app.App, opts *InventoryOptions) error {
	policies, _, err := app.ExtractPolicies(false)
	if err != nil {
		return err
	}

	p := printer.NewMachineReadablePrinter[[]apimodel.PolicyInventoryItem](os.Stdout, opts.OutputSchema)
	return p.Print(&policies)
}

func runPoliciesForHumans(app *app.App, opts *InventoryOptions) error {
	// Human + TTY → interactive TUI (owns the whole screen; banner suppressed).
	if isTerminal(os.Stdout) {
		return launchInventoryTUI(app, inventoryview.TabPolicies, opts)
	}

	// Human + non-TTY → lipgloss print-and-exit path.
	app.PrintBanner()

	policies, _, err := app.ExtractPolicies(false)
	if err != nil {
		return err
	}

	maxResults := opts.MaxResults
	if !opts.MaxResultsSet {
		maxResults = 10
	}
	th := themeForInventory(app)
	_, _ = fmt.Println(renderInventoryPolicies(th, policies, maxResults, inventoryTermWidth(os.Stdout)))
	return nil
}

func runStacksForMachines(app *app.App, opts *InventoryOptions) error {
	stacks, _, err := app.ExtractStacks(false)
	if err != nil {
		return err
	}

	p := printer.NewMachineReadablePrinter[[]*pkgmodel.Stack](os.Stdout, opts.OutputSchema)
	return p.Print(&stacks)
}

func runStacksForHumans(app *app.App, opts *InventoryOptions) error {
	// Human + TTY → interactive TUI (owns the whole screen; banner suppressed).
	if isTerminal(os.Stdout) {
		return launchInventoryTUI(app, inventoryview.TabStacks, opts)
	}

	// Human + non-TTY → lipgloss print-and-exit path.
	app.PrintBanner()

	stacks, _, err := app.ExtractStacks(false)
	if err != nil {
		return err
	}

	maxResults := opts.MaxResults
	if !opts.MaxResultsSet {
		maxResults = 10
	}
	th := themeForInventory(app)
	_, _ = fmt.Println(renderInventoryStacks(th, stacks, time.Now(), maxResults, inventoryTermWidth(os.Stdout)))
	return nil
}
