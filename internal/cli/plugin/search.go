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

type SearchOptions struct {
	Query          string
	Category       string
	Type           string
	Channel        string
	OutputConsumer printer.Consumer
	OutputSchema   string
}

func PluginSearchCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "search",
		Short: "Search available plugins",
		Long: `Search the plugins available for installation from the
configured plugin repositories. With no arguments, every available
plugin is listed; with a query, only plugins whose name, summary, or
description matches are returned.

On a stock install the plugin store lives at a path that only root can
write to, so this command may prompt for sudo to refresh the
repository metadata.`,
		Annotations: map[string]string{
			"args": "[<query>]",
		},
		RunE: func(cc *cobra.Command, args []string) error {
			opts := &SearchOptions{}
			if len(args) == 1 {
				opts.Query = args[0]
			} else if len(args) > 1 {
				return cmd.FlagErrorf("at most one search query is supported")
			}
			opts.Category, _ = cc.Flags().GetString("category")
			opts.Type, _ = cc.Flags().GetString("type")
			opts.Channel, _ = cc.Flags().GetString("channel")
			consumer, _ := cc.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = cc.Flags().GetString("output-schema")

			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			return runSearch(app, opts)
		},
		SilenceErrors: true,
	}
	c.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)
	c.Flags().String("category", "", "Filter by category")
	c.Flags().String("type", "", "Filter by plugin type (resource|auth)")
	c.Flags().String("channel", "", "Search a different channel")
	c.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	c.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	return c
}

func runSearch(app *app.App, opts *SearchOptions) error {
	if err := validateSearchOptions(opts); err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runSearchForHumans(app, opts)
	}
	return runSearchForMachines(app, opts)
}

func validateSearchOptions(opts *SearchOptions) error {
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

func runSearchForHumans(app *app.App, opts *SearchOptions) error {
	app.PrintBanner()
	plugins, err := searchPlugins(app, opts)
	if err != nil {
		return err
	}
	fmt.Print(renderPluginSearch(themeFor(app), plugins, SearchRenderOpts{
		Query:    opts.Query,
		Category: opts.Category,
		Type:     opts.Type,
	}))
	return nil
}

func runSearchForMachines(app *app.App, opts *SearchOptions) error {
	plugins, err := searchPlugins(app, opts)
	if err != nil {
		return err
	}
	resp := &apimodel.ListPluginsResponse{Plugins: plugins}
	p := printer.NewMachineReadablePrinter[apimodel.ListPluginsResponse](os.Stdout, opts.OutputSchema)
	return p.Print(resp)
}

func searchPlugins(app *app.App, opts *SearchOptions) ([]apimodel.Plugin, error) {
	mgr, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, opts.Channel)
	if err != nil {
		return nil, err
	}
	if mgr == nil {
		return nil, fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
	}
	return mgr.LocalSearch(opts.Query, opts.Category, opts.Type)
}
