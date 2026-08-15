// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connection

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
// Every error raised while the command runs becomes an envelope, not only the
// ones resolution declares: a consumer parses one protocol or it parses none,
// and the paths where that matters most are the degraded ones nobody
// anticipated. An error we did not declare is reported as internal rather than
// given a code that would imply we understood it.
//
// The limit is argv itself. An unknown flag, a bad argument count, or an
// unreadable --output-consumer fails before this runs, and exits non-zero with
// a plain message: the flags that say how to render a failure have not been
// established yet, so there is nothing to render it as. Those are caller bugs
// rather than runtime conditions — a consumer builds one fixed command line —
// and a consumer that cannot parse what it got should report the exit status
// rather than guess.
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
//
// The selection is made once and the configuration is then evaluated from that
// exact path. Resolving the path and re-reading the active pointer separately
// would let the pointer move between them, so the connection and credential
// would come from one profile while the reported name and the ambiguity
// decision described another — which is the very skew this command exists to
// rule out.
func runResolve(cc *cobra.Command, forceRefresh bool, cloud, issuer string) (view, error) {
	sel, err := selectProfile(cc)
	if err != nil {
		return view{}, err
	}

	a := &app.App{}
	if err := a.LoadConfig(sel.Path, ""); err != nil {
		return view{}, err
	}

	return resolve(input{
		Conn:         a.Config.Cli.Connection,
		Profile:      sel.Name,
		Explicit:     sel.Explicit,
		Profiles:     sel.Profiles,
		Creds:        &lazyCreds{app: a},
		ForceRefresh: forceRefresh,
		CloudFlag:    cloud,
		IssuerFlag:   issuer,
	})
}

// selection is one immutable choice of what to evaluate: which file, what to
// call it, whether the caller named it, and what else existed at that moment.
type selection struct {
	Path     string
	Name     string
	Explicit bool
	Profiles []string
}

// selectProfile decides what to evaluate, reading the store once.
//
// A named profile and an explicit config file are both explicit selections and
// neither can be ambiguous. A config file is not a profile at all, so it
// reports no name rather than borrowing the active one, which would describe an
// unrelated profile and could refuse a resolution as ambiguous that the caller
// had in fact pinned.
func selectProfile(cc *cobra.Command) (selection, error) {
	configFlag, _ := cc.Flags().GetString("config")
	profileFlag, _ := cc.Flags().GetString("profile")

	root, err := store.ResolveConfigDir()
	if err != nil {
		return selection{}, err
	}
	s := store.New(root)

	names, err := s.List()
	if err != nil {
		return selection{}, err
	}

	switch {
	case profileFlag != "":
		if err := store.ValidateName(profileFlag); err != nil {
			return selection{}, err
		}
		path := s.ProfilePath(profileFlag)
		if _, err := os.Stat(path); err != nil {
			return selection{}, fmt.Errorf("%w: %s", store.ErrNotFound, profileFlag)
		}
		return selection{Path: path, Name: profileFlag, Explicit: true, Profiles: names}, nil

	case configFlag != "":
		return selection{Path: configFlag, Explicit: true, Profiles: names}, nil

	default:
		// Resolve bootstraps and migrates, and returns the path of the profile
		// it settled on. The name is taken from that same answer rather than by
		// reading the pointer again, so both describe one moment.
		path, err := s.Resolve()
		if err != nil {
			return selection{}, err
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		return selection{Path: path, Name: name, Profiles: names}, nil
	}
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
