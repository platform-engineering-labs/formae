// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/spf13/cobra"

	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// profileResolveTimeout bounds a single profile's resolution, so one dead
// SSO session cannot hang the read of the rest. A var, not a const, so a
// test can shrink it rather than waiting out the real duration.
var profileResolveTimeout = 5 * time.Second

// profilesFallbackMessage is the internal-code text an undeclared error gets
// on the profiles path.
const profilesFallbackMessage = "formae could not list AWS profiles; run it without --output-consumer machine to see why"

func profilesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "profiles",
		Short:         "List local AWS profiles and the account each authenticates to",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cc *cobra.Command, args []string) error {
			return runConnectAWSProfiles(cc)
		},
	}
	clicmd.AddOutputFlags(c)
	c.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	return c
}

// runConnectAWSProfiles reads the local shared AWS config and reports every
// profile it names, each with the account its credentials resolve to. It is
// a local read: no cloud credentials of its own beyond the profile it is
// reading, and no control-plane session, so it works with no formae profile
// configured at all.
func runConnectAWSProfiles(cc *cobra.Command) error {
	consumer, schema, err := resolveOutputOrHuman(cc)
	if err != nil {
		return err
	}

	v, err := readAWSProfilesView(cc)
	if err != nil {
		return report(cc.OutOrStdout(), consumer, schema, err, profilesFallbackMessage)
	}

	if consumer == printer.ConsumerMachine {
		return emitProfiles(cc.OutOrStdout(), schema, v)
	}
	return printProfilesHuman(cc.OutOrStdout(), v)
}

// readAWSProfilesView enumerates the local AWS profiles and resolves each
// one's account. Enumeration failing outright (the shared config could not
// be read at all) fails the whole command: there is no profile list to build
// a partial document from. Once profiles are named, one being unresolvable
// is never fatal to the rest — it is reported alongside them, never in
// place of them.
func readAWSProfilesView(cc *cobra.Command) (profilesView, error) {
	names, err := listAWSProfiles()
	if err != nil {
		return profilesView{}, err
	}

	return profilesView{
		SchemaVersion: connectSchemaVersion,
		Phase:         "awsProfiles",
		Profiles:      resolveAWSProfiles(cc.Context(), names),
		Warnings:      []string{},
	}, nil
}

// resolveAWSProfiles resolves every named profile's account concurrently.
// Each goroutine owns a distinct index, so no coordination is needed beyond
// waiting for all of them; concurrency does not complicate the error
// handling here, since a per-profile failure only ever affects that
// profile's own row.
func resolveAWSProfiles(ctx context.Context, names []string) []profileResolution {
	results := make([]profileResolution, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			results[i] = resolveOneAWSProfile(ctx, name)
		}(i, name)
	}
	wg.Wait()
	return results
}

// resolveOneAWSProfile resolves one profile's account, bounded by its own
// timeout so a dead SSO session cannot hang the rest of the read. It asks no
// stated account of resolveCaller: this is a report, not a confirmation.
func resolveOneAWSProfile(ctx context.Context, name string) profileResolution {
	callCtx, cancel := context.WithTimeout(ctx, profileResolveTimeout)
	defer cancel()
	_, account, _, err := resolveCaller(callCtx, name)
	if err != nil {
		return profileResolution{Name: name, Unavailable: unavailableReason(err)}
	}
	return profileResolution{Name: name, Account: account}
}

// printProfilesHuman renders one line per profile: the account for one that
// resolved, or its reason for one that did not. No profiles at all gets the
// plain sentence rather than an empty listing.
func printProfilesHuman(w io.Writer, v profilesView) error {
	if len(v.Profiles) == 0 {
		_, err := fmt.Fprintln(w, "No AWS profiles were found in the local shared config.")
		return err
	}

	if _, err := fmt.Fprintln(w, "AWS profiles:"); err != nil {
		return err
	}
	for _, p := range v.Profiles {
		line := "  " + p.Name + "  "
		if p.Unavailable != "" {
			line += "unavailable: " + p.Unavailable
		} else {
			line += p.Account
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}
