// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSaveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "save <name>",
		Short: "Snapshot the active profile under a new name (does not switch)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			if err := s.Save(args[0], force); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "saved %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing profile")
	return cmd
}
