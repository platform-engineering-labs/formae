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

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/authmsg"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
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

// loginIsTerminal is a package seam so tests can force piped (non-TTY) behavior.
var loginIsTerminal = tui.IsTerminal

// themeFor resolves the active theme from the app config.
// The name falls back to "formae" for nil configs (theme.New nil-guards internally).
func themeFor(a *app.App) *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

// ackLine emits a single acknowledgment line to w. On a TTY it renders with
// lipgloss styling; when piped it writes plain text so output stays ANSI-free.
func ackLine(w io.Writer, tty bool, th *theme.Theme, m components.AckMarker, text string) {
	if tty {
		_, _ = fmt.Fprintln(w, components.AckLine(th, m, text))
		return
	}
	_, _ = fmt.Fprintln(w, components.AckLinePlain(m, text))
}

// LoginCmd signs in through the active profile's auth plugin.
func LoginCmd() *cobra.Command {
	var device bool

	command := &cobra.Command{
		Use:   "login",
		Short: "Sign in through the active profile's auth plugin",
		Long: `Sign in through the active profile's auth plugin.

The auth plugin decides how the flow works: opening a browser (the default)
or, with --device, printing a code to enter on another device. Running
login again while already signed in is a no-op. To sign in as someone else,
run logout first.`,
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
			a.PrintBanner()

			client, err := a.AuthClient()
			if err != nil {
				return err
			}

			return runLogin(client, os.Stdout, themeFor(a), device)
		},
	}

	command.Flags().BoolVar(&device, "device", false, "use a device code instead of opening a browser")
	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	clicmd.AddConfigFlags(command)

	return command
}

// runLogin drives the two-call login flow against c: LoginStart returns
// either an already-authenticated identity (short-circuiting before any
// LoginWait call) or the URL/code to render, then LoginWait blocks until the
// flow completes and returns the signed-in identity. The browser URL and
// device-code lines are instructions ("do this next"), not completions, so
// they print plain — formae has no established styling convention for that
// kind of prose (compare plugin/init.go's plain numbered next-steps); only
// the completion lines (the sign-in acknowledgments) carry an ack marker.
func runLogin(c authClient, out io.Writer, th *theme.Theme, device bool) error {
	tty := loginIsTerminal(out)

	mode := "browser"
	if device {
		mode = "device"
	}

	startResp, err := c.LoginStart(&pkgauth.LoginStartRequest{Mode: mode})
	if err != nil {
		return err
	}
	if startResp.ErrorCode != "" || startResp.Error != "" {
		return fmt.Errorf("%s", authmsg.DescribeAuthError(startResp.ErrorCode, startResp.Error))
	}

	if startResp.Status == "already_authenticated" {
		printSignedIn(out, tty, th, "already signed in", startResp.SubjectName, startResp.Subject)
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
		return fmt.Errorf("%s", authmsg.DescribeAuthError(waitResp.ErrorCode, waitResp.Error))
	}

	printSignedIn(out, tty, th, "signed in", waitResp.SubjectName, waitResp.Subject)
	return nil
}

// printSignedIn renders verb ("signed in" / "already signed in") followed by
// the best available identity label as a completion ack line. Both
// SubjectName (a display hint) and Subject (a stable id) are documented as
// optional in pkg/auth — nothing obliges a plugin to set either — so this
// falls back from SubjectName to Subject and, if neither is set, drops the
// "as <name>" clause entirely rather than printing a message with nothing
// after "as ".
func printSignedIn(out io.Writer, tty bool, th *theme.Theme, verb, subjectName, subject string) {
	name := subjectName
	if name == "" {
		name = subject
	}
	text := verb
	if name != "" {
		text = fmt.Sprintf("%s as %s", verb, name)
	}
	ackLine(out, tty, th, components.AckDone, text)
}
