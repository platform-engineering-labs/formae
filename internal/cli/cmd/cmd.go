// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

var RootCmdUsageTemplate = display.Grey("Usage: ") + display.Green("{{.CommandPath}} [OPTIONS]{{if .HasAvailableSubCommands}} [COMMAND]{{end}}\n") +
	"{{if .HasAvailableSubCommands}}\n" + display.Gold("Commands:") + "{{$types := typeMap .Commands}}" +
	"{{$first := true}}{{range $type, $cmds := $types}}" +
	"{{if $first}}{{$first = false}}{{else}}\n{{end}}\n  " + display.Gold("{{$type}}:") +
	"{{range $cmd := $cmds}}\n    " + display.Green("{{rpad $cmd.Name $cmd.NamePadding}}") + "     {{$cmd.Short}}" +
	"{{if (index $cmd.Annotations \"examples\")}}\n                   " +
	display.Grey("  {{formatExamples (index $cmd.Annotations \"examples\") $cmd}}") + "{{end}}" +
	"{{if (index $cmd.Annotations \"doc\")}}\n" +
	display.Grey("{{formatDoc (index $cmd.Annotations \"doc\") $cmd}}\n") + "{{end}}" +
	"{{end}}{{end}}\n{{end}}" +
	"{{if .HasAvailableLocalFlags}}\n" + display.Gold("Options:\n") +
	"{{range .LocalFlags | optionsUsage}}{{.}}\n{{end}}" +
	"{{end}}" +
	display.Links("Docs", "cli/{{.Name}}") +
	"\n"

var SimpleCmdUsageTemplate = display.Grey("Usage: ") + display.Green("{{.CommandPath}}{{if .HasAvailableLocalFlags}} [OPTIONS]{{end}}{{if .HasAvailableSubCommands}} [COMMAND]{{end}}") +
	display.Green("{{if index .Annotations \"args\"}} {{index .Annotations \"args\"}}{{end}}") + "\n" +
	"{{if .HasAvailableSubCommands}}\n" + display.Gold("Commands:") +
	"{{range $cmd := .Commands}}\n  " + display.Green("{{rpad $cmd.Name $cmd.NamePadding}}") + "       {{$cmd.Short}}" +
	"{{if (index $cmd.Annotations \"examples\")}}\n                   " +
	display.Grey("  {{formatExamples (index $cmd.Annotations \"examples\") $cmd}}") + "{{end}}" +
	"{{if (index $cmd.Annotations \"doc\")}}\n" +
	display.Grey("{{formatDoc (index $cmd.Annotations \"doc\") $cmd}}\n") + "{{end}}" +
	"{{end}}\n{{end}}" +
	"{{if .HasAvailableLocalFlags}}\n" + display.Gold("Options:\n") +
	"{{range .LocalFlags | optionsUsage}}{{.}}\n{{end}}" +
	"{{end}}\n" +
	"{{if .LocalFlags | hasPropertyFlags}}\n" + display.Gold("Properties:\n") +
	"{{range .LocalFlags | propertyUsage}}{{.}}\n{{end}}" +
	"{{end}}" +
	display.Links("Docs", "cli/{{.Name}}") +
	"\n"

var PropertyCommands = []string{
	"apply",
	"destroy",
	"eval",
}

func AppFromContext(ctx context.Context, configFilePath, endpoint string, cmd *cobra.Command) (*app.App, error) {
	if ctx.Value("app") != nil {
		app := ctx.Value("app").(*app.App)

		err := app.LoadConfig(configFilePath, filepath.Join(config.Config.ConfigDirectory(), config.ConfigFileNamePrefix))
		if err != nil {
			return nil, fmt.Errorf("%w%s", err, display.Links("Configuration docs", "configuration"))
		}

		return app, nil
	}

	return nil, api.AppNotFoundError{}
}

