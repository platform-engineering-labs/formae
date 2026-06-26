// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
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
