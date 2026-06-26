// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"errors"
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/spf13/cobra"
)

func newCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Print the active profile name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			active, err := s.Active()
			if err != nil {
				if errors.Is(err, store.ErrNotInitialized) {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no active profile yet")
					return nil
				}
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), active)
			return nil
		},
	}
}
