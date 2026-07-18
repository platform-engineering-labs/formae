// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package extract

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/nag"
	"github.com/platform-engineering-labs/formae/internal/cli/renderer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/schema"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/spf13/cobra"
)

// Package-level seams — replaced in tests to avoid TTY / network calls.
var (
	isInteractive = tui.IsInteractive
	runConfirm    = components.RunConfirm
	promptPath    = defaultPromptPath

	extractFn = func(a *app.App, query string) (*pkgmodel.Forma, []string, error) {
		return a.ExtractResources(query, false)
	}
	generateFn = func(a *app.App, forma *pkgmodel.Forma, targetPath, outputSchema string, schemaLocation schema.SchemaLocation) (schema.GenerateSourcesResult, error) {
		return a.GenerateSourceCode(forma, targetPath, outputSchema, schemaLocation)
	}
)

// defaultPromptPath shows a huh text-input pre-filled with defaultVal and
// returns the user's chosen path. Called only when interactive && no path given.
func defaultPromptPath(th *theme.Theme, defaultVal string) (string, error) {
	result := defaultVal
	input := huh.NewInput().
		Title("Extract to:").
		Value(&result)
	form := components.NewThemedForm(th, huh.NewGroup(input))
	if err := form.Run(); err != nil {
		return "", err
	}
	return result, nil
}

// themeFor resolves the active theme from the app config.
// The name falls back to "formae" for nil configs (theme.New nil-guards internally).
func themeFor(a *app.App) *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

type ExtractOptions struct {
	TargetPath     string
	Query          string
	Yes            bool
	OutputSchema   string
	SchemaLocation schema.SchemaLocation
	// pathExplicit tracks whether the target path was explicitly provided
	// (positional arg present), so we know when to show the path prompt.
	pathExplicit bool
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
			opts.pathExplicit = opts.TargetPath != ""
			opts.Query, _ = command.Flags().GetString("query")
			opts.Yes, _ = command.Flags().GetBool("yes")
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")
			schemaLocation, _ := command.Flags().GetString("schema-location")
			loc, err := parseSchemaLocation(schemaLocation)
			if err != nil {
				return err
			}
			opts.SchemaLocation = loc

			configFile, _ := command.Flags().GetString("config")
			a, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			return runExtract(a, opts)
		},
		Annotations: map[string]string{
			"type": "Forma",
			"examples": "formae extract --query 'type:AWS::S3::Bucket' ./buckets.pkl" +
				" | formae extract --query 'type:GCP::Compute::* managed:false' ./gcp-unmanaged.pkl" +
				" | formae extract --query 'stack:prod target:eu target:us' ./prod.pkl",
			"args": "[<target file>]",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("query", " ", "Query that allows to find resources by their attributes. Use * as a wildcard anywhere (e.g. foo*, *foo, *foo*, foo*bar). ? and regex are not yet supported.")
	command.Flags().Bool("yes", false, "Overwrite existing files without prompting")
	command.Flags().String("output-schema", "pkl", "Output schema (only 'pkl' is currently supported)")
	command.Flags().String("schema-location", "remote", "How plugin PKL schemas are referenced in the generated PklProject. 'remote' (default) emits package:// URIs that PKL fetches from the hub. 'local' emits local file imports against the agent's on-disk PklProject paths; requires CLI and agent to share a filesystem.")
	cmd.AddConfigFlags(command)

	return command
}

// parseSchemaLocation maps the --schema-location flag value to the
// internal SchemaLocation enum. Returns a clear error for unsupported
// values rather than silently accepting them.
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

// runExtract is the entry point called by the Cobra command.
// It validates options, shows the path prompt when appropriate, then
// delegates to runExtractCore.
func runExtract(a *app.App, opts *ExtractOptions) error {
	th := themeFor(a)

	// Path prompt: shown when interactive && !--yes && no explicit path given.
	if !opts.pathExplicit && !opts.Yes {
		if !isInteractive() {
			return fmt.Errorf("interactive input requires a TTY — pass the target file as an argument")
		}
		chosen, err := promptPath(th, "./extracted.pkl")
		if err != nil {
			return err
		}
		opts.TargetPath = chosen
		opts.pathExplicit = true
	}

	if err := validateExtractOptions(opts); err != nil {
		return err
	}

	a.PrintBanner()

	return runExtractCore(a, opts)
}

// runExtractCore is the testable core of the extract flow.
// It assumes validateExtractOptions has already passed and the target path is set.
func runExtractCore(a *app.App, opts *ExtractOptions) error {
	th := themeFor(a)

	// Validate output schema when the app is available.
	if a != nil && !a.IsSupportedOutputSchema(opts.OutputSchema) {
		return fmt.Errorf("unsupported output schema '%s', supported schemas are: %v", opts.OutputSchema, a.SupportedOutputSchemas())
	}

	forma, nags, err := extractFn(a, opts.Query)
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

	// Check whether the target file already exists.
	_, statErr := os.Stat(opts.TargetPath)
	fileExists := statErr == nil

	if fileExists && !opts.Yes {
		// D8: overwrite confirm requires a TTY.
		if !isInteractive() {
			return fmt.Errorf("interactive input requires a TTY — pass --yes to overwrite '%s' non-interactively", opts.TargetPath)
		}
		ok, err := runConfirm(th, fmt.Sprintf("File '%s' already exists. Overwrite?", opts.TargetPath), "")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Operation cancelled")
			return nil
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("error checking target path: %v", statErr)
	}

	res, err := generateFn(a, forma, opts.TargetPath, opts.OutputSchema, opts.SchemaLocation)
	if errors.Is(err, schema.ErrFailedToGenerateSources) {
		logFilePath := fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory())
		return fmt.Errorf("something went wrong during the extraction. This is our fault. Please contact us and send over the error logs from '%s'", logFilePath)
	}
	if err != nil {
		return fmt.Errorf("error generating source code: %v", err)
	}

	warnStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning)
	if res.InitializedNewProject {
		fmt.Println(warnStyle.Render(fmt.Sprintf("Initialized %s project at '%s'", opts.OutputSchema, res.ProjectPath)))
	}
	for _, warning := range res.Warnings {
		fmt.Println(warnStyle.Render(warning))
	}

	// Build per-resource list from the forma that was extracted.
	extracted := make([]extractedResource, 0, len(forma.Resources))
	for _, r := range forma.Resources {
		extracted = append(extracted, extractedResource{
			Label: r.Label,
			Type:  r.Type,
			Stack: r.Stack,
		})
	}

	// Themed summary (styled when stdout is a TTY).
	if tui.IsTerminal(os.Stdout) {
		fmt.Println(renderExtractSummary(th, res.TargetPath, extracted))
	} else {
		fmt.Printf("Extracted %d resource(s) to '%s'\n", len(extracted), res.TargetPath)
	}

	nag.MaybePrintNags(themeFor(a), nags)

	return nil
}

func validateExtractOptions(opts *ExtractOptions) error {
	if opts.TargetPath == "" {
		return cmd.FlagErrorf("target file is required")
	}

	info, err := os.Stat(opts.TargetPath)
	if err == nil && info.IsDir() {
		return cmd.FlagErrorf("target path '%s' is a directory, not a file", opts.TargetPath)
	}

	if strings.TrimSpace(opts.Query) == "" {
		return cmd.FlagErrorf("query is required")
	}

	return nil
}
