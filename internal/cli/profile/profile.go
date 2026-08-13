// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/banner"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// applyTheme resolves the active profile's configured CLI theme, points the
// banner at it so the logo follows the theme, and returns it so the command
// body renders in the same theme instead of a hardcoded default. Package var so
// tests can stub the resolution.
var applyTheme = func(_ *cobra.Command) *theme.Theme {
	th := resolveProfileTheme()
	banner.SetTheme(th)
	return th
}

// resolveProfileTheme reads the active profile's configured theme as a pure
// read, falling back to the default on any error. Unlike
// cmd.ResolveConfiguredTheme it must not bootstrap/migrate the store: the
// read-only profile commands (list/current/...) are contractually pure reads on
// a clean dir, so it consults the active pointer directly (never store.Resolve,
// which runs ensureInitialized) and returns the default when there is no active
// profile or its config can't be loaded.
func resolveProfileTheme() *theme.Theme {
	def := theme.New("formae")
	s, err := openStore()
	if err != nil {
		return def
	}
	active, err := s.Active()
	if err != nil || active == "" {
		return def
	}
	a := &app.App{}
	if err := a.LoadConfig(s.ProfilePath(active), ""); err != nil {
		return def
	}
	return a.Theme()
}

// ProfileCmd returns the `formae profile` command group.
func ProfileCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "profile",
		Short: "Manage named formae configuration profiles",
		Annotations: map[string]string{
			"type":     "Configuration",
			"examples": "{{.Name}} {{.Command}} list\n{{.Name}} {{.Command}} use prod\n{{.Name}} {{.Command}} create staging",
		},
		SilenceErrors: true,
	}
	command.AddCommand(
		newListCmd(),
		newCurrentCmd(),
		newShowCmd(),
		newUseCmd(),
		newSaveCmd(),
		newCreateCmd(),
		newEditCmd(),
		newDeleteCmd(),
		newDiffCmd(),
	)
	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)
	return command
}

// openStore resolves the config dir and returns a store.Store.
func openStore() (*store.Store, error) {
	root, err := store.ResolveConfigDir()
	if err != nil {
		return nil, err
	}
	return store.New(root), nil
}

// addOutputFlags registers the standard --output-consumer / --output-schema
// flags used across the CLI for commands that can emit machine-readable output.
func addOutputFlags(c *cobra.Command) {
	c.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	c.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
}

// resolveOutput reads and validates the output flags, matching the convention
// used by the agent-connecting commands (plugin, status, inventory, …).
func resolveOutput(c *cobra.Command) (printer.Consumer, string, error) {
	consumerFlag, _ := c.Flags().GetString("output-consumer")
	schema, _ := c.Flags().GetString("output-schema")
	consumer := printer.Consumer(consumerFlag)
	if consumer != printer.ConsumerHuman && consumer != printer.ConsumerMachine {
		return "", "", cmd.FlagErrorf("output-consumer must be 'human' or 'machine'")
	}
	if consumer == printer.ConsumerMachine && schema != "json" && schema != "yaml" {
		return "", "", cmd.FlagErrorf("output-schema must be either 'json' or 'yaml' for machine consumer")
	}
	return consumer, schema, nil
}
