// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/banner"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/schema"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RootCmdUsageTemplate and SimpleCmdUsageTemplate are built in init() so that
// theme colors are resolved at startup rather than at package-init import time.
var RootCmdUsageTemplate string
var SimpleCmdUsageTemplate string

func init() {
	th := theme.New("formae")
	grey := func(s string) string { return lipgloss.NewStyle().Foreground(th.Palette.TextSubtle).Render(s) }
	accent := func(s string) string { return lipgloss.NewStyle().Foreground(th.Palette.SecondaryAccent).Render(s) }
	done := func(s string) string { return lipgloss.NewStyle().Foreground(th.Palette.Done).Render(s) }

	RootCmdUsageTemplate = grey("Usage: ") + done("{{.CommandPath}} [OPTIONS]{{if .HasAvailableSubCommands}} [COMMAND]{{end}}\n") +
		"{{if .HasAvailableSubCommands}}\n" + accent("Commands:") + "{{$types := typeMap .Commands}}" +
		"{{$first := true}}{{range $type, $cmds := $types}}" +
		"{{if $first}}{{$first = false}}{{else}}\n{{end}}\n  " + accent("{{$type}}:") +
		"{{range $cmd := $cmds}}\n    " + done("{{rpad $cmd.Name $cmd.NamePadding}}") + "     {{$cmd.Short}}" +
		"{{if (index $cmd.Annotations \"examples\")}}\n                   " +
		grey("  {{formatExamples (index $cmd.Annotations \"examples\") $cmd}}") + "{{end}}" +
		"{{if (index $cmd.Annotations \"doc\")}}\n" +
		grey("{{formatDoc (index $cmd.Annotations \"doc\") $cmd}}\n") + "{{end}}" +
		"{{end}}{{end}}\n{{end}}" +
		"{{if .HasAvailableLocalFlags}}\n" + accent("Options:") + "\n" +
		"{{range .LocalFlags | optionsUsage}}{{.}}\n{{end}}" +
		"{{end}}" +
		banner.DefaultLinks() +
		"\n"

	SimpleCmdUsageTemplate = grey("Usage: ") + done("{{.CommandPath}}{{if .HasAvailableLocalFlags}} [OPTIONS]{{end}}{{if .HasAvailableSubCommands}} [COMMAND]{{end}}") +
		done("{{if index .Annotations \"args\"}} {{index .Annotations \"args\"}}{{end}}") + "\n" +
		"{{if index .Annotations \"examples\"}}\n" + accent("Examples:") + "\n  " +
		grey("{{formatExamplesMultiline (index .Annotations \"examples\") .}}") + "\n{{end}}" +
		"{{if .HasAvailableSubCommands}}\n" + accent("Commands:") +
		"{{range $cmd := .Commands}}\n  " + done("{{rpad $cmd.Name $cmd.NamePadding}}") + "       {{$cmd.Short}}" +
		"{{if (index $cmd.Annotations \"examples\")}}\n                   " +
		grey("  {{formatExamples (index $cmd.Annotations \"examples\") $cmd}}") + "{{end}}" +
		"{{if (index $cmd.Annotations \"doc\")}}\n" +
		grey("{{formatDoc (index $cmd.Annotations \"doc\") $cmd}}\n") + "{{end}}" +
		"{{end}}\n{{end}}" +
		"{{if .HasAvailableLocalFlags}}\n" + accent("Options:") + "\n" +
		"{{range .LocalFlags | optionsUsage}}{{.}}\n{{end}}" +
		"{{end}}\n" +
		"{{if .LocalFlags | hasPropertyFlags}}\n" + accent("Properties:") + "\n" +
		"{{range .LocalFlags | propertyUsage}}{{.}}\n{{end}}" +
		"{{end}}" +
		banner.DefaultLinks() +
		"\n"
}

var PropertyCommands = []string{
	"apply",
	"destroy",
	"eval",
}

// AddConfigFlags registers --config and --profile on a command and marks them
// mutually exclusive. Call from every command that connects to the agent.
func AddConfigFlags(c *cobra.Command) {
	c.Flags().String("config", "", "Path to config file")
	c.Flags().String("profile", "", "Named profile to use (see `formae profile list`)")
	c.MarkFlagsMutuallyExclusive("config", "profile")
}

// ResolveConfigPath turns the --config / --profile flags into a concrete config
// file path. Exactly one of config/profile may be non-empty (cobra enforces the
// mutual exclusion). With neither, it resolves the active profile (running
// migration/bootstrap).
func ResolveConfigPath(configFlag, profileFlag string) (string, error) {
	if profileFlag != "" {
		if err := store.ValidateName(profileFlag); err != nil {
			return "", err // path-traversal / malformed name guard.
		}
		dir, err := store.ResolveConfigDir()
		if err != nil {
			return "", err
		}
		s := store.New(dir)
		path := s.ProfilePath(profileFlag)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("%w: %s", store.ErrNotFound, profileFlag)
			}
			return "", err
		}
		return path, nil
	}
	if configFlag != "" {
		return configFlag, nil
	}
	dir, err := store.ResolveConfigDir()
	if err != nil {
		return "", err
	}
	return store.New(dir).Resolve()
}

func AppFromContext(ctx context.Context, configFilePath, endpoint string, cmd *cobra.Command) (*app.App, error) {
	if ctx.Value("app") != nil {
		application := ctx.Value("app").(*app.App)

		profileFlag, _ := cmd.Flags().GetString("profile") // "" if the flag is absent
		path, err := ResolveConfigPath(configFilePath, profileFlag)
		if err != nil {
			th := theme.New("formae")
			accentStyle := lipgloss.NewStyle().Foreground(th.Palette.SecondaryAccent)
			return nil, fmt.Errorf("%w\n\n%s %s", err, accentStyle.Render("Configuration docs:"), banner.DocRoot+"/configuration")
		}
		if err := application.LoadConfig(path, ""); err != nil {
			th := theme.New("formae")
			accentStyle := lipgloss.NewStyle().Foreground(th.Palette.SecondaryAccent)
			return nil, fmt.Errorf("%w\n\n%s %s", err, accentStyle.Render("Configuration docs:"), banner.DocRoot+"/configuration")
		}
		return application, nil
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

	// Ensure auth plugin subprocess is cleaned up when the CLI exits.
	existingPostRun := cmd.PersistentPostRun
	cmd.PersistentPostRun = func(cmd *cobra.Command, args []string) {
		app.Close()
		if existingPostRun != nil {
			existingPostRun(cmd, args)
		}
	}

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
		for _, fileExtension := range schema.DefaultRegistry.SupportedFileExtensions() {
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
