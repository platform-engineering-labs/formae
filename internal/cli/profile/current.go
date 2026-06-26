// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"errors"
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/spf13/cobra"
)

// currentOutput is the machine-readable shape of `profile current`.
type currentOutput struct {
	Active *string `json:"active" yaml:"active"`
}

func newCurrentCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "current",
		Short: "Print the active profile name",
		Args:  cobra.NoArgs,
		RunE: func(cc *cobra.Command, args []string) error {
			consumer, schema, err := resolveOutput(cc)
			if err != nil {
				return err
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			active, err := s.Active()
			if err != nil && !errors.Is(err, store.ErrNotInitialized) {
				return err
			}

			if consumer == printer.ConsumerMachine {
				out := currentOutput{}
				if active != "" {
					out.Active = &active
				}
				p := printer.NewMachineReadablePrinter[currentOutput](cc.OutOrStdout(), schema)
				return p.Print(&out)
			}

			if active == "" {
				_, _ = fmt.Fprintln(cc.OutOrStdout(), "no active profile yet")
				return nil
			}
			_, _ = fmt.Fprintln(cc.OutOrStdout(), active)
			return nil
		},
	}
	addOutputFlags(c)
	return c
}
