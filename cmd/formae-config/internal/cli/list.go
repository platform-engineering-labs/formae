// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/platform-engineering-labs/formae/cmd/formae-config/internal/profiles"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all profiles, marking the active one with *",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			names, err := s.List()
			if err != nil {
				return err
			}
			active, err := s.Active()
			if err != nil && !errors.Is(err, profiles.ErrNotInitialized) {
				return err
			}
			if asJSON {
				out := struct {
					Active   *string  `json:"active"`
					Profiles []string `json:"profiles"`
				}{Profiles: names}
				if active != "" {
					out.Active = &active
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				return enc.Encode(out)
			}
			for _, n := range names {
				marker := "  "
				if n == active {
					marker = "* "
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n", marker, n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable output")
	return cmd
}
