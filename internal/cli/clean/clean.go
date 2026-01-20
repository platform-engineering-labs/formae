// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package clean

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/pkg/ppm"
)

func CleanCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "clean",
		Short: "Clean up old software versions",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} {{.Command}}",
		},
		SilenceErrors: true,
		RunE: func(command *cobra.Command, args []string) error {
			if !ppm.Sys.IsPrivilegedUser() {
				fmt.Println("this command requires a privileged user: authentication may be required")

				err := ppm.Sys.InvokeSelfWithSudo(command.Name())
				if err != nil {
					return fmt.Errorf("could not escalate to privileged user: %w", err)
				}
			}

			manager, err := ppm.NewManager(&ppm.Config{Repo: &ppm.RepoConfig{}}, formae.DefaultInstallPrefix)
			if err != nil {
				return err
			}

			manager.Clean()

			fmt.Println("done.")

			return nil
		},
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	return command
}
