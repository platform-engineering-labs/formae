// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

type UninstallOptions struct {
	Packages       []string
	OutputConsumer printer.Consumer
	OutputSchema   string
}

func PluginUninstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "uninstall",
		Aliases: []string{"remove"},
		Short:   "Uninstall plugins from this host",
		Long: `Remove one or more plugins from this host. Each argument is a
plugin name.

If the formae agent runs on this host, restart it after uninstall so
the plugins are unloaded.`,
		Annotations: map[string]string{
			"args": "<name>...",
		},
		RunE: func(cc *cobra.Command, args []string) error {
			opts := &UninstallOptions{Packages: args}
			consumer, _ := cc.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = cc.Flags().GetString("output-schema")

			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			return runUninstall(app, opts)
		},
		SilenceErrors: true,
	}
	c.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)
	c.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	c.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	return c
}

func runUninstall(app *app.App, opts *UninstallOptions) error {
	if err := validateUninstallOptions(opts); err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runUninstallForHumans(app, opts)
	}
	return runUninstallForMachines(app, opts)
}

func validateUninstallOptions(opts *UninstallOptions) error {
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

func runUninstallForHumans(app *app.App, opts *UninstallOptions) error {
	app.PrintBanner()
	return runUninstallForHumansWithSeams(app, opts, os.Stdout, nil)
}

// runUninstallForHumansWithSeams is the testable inner implementation. mgr may
// be non-nil to inject a stub; when nil a real CLIPluginManager is created.
func runUninstallForHumansWithSeams(app *app.App, opts *UninstallOptions, w io.Writer, mgr localUninstaller) error {
	if mgr == nil {
		real, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, "")
		if err != nil {
			return err
		}
		if real == nil {
			return fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
		}
		mgr = real
	}

	th := themeFor(app)
	tty := pluginIsTerminal(w)
	n := len(opts.Packages)
	noun := "plugin"
	if n != 1 {
		noun = "plugins"
	}
	label := fmt.Sprintf("Removing %d %s…", n, noun)
	step := components.StartStep(w, th, label)

	if err := mgr.LocalUninstall(opts.Packages); err != nil {
		step.Fail(fmt.Sprintf("Failed to remove %s", strings.Join(opts.Packages, ", ")))
		return err
	}

	step.Done(fmt.Sprintf("Removed %d %s", n, noun))

	for _, name := range opts.Packages {
		ackLine(w, tty, th, components.AckDone, fmt.Sprintf("Removed %s", name))
	}
	ackLine(w, tty, th, components.AckWarn, "If this host runs the formae agent, restart it to unload the plugins: formae agent restart")
	return nil
}

func runUninstallForMachines(app *app.App, opts *UninstallOptions) error {
	return runUninstallForMachinesWithSeams(app, opts, os.Stdout, nil)
}

// runUninstallForMachinesWithSeams is the testable inner implementation for
// the machine path. mgr may be non-nil to inject a stub.
func runUninstallForMachinesWithSeams(app *app.App, opts *UninstallOptions, w io.Writer, mgr localUninstaller) error {
	if mgr == nil {
		real, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, "")
		if err != nil {
			return err
		}
		if real == nil {
			return fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
		}
		mgr = real
	}

	if err := mgr.LocalUninstall(opts.Packages); err != nil {
		return err
	}

	resp := &apimodel.UninstallPluginsResponse{
		Operations:      operationsFromPackages(opts.Packages, "remove"),
		RequiresRestart: true,
	}
	p := printer.NewMachineReadablePrinter[apimodel.UninstallPluginsResponse](w, opts.OutputSchema)
	return p.Print(resp)
}
