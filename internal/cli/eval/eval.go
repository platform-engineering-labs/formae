// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package eval

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/logging"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

type EvalOptions struct {
	FormaFile      string
	Mode           pkgmodel.FormaApplyMode
	OutputConsumer printer.Consumer
	OutputSchema   string
	Beautify       bool
	Colorize       bool
	Properties     map[string]string
}

func validateEvalOptions(opts *EvalOptions) error {
	if opts.FormaFile == "" {
		return fmt.Errorf("forma file is required")
	}
	if opts.Mode != pkgmodel.FormaApplyModePatch && opts.Mode != pkgmodel.FormaApplyModeReconcile {
		return fmt.Errorf("mode must be 'patch' or 'reconcile'")
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
			opts.Beautify, _ = command.Flags().GetBool("beautify")
			opts.Colorize, _ = command.Flags().GetBool("colorize")
			opts.Properties = cmd.PropertiesFromCmd(command)

			app, err := cmd.AppFromContext(command.Context(), "", "", command)
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
	command.Flags().Bool("beautify", true, "beautify output (human consumer only)")
	command.Flags().Bool("colorize", true, "colorize output (human consumer only)")
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")

	return command
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
	display.PrintBanner()
	fmt.Print(display.Gold("Evaluating forma:") + "\n  " + display.Green("File: ") + fmt.Sprintf("%s\n  ", opts.FormaFile) + display.Green("Mode:") + fmt.Sprintf(" %s\n\n", opts.Mode))

	result, err := app.Evaluate(opts.FormaFile, opts.Properties, opts.Mode)
	if err != nil {
		return fmt.Errorf("cannot evaluate forma: %v", err)
	}
	output, err := app.SerializeForma(result, &plugin.SerializeOptions{
		Schema:   opts.OutputSchema,
		Beautify: opts.Beautify,
		Colorize: opts.Colorize,
	})
	if err != nil {
		return fmt.Errorf("cannot serialize eval result: %v", err)
	}

	fmt.Printf("%s\n\n%s\n", display.Goldf("Evaluated forma as '%s':", opts.OutputSchema), output)

	return nil
}

func runEvalForMachines(app *app.App, opts *EvalOptions) error {
	result, err := app.Evaluate(opts.FormaFile, opts.Properties, opts.Mode)
	if err != nil {
		return fmt.Errorf("cannot evaluate forma: %v", err)
	}
	output, err := app.SerializeForma(result, &plugin.SerializeOptions{
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
