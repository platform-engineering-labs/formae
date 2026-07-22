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
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

type ListOptions struct {
	OutputConsumer printer.Consumer
	OutputSchema   string
}

func PluginListCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List installed plugins on this host",
		Long: `List the plugins installed on this host with their version.

This reads the local package store without sudo.`,
		RunE: func(cc *cobra.Command, args []string) error {
			opts := &ListOptions{}
			consumer, _ := cc.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = cc.Flags().GetString("output-schema")

			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			return runList(app, opts)
		},
		SilenceErrors: true,
	}
	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	return command
}

func runList(app *app.App, opts *ListOptions) error {
	if err := validateListOptions(opts); err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runListForHumans(app, opts)
	}
	return runListForMachines(app, opts)
}

func validateListOptions(opts *ListOptions) error {
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

func runListForHumans(app *app.App, opts *ListOptions) error {
	app.PrintBanner()
	plugins, err := installedPlugins(app)
	if err != nil {
		return err
	}
	fmt.Print(renderPluginList(themeFor(app), plugins))
	return nil
}

func runListForMachines(app *app.App, opts *ListOptions) error {
	plugins, err := installedPlugins(app)
	if err != nil {
		return err
	}
	resp := &apimodel.ListPluginsResponse{Plugins: plugins}
	p := printer.NewMachineReadablePrinter[apimodel.ListPluginsResponse](os.Stdout, opts.OutputSchema)
	return p.Print(resp)
}

// installedPlugins reads the local orbital tree directly. The CLI runs
// no agent / actor system locally, so this is the only source of truth
// for what is installed on this host. The tree is opened read-only
// (sudo=false, writable=false), so this never elevates.
func installedPlugins(app *app.App) ([]apimodel.Plugin, error) {
	mgr, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, "", false, false)
	if err != nil {
		return nil, err
	}
	if mgr == nil {
		return nil, fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
	}
	return mgr.ListInstalled()
}
