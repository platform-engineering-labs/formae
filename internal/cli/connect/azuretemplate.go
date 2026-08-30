// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	_ "embed"

	"github.com/spf13/cobra"

	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
)

// azureTemplateJSON is the ARM template that establishes the trust without
// formae ever holding a provisioning credential: a customer deploys it
// themselves (portal, their own az, or their own pipeline) and gives formae
// the clientId and tenantId outputs to register.
//
//go:embed assets/connect-azure.json
var azureTemplateJSON []byte

// azureTemplateCmd prints the embedded template. It is the only way a
// customer who will not hand the CLI provisioning credentials gets the
// template out of it: the credential-less path has no URL to fetch it from,
// so the binary that already shipped it is the source.
func azureTemplateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "template",
		Short:         "Print the ARM template that establishes trust without a provisioning credential",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cc *cobra.Command, args []string) error {
			_, err := cc.OutOrStdout().Write(azureTemplateJSON)
			return err
		},
	}
	c.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	return c
}
