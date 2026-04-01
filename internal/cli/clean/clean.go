// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package clean

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/spf13/cobra"
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
			binPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("could not determine binary path: %w", err)
			}
			root := filepath.Dir(filepath.Dir(binPath))

			orb, err := mgr.New(slog.Default(), root, opsmgr.TreeConfig)
			if err != nil {
				return err
			}

			// init root if needed
			if !orb.Ready() {
				fmt.Printf("no managed installation root detected at: %s\n", root)
			}

			fmt.Println("done.")

			return nil
		},
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	return command
}
