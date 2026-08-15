// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connection

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// ConnectionCmd returns the `formae connection` command group.
//
// Hidden because we do not advertise a hosted CLI surface: it is the contract a
// program reads configuration through, not something a person is expected to
// run. Hidden is not the same as unsafe — running it by hand is fine, and its
// human output masks the credential.
func ConnectionCmd() *cobra.Command {
	command := &cobra.Command{
		Use:           "connection",
		Short:         "Inspect the connection formae would use",
		Hidden:        true,
		SilenceErrors: true,
	}
	command.AddCommand(newResolveCmd())
	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	return command
}

func newResolveCmd() *cobra.Command {
	var forceRefresh bool
	var cloud, cloudIssuer string

	c := &cobra.Command{
		Use:   "resolve",
		Short: "Resolve the connection and credential for a profile",
		Args:  cobra.NoArgs,
		RunE: func(cc *cobra.Command, args []string) error {
			consumer, schema, err := clicmd.ResolveOutput(cc)
			if err != nil {
				// The output flags decide how a failure is rendered, so a
				// failure to read them cannot be rendered that way.
				return err
			}

			v, err := runResolve(cc, forceRefresh, cloud, cloudIssuer)
			if err != nil {
				return report(cc.OutOrStdout(), consumer, schema, err)
			}

			if consumer == printer.ConsumerMachine {
				return printer.NewMachineReadablePrinter[view](cc.OutOrStdout(), schema).Print(&v)
			}
			return printHuman(cc.OutOrStdout(), v)
		},
	}

	c.Flags().BoolVar(&forceRefresh, "force-refresh", false,
		"refresh the credential even if the stored one still looks fresh")
	c.Flags().StringVar(&cloud, "cloud", "", "control plane base URL")
	c.Flags().StringVar(&cloudIssuer, "cloud-issuer", "", "control plane issuer URL")
	_ = c.Flags().MarkHidden("cloud")
	_ = c.Flags().MarkHidden("cloud-issuer")
	clicmd.AddConfigFlags(c)
	clicmd.AddOutputFlags(c)
	c.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	return c
}

// report renders err and returns it so the process still exits non-zero.
//
// In machine mode every error becomes an envelope, not only the ones this
// command declares: a consumer parses one protocol or it parses none, and the
// paths where that matters most are the degraded ones nobody anticipated. An
// error we did not declare is reported as internal rather than given a code
// that would imply we understood it.
func report(w io.Writer, consumer printer.Consumer, schema string, err error) error {
	if consumer != printer.ConsumerMachine {
		return err
	}
	handled, perr := printer.PrintFailure(w, schema, err)
	if perr != nil {
		return perr
	}
	if !handled {
		if _, perr := printer.PrintFailure(w, schema,
			printer.Fail(printer.CodeInternal, err.Error(), nil)); perr != nil {
			return perr
		}
	}
	return err
}

// runResolve gathers what resolution decides from and runs it.
func runResolve(cc *cobra.Command, forceRefresh bool, cloud, issuer string) (view, error) {
	configFile, _ := cc.Flags().GetString("config")
	named, _ := cc.Flags().GetString("profile")

	a, err := clicmd.AppFromContext(cc.Context(), configFile, "", cc)
	if err != nil {
		return view{}, err
	}

	effective, profiles, err := profileContext(named)
	if err != nil {
		return view{}, err
	}

	return resolve(input{
		Conn:         a.Config.Cli.Connection,
		Profile:      effective,
		Explicit:     named != "",
		Profiles:     profiles,
		Creds:        &lazyCreds{app: a},
		ForceRefresh: forceRefresh,
		CloudFlag:    cloud,
		IssuerFlag:   issuer,
	})
}

// profileContext reports the effective profile name and every profile that
// exists. The effective name is part of the contract: a consumer never has to
// reason about what "active" meant at the time of the call.
func profileContext(named string) (string, []string, error) {
	root, err := store.ResolveConfigDir()
	if err != nil {
		return "", nil, err
	}
	s := store.New(root)

	names, err := s.List()
	if err != nil {
		return "", nil, err
	}
	if named != "" {
		return named, names, nil
	}
	active, err := s.Active()
	if err != nil {
		return "", nil, err
	}
	return active, names, nil
}

// lazyCreds builds the auth client only if a credential is actually wanted.
// Classic connections never reach it, and building one eagerly would fail on
// every profile that configures no auth plugin at all.
type lazyCreds struct {
	app    *app.App
	client credentialProvider
}

func (l *lazyCreds) GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error) {
	if l.client == nil {
		client, err := l.app.AuthClient()
		if err != nil {
			return nil, err
		}
		l.client = client
	}
	return l.client.GetAuthHeader(forceRefresh)
}

// printHuman renders the resolved connection for a person, with the credential
// masked. It answers "am I hosted, which installation, do I have a live
// session?" without being a way to print a token into a scrollback.
func printHuman(w io.Writer, v view) error {
	if _, err := fmt.Fprintf(w, "profile: %s\n", v.Profile); err != nil {
		return err
	}
	for _, k := range []string{"mode", "url", "port", "endpoint", "installation"} {
		if val, ok := v.Connection[k]; ok {
			if _, err := fmt.Fprintf(w, "%s: %v\n", k, val); err != nil {
				return err
			}
		}
	}
	if v.Credential != "" {
		if _, err := fmt.Fprintln(w, "credential: <redacted>"); err != nil {
			return err
		}
	}
	return nil
}
