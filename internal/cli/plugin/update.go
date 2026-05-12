// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

type UpdateOptions struct {
	Packages       []string
	Channel        string
	OutputConsumer printer.Consumer
	OutputSchema   string
}

func PluginUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update",
		Short: "Update installed plugins on this host",
		Long: `Update installed plugins on this host to the latest available
version. With no arguments, every installed plugin is considered for
update; otherwise only the named plugins are updated.

If the formae agent runs on this host, restart it after update so the
updated plugins are loaded.`,
		Annotations: map[string]string{
			"args": "[<name>[@<version>]...]",
		},
		RunE: func(cc *cobra.Command, args []string) error {
			opts := &UpdateOptions{Packages: args}
			opts.Channel, _ = cc.Flags().GetString("channel")
			consumer, _ := cc.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = cc.Flags().GetString("output-schema")

			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			return runUpdate(app, opts)
		},
		SilenceErrors: true,
	}
	c.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)
	c.Flags().String("channel", "", "Update from a different channel")
	c.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	c.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	return c
}

func runUpdate(app *app.App, opts *UpdateOptions) error {
	if err := validateUpdateOptions(opts); err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runUpdateForHumans(app, opts)
	}
	return runUpdateForMachines(app, opts)
}

func validateUpdateOptions(opts *UpdateOptions) error {
	if opts.OutputConsumer != printer.ConsumerHuman && opts.OutputConsumer != printer.ConsumerMachine {
		return cmd.FlagErrorf("output-consumer must be 'human' or 'machine'")
	}
	if opts.OutputConsumer == printer.ConsumerMachine {
		if opts.OutputSchema != "json" && opts.OutputSchema != "yaml" {
			return cmd.FlagErrorf("output-schema must be either 'json' or 'yaml' for machine consumer")
		}
	}
	return nil
}

func runUpdateForHumans(app *app.App, opts *UpdateOptions) error {
	mgr, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, opts.Channel)
	if err != nil {
		return err
	}
	if mgr == nil {
		return fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
	}

	if err := mgr.LocalUpdate(opts.Packages); err != nil {
		return err
	}

	if len(opts.Packages) == 0 {
		fmt.Printf("  %s Updated all installed plugins\n", display.Green("✓"))
	} else {
		for _, name := range pluginNamesFromArgs(opts.Packages) {
			fmt.Printf("  %s Updated %s\n", display.Green("✓"), name)
		}
	}
	fmt.Printf("\n  %s If this host runs the formae agent, restart it to load the updated plugins: formae agent restart\n", display.Gold("!"))
	return nil
}

func runUpdateForMachines(app *app.App, opts *UpdateOptions) error {
	mgr, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, opts.Channel)
	if err != nil {
		return err
	}
	if mgr == nil {
		return fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
	}

	if err := mgr.LocalUpdate(opts.Packages); err != nil {
		return err
	}

	resp := &apimodel.UpdatePluginsResponse{
		Operations:      operationsFromPackages(opts.Packages, "update"),
		RequiresRestart: true,
	}
	p := printer.NewMachineReadablePrinter[apimodel.UpdatePluginsResponse](os.Stdout, opts.OutputSchema)
	return p.Print(resp)
}
