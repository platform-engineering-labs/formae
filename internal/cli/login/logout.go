// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/authmsg"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/logging"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/spf13/cobra"
)

// LogoutCmd signs out of the active profile's auth plugin.
func LogoutCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "logout",
		Short: "Sign out of the active profile's auth plugin",
		Long: `Sign out of the active profile's auth plugin.

On a hosted profile, the profiles login derived for that control plane are
removed along with the credential. The profile you are signed out of stays,
so you have something to sign back in with, and profiles you wrote yourself
are never touched.`,
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

			return runLogoutAndPrune(client, pruneStep{
				Conn:      a.Config.Cli.Connection,
				ConfigDir: store.ResolveConfigDir,
				Out:       os.Stdout,
				Theme:     themeFor(a),
			})
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	clicmd.AddConfigFlags(command)

	return command
}

// pruneStep is everything the prune half of a logout needs from the command.
//
// It carries no control-plane override and no client, and both absences are
// the design. The origin whose profiles are removed comes from the ledger
// entry bound to the profile the user just signed out of, so a stale
// FORMAE_CLOUD_URL cannot aim a removal at an environment they never touched;
// and no control plane is asked anything on this path, because the user's
// explicit sign-out is the authority rather than an inference from a snapshot.
type pruneStep struct {
	Conn      pkgmodel.Connection
	ConfigDir func() (string, error)
	Out       io.Writer
	Theme     *theme.Theme
}

// runLogoutAndPrune signs out and then removes the profiles login derived for
// the control plane that sign-out was against.
//
// The order is load-bearing in both directions. The auth plugin's config comes
// from the active profile, so removing profiles first could remove the block
// the sign-out has to be made with; and a sign-out that failed has not
// established that the credential is gone, so it removes nothing — deleting
// profiles while the credential may still work is the worse of the two errors.
func runLogoutAndPrune(c authClient, s pruneStep) error {
	if err := runLogout(c, s.Out, s.Theme); err != nil {
		return err
	}
	return runPrune(s)
}

// runLogout ends the current session on c and reports the outcome as a
// completion ack line.
func runLogout(c authClient, out io.Writer, th *theme.Theme) error {
	resp, err := c.Logout()
	if err != nil {
		return err
	}
	if resp.ErrorCode != "" || resp.Error != "" {
		return fmt.Errorf("%s", authmsg.DescribeAuthError(resp.ErrorCode, resp.Error))
	}

	ackLine(out, loginIsTerminal(out), th, components.AckDone, "signed out")
	return nil
}

// runPrune removes the profiles this formae derived for the control plane the
// user has just signed out of, and reports the outcome as this command's exit
// status.
//
// A sign-out and the removal of the profiles that went with it are separate
// facts, and every message here keeps them apart: the credential is gone
// whatever this does, so nothing it prints may read as a sign-out that failed.
func runPrune(s pruneStep) error {
	tty := loginIsTerminal(s.Out)

	hostedConn, isHosted := s.Conn.(*pkgmodel.HostedConnection)
	if !isHosted || hostedConn == nil {
		// A classic profile addresses the user's own agent, so no profile was
		// ever derived for it and there is nothing to say.
		return nil
	}

	dir, err := s.ConfigDir()
	if err != nil {
		// Resolved before anything is read, so a ledger and a profiles
		// directory are never looked for relative to the working directory.
		return fmt.Errorf("%s: %w", logoutIncomplete(""), err)
	}
	st := store.New(dir)

	l, warnings, err := loadLedger(st.ManagedLedgerPath())
	if err != nil {
		if errors.Is(err, errUnknownSchemaVersion) {
			// Those records belong to a newer formae: rewriting them would
			// destroy them, and removing a profile recorded by one of them
			// would remove a file we cannot read the record for. Signing out
			// still succeeded, so this is a notice and not a failure.
			printWarnings(s.Out, tty, s.Theme, append(warnings, fmt.Sprintf("%v; no profile was removed by this run", err)))
			return nil
		}
		return fmt.Errorf("%s: %w", logoutIncomplete(""), err)
	}

	bound, activeErr := boundEntries(l, st, hostedConn.Installation)
	if len(bound) != 1 {
		// Nothing licensed a removal, so what the ledger refused to believe is
		// reported here: there is no run to report it, and a conflicting set
		// is often exactly why no profile was removed.
		printWarnings(s.Out, tty, s.Theme, warnings)
		if marker, reason := noRemovalReason(l, bound, activeErr, st.ManagedLedgerPath()); reason != "" {
			ackLine(s.Out, tty, s.Theme, marker, "no profiles were removed: "+reason)
		}
		return nil
	}

	// The origin is the entry's own, canonicalised when the ledger was loaded
	// and so comparable with the values the prune matches against. A client
	// and a verifier are absent because a prune asks no control plane anything
	// and renders no profile.
	result := pruneAll(syncDeps{Store: st, Out: s.Out, TTY: tty, Theme: s.Theme}, bound[0].ControlPlane)
	printWarnings(s.Out, tty, s.Theme, result.Warnings)
	return pruneExit(result)
}

