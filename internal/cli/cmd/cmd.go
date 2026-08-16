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

	"github.com/platform-engineering-labs/formae/internal/cli/printer"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/banner"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/logo"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/schema"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RootCmdUsageTemplate and SimpleCmdUsageTemplate are built in init() so that
// theme colors are resolved at startup rather than at package-init import time.
var RootCmdUsageTemplate string
var SimpleCmdUsageTemplate string

func init() {
	RootCmdUsageTemplate, SimpleCmdUsageTemplate = buildUsageTemplates(theme.New("formae"))
}

// buildUsageTemplates renders the root and simple usage templates in th's
// colors. Help and usage output is rendered by cobra before a command's config
// has loaded, so init() builds these in the default theme and RethemeUsage
// rebuilds them once the active theme is known (see root.go).
func buildUsageTemplates(th *theme.Theme) (rootTpl, simpleTpl string) {
	// The usage highlight is a single accent color: the theme's wordmark color
	// when it sets one (rich → blue), else SecondaryAccent (quiet → orange). This
	// keeps each theme to one highlight in the usage, with command names in
	// neutral TextPrimary rather than a second color.
	accentColor := th.Palette.SecondaryAccent
	if wm := th.Palette.LogoWordmark; wm.Light != "" || wm.Dark != "" {
		accentColor = wm
	}
	grey := func(s string) string { return lipgloss.NewStyle().Foreground(th.Palette.TextSubtle).Render(s) }
	accent := func(s string) string { return lipgloss.NewStyle().Foreground(accentColor).Render(s) }
	name := func(s string) string { return lipgloss.NewStyle().Foreground(th.Palette.TextPrimary).Render(s) }

	rootTpl = grey("Usage: ") + name("{{.CommandPath}} [OPTIONS]{{if .HasAvailableSubCommands}} [COMMAND]{{end}}\n") +
		"{{if .HasAvailableSubCommands}}\n" + accent("Commands:") + "{{$types := typeMap .Commands}}" +
		"{{$first := true}}{{range $type, $cmds := $types}}" +
		"{{if $first}}{{$first = false}}{{else}}\n{{end}}\n  " + accent("{{$type}}:") +
		"{{range $cmd := $cmds}}\n    " + name("{{rpad $cmd.Name $cmd.NamePadding}}") + "     {{$cmd.Short}}" +
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

	simpleTpl = grey("Usage: ") + name("{{.CommandPath}}{{if .HasAvailableLocalFlags}} [OPTIONS]{{end}}{{if .HasAvailableSubCommands}} [COMMAND]{{end}}") +
		name("{{if index .Annotations \"args\"}} {{index .Annotations \"args\"}}{{end}}") + "\n" +
		"{{if index .Annotations \"examples\"}}\n" + accent("Examples:") + "\n  " +
		grey("{{formatExamplesMultiline (index .Annotations \"examples\") .}}") + "\n{{end}}" +
		"{{if .HasAvailableSubCommands}}\n" + accent("Commands:") +
		"{{range $cmd := .Commands}}\n  " + name("{{rpad $cmd.Name $cmd.NamePadding}}") + "       {{$cmd.Short}}" +
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
	return rootTpl, simpleTpl
}

// RethemeUsage rebuilds the usage templates in th's colors and re-applies the
// one c currently uses (root vs simple). Help/usage output renders before a
// command's config loads, so callers resolve the active theme at render time and
// call this to color the output. A command using neither template is untouched.
func RethemeUsage(c *cobra.Command, th *theme.Theme) {
	oldRoot, oldSimple := RootCmdUsageTemplate, SimpleCmdUsageTemplate
	RootCmdUsageTemplate, SimpleCmdUsageTemplate = buildUsageTemplates(th)
	switch c.UsageTemplate() {
	case oldRoot:
		c.SetUsageTemplate(RootCmdUsageTemplate)
	case oldSimple:
		c.SetUsageTemplate(SimpleCmdUsageTemplate)
	}
}

// ResolveConfiguredTheme best-effort resolves the CLI theme from c's
// --profile/--config flags (or the active profile when neither is set), for
// theming help/usage output. It falls back to the default theme on any error so
// help always renders.
func ResolveConfiguredTheme(c *cobra.Command) *theme.Theme {
	profileFlag, _ := c.Flags().GetString("profile")
	configFlag, _ := c.Flags().GetString("config")
	path, err := ResolveConfigPath(configFlag, profileFlag)
	if err != nil {
		return theme.New("formae")
	}
	a := &app.App{}
	if err := a.LoadConfig(path, ""); err != nil {
		return theme.New("formae")
	}
	return a.Theme()
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
		// Re-seed lipgloss's global dark-background now that the profile's
		// cli.appearance is known. root.go seeds from env+auto-detect before any
		// config is loaded; the config layer sits below the FORMAE_APPEARANCE env
		// override and above auto-detect, so it can only take effect here. Under
		// cli.theme="omarchy" with appearance left on auto, prefer the OS theme's
		// own light/dark declaration over a fragile OSC-11 probe.
		appearance := application.Config.Cli.Appearance
		if application.Config.Cli.Theme == "omarchy" && isAuto(appearance) {
			if osAppearance := theme.OmarchyAutoAppearance(); osAppearance != "" {
				appearance = osAppearance
			}
		}
		lipgloss.SetHasDarkBackground(logo.ResolveDarkBackground(appearance))
		return application, nil
	}

	return nil, api.AppNotFoundError{}
}

// isAuto reports whether appearance is unset or explicitly "auto" (the two
// cases that defer to auto-detection rather than pinning light/dark).
func isAuto(appearance string) bool {
	return appearance == "" || strings.EqualFold(appearance, "auto")
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

// AddOutputFlags registers the standard --output-consumer / --output-schema
// flags used across the CLI for commands that can emit machine-readable output.
func AddOutputFlags(c *cobra.Command) {
	c.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	c.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
}

// ResolveOutput reads and validates the output flags, matching the convention
// used by the agent-connecting commands (plugin, status, inventory, …).
func ResolveOutput(c *cobra.Command) (printer.Consumer, string, error) {
	consumerFlag, _ := c.Flags().GetString("output-consumer")
	schema, _ := c.Flags().GetString("output-schema")
	consumer := printer.Consumer(consumerFlag)
	if consumer != printer.ConsumerHuman && consumer != printer.ConsumerMachine {
		return "", "", FlagErrorf("output-consumer must be 'human' or 'machine'")
	}
	if consumer == printer.ConsumerMachine && schema != "json" && schema != "yaml" {
		return "", "", FlagErrorf("output-schema must be either 'json' or 'yaml' for machine consumer")
	}
	return consumer, schema, nil
}
