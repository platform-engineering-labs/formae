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

type InfoOptions struct {
	Name           string
	Channel        string
	OutputConsumer printer.Consumer
	OutputSchema   string
}

func PluginInfoCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "info",
		Short: "Show detailed plugin information",
		Long: `Show the description, version, and metadata for a single
plugin from the configured plugin repositories.

On a stock install the plugin store lives at a path that only root can
write to, so this command may prompt for sudo to refresh the
repository metadata.`,
		Annotations: map[string]string{
			"args": "<name>",
		},
		RunE: func(cc *cobra.Command, args []string) error {
			opts := &InfoOptions{}
			if len(args) == 1 {
				opts.Name = args[0]
			}
			opts.Channel, _ = cc.Flags().GetString("channel")
			consumer, _ := cc.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = cc.Flags().GetString("output-schema")

			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			return runInfo(app, opts)
		},
		SilenceErrors: true,
	}
	c.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)
	c.Flags().String("channel", "", "Query a different channel")
	c.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	c.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	return c
}

func runInfo(app *app.App, opts *InfoOptions) error {
	if err := validateInfoOptions(opts); err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runInfoForHumans(app, opts)
	}
	return runInfoForMachines(app, opts)
}

func validateInfoOptions(opts *InfoOptions) error {
	if opts.Name == "" {
		return cmd.FlagErrorf("exactly one plugin name is required")
	}
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

func runInfoForHumans(app *app.App, opts *InfoOptions) error {
	plugin, err := fetchPluginInfo(app, opts)
	if err != nil {
		return err
	}
	if plugin == nil {
		return fmt.Errorf("plugin '%s' not found", opts.Name)
	}
	fmt.Print(renderPluginInfo(plugin))
	return nil
}

func runInfoForMachines(app *app.App, opts *InfoOptions) error {
	plugin, err := fetchPluginInfo(app, opts)
	if err != nil {
		return err
	}
	if plugin == nil {
		return fmt.Errorf("plugin '%s' not found", opts.Name)
	}
	resp := &apimodel.GetPluginResponse{Plugin: *plugin}
	p := printer.NewMachineReadablePrinter[apimodel.GetPluginResponse](os.Stdout, opts.OutputSchema)
	return p.Print(resp)
}

func fetchPluginInfo(app *app.App, opts *InfoOptions) (*apimodel.Plugin, error) {
	mgr, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, opts.Channel, true, true)
	if err != nil {
		return nil, err
	}
	if mgr == nil {
		return nil, fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
	}
	return mgr.LocalInfo(opts.Name)
}