// boundEntries returns every valid ledger entry that binds to the active
// profile: one whose name resolves to that very file, that records the
// installation the profile addresses, and whose fingerprint the file's
// contents match.
//
// All three are needed, and the precision is what makes the removal safe. A
// profile file carries no control-plane origin, while the origin is what
// selects the profiles a sign-out removes, so the entry is the only thing that
// can say which environment was signed out of — and an entry that names a file
// this formae no longer wrote, or another installation, says nothing about
// this one. Quarantined entries are invisible here, as they are everywhere a
// decision is taken: a conflicting set authorises nothing.
func boundEntries(l *ledger, s *store.Store, installation string) ([]*ledgerEntry, error) {
	active, err := activeProfileFile(s)
	if err != nil {
		return nil, err
	}

	var bound []*ledgerEntry
	for _, e := range l.Authoritative() {
		if e.InstallationID != installation {
			continue
		}
		info, digest, err := statAndDigest(s.ProfilePath(e.Name))
		if err != nil || !e.records(digest) {
			continue
		}
		if os.SameFile(active, info) {
			bound = append(bound, e)
		}
	}
	return bound, nil
}

// noRemovalReason explains why a sign-out removed no profile, and how loudly
// to say it.
//
// An empty reason says nothing at all, and that case is the point of the
// function: a user who has just signed out of a profile they wrote themselves,
// on a machine this formae has never derived a profile on, should not be told
// about profiles that never existed. Where there is something to say, the
// marker separates the two kinds of silence — a profile that simply is not one
// of ours is a no-op, while a ledger that cannot say which control plane was
// signed out of has left profiles behind and is worth acting on.
func noRemovalReason(l *ledger, bound []*ledgerEntry, activeErr error,
	ledgerPath string) (components.AckMarker, string) {
	switch {
	case activeErr != nil:
		return components.AckWarn, fmt.Sprintf(
			"%v, so formae could not tell which of its profiles you signed out of", activeErr)

	case len(bound) > 1:
		// Entries are unique per origin, but two origins may record the same
		// name, and then nothing on disk says which of them this sign-out was
		// against. Choosing one would remove a whole environment's profiles on
		// a coin flip.
		return components.AckWarn, fmt.Sprintf(
			"profile %q is recorded for more than one control plane (%s), so formae cannot tell which one you "+
				"signed out of; remove %s to reset the ledger, which deletes no profile",
			bound[0].Name, strings.Join(origins(bound), ", "), ledgerPath)

	case len(l.entries) == 0:
		// A count, and never an authority: there is nothing to say because
		// this formae has recorded no profile at all, anywhere.
		return components.AckSkip, ""

	default:
		return components.AckSkip, "the active profile is not one formae derived"
	}
}

// origins returns the control planes the entries are recorded against.
func origins(entries []*ledgerEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ControlPlane)
	}
	return out
}

// printWarnings renders each warning as its own line, in the same
// acknowledgment idiom as the sign-in or sign-out line above it.
func printWarnings(w io.Writer, tty bool, th *theme.Theme, warnings []string) {
	for _, warning := range warnings {
		ackLine(w, tty, th, components.AckWarn, warning)
	}
}

// pruneExit maps a completed prune onto the command's exit status. Only a
// prune that did not complete is non-zero: a profile deliberately kept, or one
// left alone because it is no longer ours, is a warning and a steady state.
func pruneExit(r syncResult) error {
	if r.Fatal == nil {
		return nil
	}
	msg := logoutIncomplete(changesMade(r))
	if errors.Is(r.Fatal, errLedgerLocked) {
		// The lock's path is not something the user can act on; the other
		// process is. The failure stays in the chain all the same, so a caller
		// can still tell a contended ledger from a run that really failed; the
		// zero precision is what keeps its text out of the message.
		return fmt.Errorf(
			"%s: another formae process is updating them, so run formae logout again when it has finished%.0w",
			msg, r.Fatal)
	}
	return fmt.Errorf("%s: %w", msg, r.Fatal)
}

// logoutIncomplete states the two facts a failed prune has to state together:
// the sign-out worked, and the profiles it derived are still there. made, when
// non-empty, names what the run managed before it stopped.
func logoutIncomplete(made string) string {
	if made == "" {
		return "you are signed out, but formae could not finish removing the profiles it derived"
	}
	return fmt.Sprintf("you are signed out, but formae could not finish removing the profiles it derived (%s)", made)
}
