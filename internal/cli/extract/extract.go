// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package extract

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/nag"
	"github.com/platform-engineering-labs/formae/internal/cli/prompter"
	"github.com/platform-engineering-labs/formae/internal/cli/renderer"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/spf13/cobra"
)

type ExtractOptions struct {
	TargetPath     string
	Query          string
	Yes            bool
	OutputSchema   string
	SchemaLocation string
}

func ExtractCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "extract",
		Short: "Extract resources to a file",
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := &ExtractOptions{}
			opts.TargetPath = command.Flags().Arg(0)
			opts.Query, _ = command.Flags().GetString("query")
			opts.Yes, _ = command.Flags().GetBool("yes")
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")
			opts.SchemaLocation, _ = command.Flags().GetString("schema-location")

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runExtract(app, opts)
		},
		Annotations: map[string]string{
			"type":     "Forma",
			"examples": "{{.Name}} {{.Command}} ./code.pkl  |  {{.Name}} {{.Command}} --query 'type:AWS::S3::Bucket' ./buckets.pkl",
			"args":     "<target file>",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("query", " ", "Query that allows to find resources by their attributes")
	command.Flags().Bool("yes", false, "Overwrite existing files without prompting")
	command.Flags().String("output-schema", "pkl", "Output schema (only 'pkl' is currently supported)")
	command.Flags().String("schema-location", "local", "Schema location: 'remote' (PKL registry) or 'local' (installed plugins)")
	command.Flags().String("config", "", "Path to config file")

	return command
}

func runExtract(app *app.App, opts *ExtractOptions) error {
	err := validateExtractOptions(opts)
	if err != nil {
		return err
	}

	display.PrintBanner()

	if !app.IsSupportedOutputSchema(opts.OutputSchema) {
		return fmt.Errorf("unsupported output schema '%s', supported schemas are: %v", opts.OutputSchema, app.SupportedOutputSchemas())
	}

	forma, nags, err := app.ExtractResources(opts.Query)
	if err != nil {
		msg, renderErr := renderer.RenderErrorMessage(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	if forma == nil || len(forma.Resources) == 0 {
		fmt.Println("No resources found")
		return nil
	}

	_, err = os.Stat(opts.TargetPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error checking target path: %v", err)
	}

	if _, err = os.Stat(opts.TargetPath); err == nil && !opts.Yes {
		prompter := prompter.NewBasicPrompter()
		if !prompter.Confirm(fmt.Sprintf("File '%s' already exists. Overwrite?", opts.TargetPath), false) {
			fmt.Println("Operation cancelled")
			return nil
		}
	}

	res, err := app.GenerateSourceCode(forma, opts.TargetPath, opts.OutputSchema, opts.SchemaLocation)
	if errors.Is(err, plugin.ErrFailedToGenerateSources) {
		logFilePath := fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory())
		return fmt.Errorf("something went wrong during the extraction. This is our fault. Please contact us and send over the error logs from '%s'", logFilePath)
	}
	if err != nil {
		return fmt.Errorf("error generating source code: %v", err)
	}

	if res.InitializedNewProject {
		display.Gold(fmt.Sprintf("Initialzed %s project at '%s'\n", opts.OutputSchema, res.ProjectPath))
	}
	for _, warning := range res.Warnings {
		display.Warning(warning)
	}
	display.Success(fmt.Sprintf("Successfully extracted %d resource(s) as Pkl to '%s'\n", res.ResourceCount, res.TargetPath))

	nag.MaybePrintNags(nags)

	return nil
}

func validateExtractOptions(opts *ExtractOptions) error {
	if opts.TargetPath == "" {
		return cmd.FlagErrorf("target file is required")
	}

	if strings.TrimSpace(opts.Query) == "" {
		return cmd.FlagErrorf("query is required")
	}

	return nil
}
