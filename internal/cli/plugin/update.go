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
	app.PrintBanner()
	return runUpdateForHumansWithSeams(app, opts, os.Stdout, nil)
}

// runUpdateForHumansWithSeams is the testable inner implementation. mgr may
// be non-nil to inject a stub; when nil a real CLIPluginManager is created.
func runUpdateForHumansWithSeams(app *app.App, opts *UpdateOptions, w io.Writer, mgr localUpdater) error {
	if mgr == nil {
		real, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, opts.Channel, true, true)
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

	var label string
	if len(opts.Packages) == 0 {
		label = "Updating all installed plugins…"
	} else {
		n := len(opts.Packages)
		noun := "plugin"
		if n != 1 {
			noun = "plugins"
		}
		label = fmt.Sprintf("Updating %d %s…", n, noun)
	}
	step := components.StartStep(w, th, label)

	if err := mgr.LocalUpdate(opts.Packages); err != nil {
		names := pluginNamesFromArgs(opts.Packages)
		if len(names) == 0 {
			step.Fail("Failed to update plugins")
		} else {
			step.Fail(fmt.Sprintf("Failed to update %s", strings.Join(names, ", ")))
		}
		return err
	}

	if len(opts.Packages) == 0 {
		step.Done("Updated all installed plugins")
	} else {
		names := pluginNamesFromArgs(opts.Packages)
		noun := "plugin"
		if len(names) != 1 {
			noun = "plugins"
		}
		step.Done(fmt.Sprintf("Updated %d %s", len(names), noun))
		for _, name := range names {
			ackLine(w, tty, th, components.AckDone, fmt.Sprintf("Updated %s", name))
		}
	}
	ackLine(w, tty, th, components.AckWarn, "If this host runs the formae agent, restart it to load the updated plugins: formae agent restart")
	return nil
}

func runUpdateForMachines(app *app.App, opts *UpdateOptions) error {
	return runUpdateForMachinesWithSeams(app, opts, os.Stdout, nil)
}

// runUpdateForMachinesWithSeams is the testable inner implementation for the
// machine path. mgr may be non-nil to inject a stub.
func runUpdateForMachinesWithSeams(app *app.App, opts *UpdateOptions, w io.Writer, mgr localUpdater) error {
	if mgr == nil {
		real, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, opts.Channel, true, true)
		if err != nil {
			return err
		}
		if real == nil {
			return fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
		}
		mgr = real
	}

	if err := mgr.LocalUpdate(opts.Packages); err != nil {
		return err
	}

	resp := &apimodel.UpdatePluginsResponse{
		Operations:      operationsFromPackages(opts.Packages, "update"),
		RequiresRestart: true,
	}
	p := printer.NewMachineReadablePrinter[apimodel.UpdatePluginsResponse](w, opts.OutputSchema)
	return p.Print(resp)
}
