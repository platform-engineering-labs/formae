// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package login implements the generic `formae login` and `formae logout`
// commands. Both are driven through the active profile's auth plugin, which
// is discovered and started the same way any other authenticated command
// starts one — see internal/cli/app.App.AuthClient.
package login

import (
	"fmt"
	"io"
	"os"

	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/logging"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	"github.com/spf13/cobra"
)

// authClient is the subset of *pkgauth.Client that drives login and logout.
// Depending on this narrow interface, rather than the concrete client, lets
// tests exercise the command logic against a stub with no plugin subprocess.
type authClient interface {
	LoginStart(*pkgauth.LoginStartRequest) (*pkgauth.LoginStartResponse, error)
	LoginWait(*pkgauth.LoginWaitRequest) (*pkgauth.LoginWaitResponse, error)
	Logout() (*pkgauth.LogoutResponse, error)
}

// LoginCmd signs in through the active profile's auth plugin.
func LoginCmd() *cobra.Command {
	var force, device bool

	command := &cobra.Command{
		Use:   "login",
		Short: "Sign in through the active profile's auth plugin",
		Long: `Sign in through the active profile's auth plugin.

The auth plugin decides how the flow works: opening a browser (the default)
or, with --device, printing a code to enter on another device. Running
login again while already signed in is a no-op unless --force is given.`,
		Annotations: map[string]string{
			"type":     "Auth",
			"examples": "{{.Name}} {{.Command}}|{{.Name}} {{.Command}} --device",
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

			client, err := a.AuthClient()
			if err != nil {
				return err
			}

			return runLogin(client, os.Stdout, force, device)
		},
	}

	command.Flags().BoolVar(&force, "force", false, "re-authenticate even if already signed in")
	command.Flags().BoolVar(&device, "device", false, "use a device code instead of opening a browser")
	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	clicmd.AddConfigFlags(command)

	return command
}

// runLogin drives the two-call login flow against c: LoginStart returns
// either an already-authenticated identity (short-circuiting before any
// LoginWait call) or the URL/code to render, then LoginWait blocks until the
// flow completes and returns the signed-in identity.
func runLogin(c authClient, out io.Writer, force, device bool) error {
	mode := "browser"
	if device {
		mode = "device"
	}

	startResp, err := c.LoginStart(&pkgauth.LoginStartRequest{Mode: mode, Force: force})
	if err != nil {
		return err
	}
	if startResp.ErrorCode != "" || startResp.Error != "" {
		return fmt.Errorf("%s", describeAuthError(startResp.ErrorCode, startResp.Error))
	}

	if startResp.Status == "already_authenticated" {
		_, _ = fmt.Fprintf(out, "already signed in as %s\n", startResp.SubjectName)
		return nil
	}

	if startResp.Method == "device" {
		_, _ = fmt.Fprintf(out, "Visit %s and enter code: %s\n", startResp.VerificationURI, startResp.UserCode)
	} else {
		_, _ = fmt.Fprintf(out, "Open this URL to sign in:\n  %s\n", startResp.BrowserURL)
	}

	waitResp, err := c.LoginWait(&pkgauth.LoginWaitRequest{SessionID: startResp.SessionID})
	if err != nil {
		return err
	}
	if waitResp.ErrorCode != "" || waitResp.Error != "" {
		return fmt.Errorf("%s", describeAuthError(waitResp.ErrorCode, waitResp.Error))
	}

	_, _ = fmt.Fprintf(out, "signed in as %s\n", waitResp.SubjectName)
	return nil
}

// describeAuthError maps an auth plugin ErrorCode to the copy formae shows
// the user. An empty or unrecognised code — including one from a plugin
// built against a newer SDK than this CLI knows about — degrades to
// fallback, the plugin's own error text, rather than failing or going blank.
func describeAuthError(code pkgauth.ErrorCode, fallback string) string {
	switch code {
	case pkgauth.ErrorCodeUnsupported:
		return "the active profile's auth plugin does not support this operation"
	case pkgauth.ErrorCodeNotLoggedIn:
		return "not signed in — run 'formae login'"
	case pkgauth.ErrorCodeSessionExpired:
		return "your session expired — run 'formae login'"
	case pkgauth.ErrorCodeIssuerUnreachable:
		return "the identity provider is unreachable — try again shortly"
	default:
		return fallback
	}
}