// NOTE Cannot use cmd.Context because it is not part of the lifecycle yet
func SetPropertyFlagsOnCmd(ctx context.Context, cmd *cobra.Command) {
	var props map[string]pkgmodel.Prop

	if !slices.Contains(PropertyCommands, cmd.Name()) {
		return
	}

	if ctx.Value("forma.properties") != nil {
		props = ctx.Value("forma.properties").(map[string]pkgmodel.Prop)
	}

	// Setup Flags on Command
	for _, v := range props {
		switch v.Type {
		case "Boolean":
			if v.Default == nil {
				cmd.Flags().Bool(v.Flag, false, "property: "+v.Flag)
			} else {
				cmd.Flags().Bool(v.Flag, v.Default.(bool), "property: "+v.Flag)
			}
		case "Int":
			if v.Default == nil {
				cmd.Flags().Int(v.Flag, 0, "property: "+v.Flag)
			} else {
				cmd.Flags().Int(v.Flag, int(v.Default.(float64)), "property: "+v.Flag)
			}
		case "Float":
			if v.Default == nil {
				cmd.Flags().Float64(v.Flag, 0, "property: "+v.Flag)
			} else {
				cmd.Flags().Float64(v.Flag, v.Default.(float64), "property: "+v.Flag)
			}
		default:
			if v.Default == nil {
				cmd.Flags().String(v.Flag, "", "property: "+v.Flag)
			} else {
				cmd.Flags().String(v.Flag, fmt.Sprintf("%v", v.Default), "property: "+v.Flag)
			}
		}

		cmd.Flags().Lookup(v.Flag).Annotations = map[string][]string{"forma.property": {"true"}}

		if v.Default == nil {
			_ = cmd.MarkFlagRequired(v.Flag)
		}
	}
}

func InitCommandWithContext(cmd *cobra.Command) (*cobra.Command, error) {
	app := app.NewApp()
	ctx := context.WithValue(context.Background(), "app", app)

	dyn, path := IsDynamicCommand(app)
	if dyn {
		props, err := app.Projects.Properties(path)
		if err != nil {
			return nil, err
		}

		// filter props with no flags
		filteredProps := make(map[string]pkgmodel.Prop)
		for k, v := range props {
			if v.Flag != "" {
				filteredProps[k] = v
			}
		}

		if props != nil {
			ctx = context.WithValue(ctx, "forma.properties", filteredProps)
		}
	}

	cmd.SetContext(ctx)
	for _, sub := range cmd.Commands() {
		SetPropertyFlagsOnCmd(ctx, sub)
	}
	return cmd, nil
}

func IsDynamicCommand(app *app.App) (bool, string) {
	if len(os.Args) < 3 {
		return false, ""
	}

	if !slices.Contains(PropertyCommands, os.Args[1]) {
		return false, ""
	}

	for _, arg := range os.Args {
		for _, fileExtension := range app.PluginManager.SupportedFileExtensions() {
			if strings.Contains(arg, fileExtension) {
				return true, arg
			}
		}
	}

	return false, ""
}

// PropertiesFromCmd extracts property values from a cobra command based on the
// properties defined in the command's context. It returns a map of property names to their
// string values.
// NOTE: For evaluation, schema plugins only accept property strings which are then cast
// at eval time
func PropertiesFromCmd(cmd *cobra.Command) map[string]string {
	result := make(map[string]string)
	var props map[string]pkgmodel.Prop

	if cmd.Context().Value("forma.properties") != nil {
		props = cmd.Context().Value("forma.properties").(map[string]pkgmodel.Prop)
	}

	for _, v := range props {
		var val any

		switch v.Type {
		case "Int":
			val, _ = cmd.Flags().GetInt(v.Flag)
		case "Float":
			val, _ = cmd.Flags().GetFloat64(v.Flag)
		case "Boolean":
			val, _ = cmd.Flags().GetBool(v.Flag)
		default:
			val, _ = cmd.Flags().GetString(v.Flag)
		}

		result[v.Flag] = fmt.Sprintf("%v", val)
	}

	return result
}
