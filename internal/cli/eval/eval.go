// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package eval

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/schema"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// isTerminal is a package-level seam so tests can stub it.
var isTerminal = tui.IsTerminal

// themeFor resolves the active theme from the app config.
// The name falls back to "formae" for nil configs (theme.New nil-guards internally).
func themeFor(a *app.App) *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

type EvalOptions struct {
	FormaFile      string
	Mode           pkgmodel.FormaApplyMode
	OutputConsumer printer.Consumer
	OutputSchema   string
	Properties     map[string]string
	SchemaLocation schema.SchemaLocation
}

func validateEvalOptions(opts *EvalOptions) error {
	if opts.FormaFile == "" {
		return cmd.FlagErrorf("forma file is required")
	}
	if opts.Mode != pkgmodel.FormaApplyModePatch && opts.Mode != pkgmodel.FormaApplyModeReconcile {
		return cmd.FlagErrorf("mode must be 'patch' or 'reconcile'")
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

func EvalCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "eval",
		Short: "Evaluate a forma",
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := &EvalOptions{}
			opts.FormaFile = command.Flags().Arg(0)
			mode, _ := command.Flags().GetString("mode")
			opts.Mode = pkgmodel.FormaApplyMode(mode)
			consumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")
			opts.Properties = cmd.PropertiesFromCmd(command)
			schemaLocation, _ := command.Flags().GetString("schema-location")
			loc, err := parseSchemaLocation(schemaLocation)
			if err != nil {
				return err
			}
			opts.SchemaLocation = loc

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runEval(app, opts)
		},
		Annotations: map[string]string{
			"type":     "Forma",
			"examples": "{{.Name}} {{.Command}} forma.pkl  |  {{.Name}} {{.Command}} --properties foo=bar,john=doe forma.pkl",
			"args":     "<forma file>",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("mode", string(pkgmodel.FormaApplyModeReconcile), "Apply mode (reconcile | patch)")
	command.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("schema-location", "remote", "How plugin PKL schemas are referenced when serializing the evaluated forma. 'remote' (default) emits package:// URIs that PKL fetches from the hub. 'local' emits local file imports against the agent's on-disk PklProject paths; requires CLI and agent to share a filesystem.")
	cmd.AddConfigFlags(command)

	return command
}

// parseSchemaLocation maps the --schema-location flag value to the
// internal SchemaLocation enum.
func parseSchemaLocation(s string) (schema.SchemaLocation, error) {
	switch s {
	case "", "remote":
		return schema.SchemaLocationRemote, nil
	case "local":
		return schema.SchemaLocationLocal, nil
	default:
		return "", cmd.FlagErrorf("invalid --schema-location %q; must be one of 'remote' or 'local'", s)
	}
}

func runEval(app *app.App, opts *EvalOptions) error {
	if err := validateEvalOptions(opts); err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerMachine {
		return runEvalForMachines(app, opts)
	}
	return runEvalForHumans(app, opts)
}

func runEvalForHumans(app *app.App, opts *EvalOptions) error {
	th := themeFor(app)
	app.PrintBanner()
	fmt.Printf("%s\n\n", renderEvalHeader(th, opts.FormaFile, string(opts.Mode)))

	result, err := app.Evaluate(opts.FormaFile, opts.Properties, opts.Mode)
	if err != nil {
		return fmt.Errorf("cannot evaluate forma: %v", err)
	}
	output, err := app.SerializeForma(result, &schema.SerializeOptions{
		Schema:         opts.OutputSchema,
		Beautify:       true,
		Colorize:       isTerminal(os.Stdout),
		SchemaLocation: opts.SchemaLocation,
	})
	if err != nil {
		return fmt.Errorf("cannot serialize eval result: %v", err)
	}

	fmt.Printf("%s\n\n%s\n", components.SectionHeader(th, fmt.Sprintf("Evaluated forma as '%s'", opts.OutputSchema)), output)

	return nil
}

func runEvalForMachines(app *app.App, opts *EvalOptions) error {
	result, err := app.Evaluate(opts.FormaFile, opts.Properties, opts.Mode)
	if err != nil {
		return fmt.Errorf("cannot evaluate forma: %v", err)
	}
	output, err := app.SerializeForma(result, &schema.SerializeOptions{
		Schema:   opts.OutputSchema,
		Beautify: false,
		Colorize: false,
	})
	if err != nil {
		return fmt.Errorf("cannot serialize eval result: %v", err)
	}
	fmt.Print(output + "\n")

	return nil
}
