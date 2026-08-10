// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"fmt"
	"io"
	"os"

	"github.com/platform-engineering-labs/formae/internal/cli/authmsg"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/spf13/cobra"
)

// LogoutCmd signs out of the active profile's auth plugin.
func LogoutCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "logout",
		Short: "Sign out of the active profile's auth plugin",
		Annotations: map[string]string{
			"type":     "Auth",
			"examples": "{{.Name}} {{.Command}}",
		},
		SilenceErrors: true,
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			configFile, _ := cmd.Flags().GetString("config")

			a, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}
			a.PrintBanner()

			client, err := a.AuthClient()
			if err != nil {
				return err
			}

			return runLogout(client, os.Stdout)
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	clicmd.AddConfigFlags(command)

	return command
}

// runLogout ends the current session on c and reports the outcome.
func runLogout(c authClient, out io.Writer) error {
	resp, err := c.Logout()
	if err != nil {
		return err
	}
	if resp.ErrorCode != "" || resp.Error != "" {
		return fmt.Errorf("%s", authmsg.DescribeAuthError(resp.ErrorCode, resp.Error))
	}

	_, _ = fmt.Fprintln(out, "signed out")
	return nil
}
