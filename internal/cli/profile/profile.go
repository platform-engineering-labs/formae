// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
)

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
