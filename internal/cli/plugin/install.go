// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

type InstallOptions struct {
	Packages       []string
	Channel        string
	OutputConsumer printer.Consumer
	OutputSchema   string
}

// pluginNamesFromArgs returns the bare plugin names from "name@version"
// args. Mainly for echoing the requested set back to the user when the
// install/update path accepted versioned specs.
func pluginNamesFromArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		name, _, _ := strings.Cut(a, "@")
		out = append(out, name)
	}
	return out
}

func PluginInstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "install",
		Short: "Install plugins on this host",
		Long: `Install one or more plugins on this host. Each argument is a
plugin name, optionally with a version (e.g. aws or aws@1.2.3).

If the formae agent runs on this host, restart it after install so the
new plugins are loaded.`,
		Annotations: map[string]string{
			"args": "<name>[@<version>]...",
		},
		RunE: func(cc *cobra.Command, args []string) error {
			opts := &InstallOptions{Packages: args}
			opts.Channel, _ = cc.Flags().GetString("channel")
			consumer, _ := cc.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = cc.Flags().GetString("output-schema")

			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			return runInstall(app, opts)
		},
		SilenceErrors: true,
	}
	c.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)
	c.Flags().String("channel", "", "Install from a different channel")
	c.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	c.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	return c
}

func runInstall(app *app.App, opts *InstallOptions) error {
	if err := validateInstallOptions(opts); err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runInstallForHumans(app, opts)
	}
	return runInstallForMachines(app, opts)
}

func validateInstallOptions(opts *InstallOptions) error {
	if len(opts.Packages) == 0 {
		return cmd.FlagErrorf("at least one plugin name is required")
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

func runInstallForHumans(app *app.App, opts *InstallOptions) error {
	mgr, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, opts.Channel)
	if err != nil {
		return err
	}
	if mgr == nil {
		return fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
	}

	if err := mgr.LocalInstall(opts.Packages); err != nil {
		return err
	}

	for _, name := range pluginNamesFromArgs(opts.Packages) {
		fmt.Printf("  %s Installed %s\n", display.Green("✓"), name)
	}
	fmt.Printf("\n  %s If this host runs the formae agent, restart it to load the new plugins: formae agent restart\n", display.Gold("!"))
	return nil
}

func runInstallForMachines(app *app.App, opts *InstallOptions) error {
	mgr, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, opts.Channel)
	if err != nil {
		return err
	}
	if mgr == nil {
		return fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
	}

	if err := mgr.LocalInstall(opts.Packages); err != nil {
		return err
	}

	resp := &apimodel.InstallPluginsResponse{
		Operations:      operationsFromPackages(opts.Packages, "install"),
		RequiresRestart: true,
	}
	p := printer.NewMachineReadablePrinter[apimodel.InstallPluginsResponse](os.Stdout, opts.OutputSchema)
	return p.Print(resp)
}

// operationsFromPackages synthesizes PluginOperation entries from the
// user-supplied package specs. Type is left empty because the local
// install path does not surface plugin metadata; the action and the
// requested version (if any) are reflected as-is.
func operationsFromPackages(packages []string, action string) []apimodel.PluginOperation {
	ops := make([]apimodel.PluginOperation, 0, len(packages))
	for _, pkg := range packages {
		name, version, _ := strings.Cut(pkg, "@")
		ops = append(ops, apimodel.PluginOperation{
			Name:    name,
			Version: version,
			Action:  action,
		})
	}
	return ops
}
