// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package login implements the generic `formae login` and `formae logout`
// commands. Both are driven through the active profile's auth plugin, which
// is discovered and started the same way any other authenticated command
// starts one — see internal/cli/app.App.AuthClient.
package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/authmsg"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/logging"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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

// credentialProvider is the subset of *pkgauth.Client that yields the
// credential a completed sign-in produced. It is a second narrow interface
// rather than a widening of authClient because it is a second concern:
// authClient drives the flow, this one reads its result.
type credentialProvider interface {
	GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error)
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
	var device, hosted bool
	var cloud, cloudIssuer string

	command := &cobra.Command{
		Use:   "login",
		Short: "Sign in through the active profile's auth plugin",
		Long: `Sign in through the active profile's auth plugin.

The auth plugin decides how the flow works: opening a browser (the default)
or, with --device, printing a code to enter on another device. Running
login again while already signed in is a no-op. To sign in as someone else,
run logout first.

On a hosted profile, a successful sign-in is followed by a profile per
installation your grants cover: profiles are created, brought up to date,
and removed once a grant is gone. Profiles you wrote yourself are never
touched.`,
		Annotations: map[string]string{
			"type":     "Auth",
			"examples": "{{.Name}} {{.Command}}|{{.Name}} {{.Command}} --device",
		},
		SilenceErrors: true,
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			consumer, schema, err := clicmd.ResolveOutput(cmd)
			if err != nil {
				// The output flags decide how a failure is rendered, so a failure
				// to read them cannot be rendered that way.
				return err
			}

			// In machine mode the documents are the output, so the prose goes
			// nowhere: a banner or an ack line interleaved with JSON makes the
			// whole stream unparseable.
			out := io.Writer(os.Stdout)
			var emit emitter
			if consumer == printer.ConsumerMachine {
				emit = machineEmitter(os.Stdout, schema)
				out = io.Discard
			}

			run := func(err error) error {
				if err == nil || consumer != printer.ConsumerMachine {
					return err
				}
				if _, perr := reportLogin(os.Stdout, schema, err); perr != nil {
					return perr
				}
				// Returned so the process still exits non-zero: the envelope says
				// what happened, the status says that something did.
				return err
			}

			// The hosted branch is taken *before* the App is built, and that
			// ordering is the point rather than a detail. AppFromContext resolves
			// the active profile, and resolving it on a machine that has none
			// creates one — a classic localhost default, for a user who may have
			// come here precisely to use the hosted platform. Signing in cannot
			// be reached through a step that decides the question it is asking.
			if hosted {
				return run(runCloudLoginAndSync(cmd.Context(), cloudLogin{
					Cloud:     cloud,
					Issuer:    cloudIssuer,
					Device:    device,
					PluginDir: defaultCloudPluginDir,
					ConfigDir: store.ResolveConfigDir,
					NewClient: newCloudAPI,
					Verifier:  newVerifier(),
					Out:       out,
					// There is no config to read a theme from, and reading one
					// would mean resolving a profile.
					Theme:     theme.New("formae"),
					NewPlugin: newAuthPlugin,
					Emit:      emit,
				}))
			}

			configFile, _ := cmd.Flags().GetString("config")

			a, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return run(err)
			}
			if emit == nil {
				a.PrintBanner()
			}

			client, err := a.AuthClient()
			if err != nil {
				return run(err)
			}

			return run(runLoginAndSync(cmd.Context(), client, syncStep{
				Creds:      client,
				Entry:      syncFromProfile{conn: a.Config.Cli.Connection},
				ConfigDir:  store.ResolveConfigDir,
				NewClient:  newCloudClient,
				Verifier:   newProfileVerifier(),
				Out:        out,
				Theme:      themeFor(a),
				CloudFlag:  cloud,
				IssuerFlag: cloudIssuer,
				Emit:       emit,
			}, device))
		},
	}

	command.Flags().BoolVar(&device, "device", false, "use a device code instead of opening a browser")
	// A distinct flag, and never inferred from --cloud/--cloud-issuer having a
	// value: those also read FORMAE_CLOUD_URL / FORMAE_CLOUD_ISSUER, so arming on
	// value-presence would make a plain `formae login` on a classic profile start
	// signing in to the hosted platform for anyone who has them exported.
	command.Flags().BoolVar(&hosted, "hosted", false,
		"sign in to the hosted platform rather than to the active profile")
	command.Flags().StringVar(&cloud, "cloud", "",
		fmt.Sprintf("control plane base URL (default: $FORMAE_CLOUD_URL or %s)", DefaultCloudURL))
	command.Flags().StringVar(&cloudIssuer, "cloud-issuer", "",
		fmt.Sprintf("control plane issuer URL (default: $FORMAE_CLOUD_ISSUER or %s)", DefaultCloudIssuer))
	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	clicmd.AddConfigFlags(command)
	clicmd.AddOutputFlags(command)

	return command
}

// runLoginAndSync signs in, brings the profiles this formae derived into line
// with the installations the caller's grants cover, and reports the outcome
// through the step's emitter when it has one.
//
// Sign-in and sync are separate steps because a sign-in that completed a flow and
// one that found a session already open are equally successful sign-ins: the sync
// runs after either. Writing it this way rather than inside runLogin is what
// keeps the short-circuit from skipping it — the branch that returns early
// returns success, and success is exactly what the sync follows.
//
// The identity and the sync result both have to survive their own steps to reach
// the completion document, and neither used to: runLogin returned only an error
// and discarded both identity responses, and runSync returned only the exit
// status and discarded the result it had built. That is why driving a sign-in
// was a change to signatures rather than to printing.
func runLoginAndSync(ctx context.Context, c authClient, s syncStep, device bool) error {
	report, err := runLogin(c, s.Out, s.Theme, device, s.Emit)
	if err != nil {
		return err
	}

	result, active, syncErr := runSync(ctx, s)
	if syncErr != nil {
		// Typed, because a consumer has to tell this apart from a sign-in that
		// failed: the user IS authenticated here, and their session is saved, so
		// sending them back through a sign-in is the one response that cannot
		// help. Every message in runSync already says "you are signed in, but";
		// this carries that distinction across the protocol.
		return &SyncIncompleteError{Cause: syncErr}
	}

	if s.Emit == nil {
		return nil
	}
	return s.Emit.complete(completeView{
		Status:      report.Status,
		Subject:     report.Subject,
		SubjectName: report.SubjectName,
		Profiles: profilesView{
			Created: result.named(verbCreated),
			Updated: result.named(verbUpdated),
			Renamed: result.named(verbRenamed),
			Removed: result.named(verbRemoved),
		},
		Active:   active,
		Warnings: result.Warnings,
	})
}

// SyncIncompleteError is a successful sign-in whose profile sync did not finish.
type SyncIncompleteError struct{ Cause error }

func (e *SyncIncompleteError) Error() string { return e.Cause.Error() }
func (e *SyncIncompleteError) Unwrap() error { return e.Cause }

// loginReport is who signed in, and whether a flow ran to do it.
type loginReport struct {
	// Status is statusSignedIn or statusAlreadyAuthenticated. A caller driving
	// setup more than once needs to tell them apart.
	Status      string
	Subject     string
	SubjectName string
}

// The two outcomes of a sign-in, as a consumer sees them.
const (
	statusSignedIn             = "signed_in"
	statusAlreadyAuthenticated = "already_authenticated"
)

// runLogin drives the two-call login flow against c: LoginStart returns
// either an already-authenticated identity (short-circuiting before any
// LoginWait call) or the URL/code to render, then LoginWait blocks until the
// flow completes and returns the signed-in identity. The browser URL and
// device-code lines are instructions ("do this next"), not completions, so
// they print plain — formae has no established styling convention for that
// kind of prose (compare plugin/init.go's plain numbered next-steps); only
// the completion lines (the sign-in acknowledgments) carry an ack marker.
func runLogin(c authClient, out io.Writer, th *theme.Theme, device bool, emit emitter) (loginReport, error) {
	tty := loginIsTerminal(out)

	mode := "browser"
	if device {
		mode = "device"
	}

	startResp, err := c.LoginStart(&pkgauth.LoginStartRequest{Mode: mode})
	if err != nil {
		return loginReport{}, err
	}
	if startResp.ErrorCode != "" || startResp.Error != "" {
		return loginReport{}, authRefusal(startResp.ErrorCode, startResp.Error)
	}

	if startResp.Status == "already_authenticated" {
		printSignedIn(out, tty, th, "already signed in", startResp.SubjectName, startResp.Subject)
		return loginReport{
			Status:      statusAlreadyAuthenticated,
			Subject:     startResp.Subject,
			SubjectName: startResp.SubjectName,
		}, nil
	}

	// What the user has to do next, before the flow is waited on. For a person
	// that is a line of prose; for a program it is the started document, and in
	// both cases it has to be out before this blocks.
	if emit != nil {
		if err := emit.started(startResp); err != nil {
			return loginReport{}, err
		}
	} else if startResp.Method == "device" {
		_, _ = fmt.Fprintf(out, "Visit %s and enter code: %s\n", startResp.VerificationURI, startResp.UserCode)
	} else {
		_, _ = fmt.Fprintf(out, "Open this URL to sign in:\n  %s\n", startResp.BrowserURL)
	}

	waitResp, err := c.LoginWait(&pkgauth.LoginWaitRequest{SessionID: startResp.SessionID})
	if err != nil {
		return loginReport{}, err
	}
	if waitResp.ErrorCode != "" || waitResp.Error != "" {
		return loginReport{}, authRefusal(waitResp.ErrorCode, waitResp.Error)
	}

	printSignedIn(out, tty, th, "signed in", waitResp.SubjectName, waitResp.Subject)
	return loginReport{
		Status:      statusSignedIn,
		Subject:     waitResp.Subject,
		SubjectName: waitResp.SubjectName,
	}, nil
}

// authRefusal keeps the auth plugin's own code alongside the message a person
// reads.
//
// The message alone is not enough for a caller that has to decide what to do
// next: not_logged_in and session_expired mean "sign in again", where
// issuer_unreachable and unsupported do not, and collapsing all four into one
// formatted string — which is what this did — makes them indistinguishable.
func authRefusal(code pkgauth.ErrorCode, fallback string) error {
	return &AuthError{
		Code:    string(code),
		Message: authmsg.DescribeAuthError(code, fallback),
	}
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

// syncStep is everything the sync half of a login needs from the command. It
// is a struct rather than a longer parameter list because the sync's own
// dependencies are already expressed that way (syncDeps), and because two of
// these cannot be resolved until the sync knows it applies: the control-plane
// client needs the origin the platform resolves to, and the config directory
// is not read at all for a profile that cannot sync.
type syncStep struct {
	Creds      credentialProvider
	Entry      syncEntry
	ConfigDir  func() (string, error)
	NewClient  func(origin string) CloudClient
	Verifier   profileVerifier
	Out        io.Writer
	Theme      *theme.Theme
	CloudFlag  string
	IssuerFlag string

	// Emit, when set, writes the machine documents a driven sign-in produces.
	// Out then takes the prose nobody is reading, so the document stream holds
	// documents and nothing else.
	Emit emitter
}

// syncEntry is where a sync's authority comes from: the connection of the
// profile that was signed in to, or the flags of a profile-independent hosted
// sign-in that had no profile to read.
//
// It is a sum rather than a nillable connection beside a nillable block because
// the two are genuinely alternatives, and the shapes a pair of optional fields
// would also permit — both set, neither set — have no meaning. Writing it this
// way is what stops runSync's opening question ("is this hosted?") from being
// answered by a nil check that a cloud sign-in silently fails.
type syncEntry interface {
	// gate decides everything knowable from configuration alone. The credential
	// half is deliberately not here: every path reaches gateCredential, so no
	// entry can skip the one condition that is only knowable once the auth plugin
	// has answered.
	gate(p platform) gateResult

	// applies reports whether a sync is expected at all. A classic profile's
	// sign-in covers no hosted installations, and that is the ordinary case
	// rather than a refusal — it must stay silent, where a gate that fails
	// prints a notice saying why.
	applies() bool

	// sourceAuth is the raw auth block a generated profile is compared against,
	// so the sync can name keys it does not carry forward. It is nil for a
	// synthesised block: that one is ours and has no unknown keys by
	// construction, so there is nothing to warn about.
	sourceAuth() json.RawMessage
}

// syncFromProfile is the entry for a sign-in through a profile.
type syncFromProfile struct{ conn pkgmodel.Connection }

func (s syncFromProfile) gate(p platform) gateResult { return gateProfile(s.conn, p) }

func (s syncFromProfile) applies() bool {
	hosted, ok := s.conn.(*pkgmodel.HostedConnection)
	return ok && hosted != nil
}

func (s syncFromProfile) sourceAuth() json.RawMessage {
	if hosted, ok := s.conn.(*pkgmodel.HostedConnection); ok && hosted != nil {
		return hosted.Auth
	}
	return nil
}

// syncFromFlagsEntry is the entry for a hosted sign-in that had no profile: the
// block was built from the resolved platform, so it is hosted by construction.
type syncFromFlagsEntry struct{ block cliAuthBlock }

func (s syncFromFlagsEntry) gate(p platform) gateResult { return gateSynthesised(s.block, p) }

func (s syncFromFlagsEntry) applies() bool { return true }

func (s syncFromFlagsEntry) sourceAuth() json.RawMessage { return nil }

// syncFromFlags is the constructor the cloud path uses. It exists so the entry's
// field can stay unexported: a caller that could assemble the struct itself
// could pair a synthesised block with any platform.
func syncFromFlags(block cliAuthBlock) syncEntry { return syncFromFlagsEntry{block: block} }

// runSync derives and maintains one profile per installation the caller's
// grants cover, and reports the outcome as this command's exit status.
//
// A sign-in and a sync are separate facts, and every message here keeps them
// apart: the user is signed in whatever the sync did, so nothing this function
// prints may read as a login that failed.
func runSync(ctx context.Context, s syncStep) (syncResult, string, error) {
	tty := loginIsTerminal(s.Out)

	p, err := resolvePlatform(s.CloudFlag, s.IssuerFlag)
	if err != nil {
		// Resolved first, and reported whatever the profile turns out to be:
		// an override the user (or their environment) actually set is worth a
		// word even on a profile that would not have synced anyway.
		notApplicable(s.Out, tty, s.Theme, err.Error())
		return syncResult{}, "", nil
	}

	if !s.Entry.applies() {
		// A classic profile addresses the user's own agent, so its sign-in
		// covers no hosted installations. That is the ordinary case, and the
		// user asked for nothing that did not happen, so it is silent: a
		// notice here would print on the most common login there is.
		return syncResult{}, "", nil
	}

	// The configuration half is decided before the auth plugin is asked for a
	// credential, so a block that would not pass never causes a request to the
	// issuer it names.
	gate := s.Entry.gate(p)
	if !gate.OK {
		notApplicable(s.Out, tty, s.Theme, gate.Reason)
		return syncResult{}, "", nil
	}

	hdr, err := credential(s.Creds)
	if err != nil {
		return syncResult{}, "", fmt.Errorf("%s: %w", syncIncomplete(""), err)
	}

	gate = gateCredential(gate, p, hdr)
	if !gate.OK {
		notApplicable(s.Out, tty, s.Theme, gate.Reason)
		return syncResult{}, "", nil
	}

	dir, err := s.ConfigDir()
	if err != nil {
		return syncResult{}, "", fmt.Errorf("%s: %w", syncIncomplete(""), err)
	}

	st := store.New(dir)

	// The raw auth block is the one the gate just validated. It travels
	// alongside the decoded block so the sync can name the keys a generated
	// profile does not carry; its values are never printed.
	result := syncProfiles(ctx, syncDeps{
		Client:   s.NewClient(p.Origin),
		Store:    st,
		Verifier: s.Verifier,
		Out:      s.Out,
		TTY:      tty,
		Theme:    s.Theme,
	}, p, gate.Bearer, gate.Auth, s.Entry.sourceAuth())

	active := activateFirstIfNone(st, result, s.Out, tty, s.Theme)

	printWarnings(s.Out, tty, s.Theme, result.Warnings)
	return result, active, syncExit(result)
}

// activateFirstIfNone points the active profile at one this run published, but
// only when the store has no active profile at all.
//
// A machine that has just signed in for the first time has profiles and no
// pointer, and nothing else in this package writes one — the sync reads the
// active profile only to protect it from rename and prune. Left without one, the
// next formae command bootstraps a classic localhost default beside the hosted
// profile that was just created, which is the outcome the whole hosted sign-in
// path exists to avoid.
//
// An existing pointer is never moved. A user with profiles already has an answer
// to "which one", and signing in is not a request to change it; the rename path
// refuses to touch the active profile for the same reason, and reaching around
// that here would be the same mistake one level up.
//
// It runs after publication, and that ordering matters: store.Use runs the
// store's initialization, which on a store with no profiles at all bootstraps
// the very default this avoids. With a published profile present, initialization
// stops at "orphaned profiles, no default" and only the pointer is written.
//
// Failing to write the pointer is a warning, never a failed sign-in. The user is
// signed in and their profiles exist either way, which is the rule every message
// in this file follows.
// It returns the active profile a caller's next request would use, which is
// whatever the pointer names when this is done — the one it just wrote, the one
// that was already there, or empty when there is none.
func activateFirstIfNone(st *store.Store, result syncResult, out io.Writer, tty bool, th *theme.Theme) string {
	if existing, err := st.Active(); err == nil {
		return existing
	}
	published := result.published()
	if len(published) == 0 {
		return "" // nothing to point at, and no pointer is better than a dangling one.
	}

	name := published[0]
	if err := st.Use(name); err != nil {
		ackLine(out, tty, th, components.AckSkip, fmt.Sprintf(
			"profile %s was created but could not be made the active one (%v); "+
				"run `formae profile use %s` to select it", name, err, name))
		return ""
	}
	ackLine(out, tty, th, components.AckDone, "made profile "+name+" active")
	return name
}

// credential returns the header carrying the credential the sign-in produced,
// or the reason there is none to send.
//
// The refresh is not forced: the sign-in this follows has just produced a
// credential, and forcing one would spend a round trip to replace something
// already fresh. A plugin that reports success while returning nothing the
// CLI can transmit fails here rather than being carried forward as an
// unauthenticated request — the same fail-closed reading of a header the API
// path takes, and for the same reason. Only the canonical Authorization key
// is read, because it is the only key this CLI ever sends.
func credential(c credentialProvider) (http.Header, error) {
	resp, err := c.GetAuthHeader(false)
	if err != nil {
		return nil, fmt.Errorf("ask the auth plugin for the credential this sign-in produced: %w", err)
	}
	if resp == nil {
		return nil, errors.New(noCredentialMessage)
	}
	if resp.ErrorCode != "" || resp.Error != "" {
		return nil, errors.New(authmsg.DescribeAuthError(resp.ErrorCode, resp.Error))
	}
	hdr := http.Header(resp.Headers)
	if hdr.Get("Authorization") == "" {
		return nil, errors.New(noCredentialMessage)
	}
	return hdr, nil
}

// noCredentialMessage is reported when the auth plugin says the sign-in
// worked but hands back nothing the CLI could send.
const noCredentialMessage = "the auth plugin returned no credential"

// syncExit maps a completed sync onto the command's exit status.
//
// The rules overlap — an all-skipped run is both "records were skipped" and
// "nothing in the desired set was satisfied", and a snapshot too incomplete to
// license a removal can also have published nothing — so they are evaluated in
// a fixed order and the first match wins. The order is written out here rather
// than left to emerge from where the conditions happen to sit:
//
//  1. the sign-in failed — the caller returns before the sync runs;
//  2. the sync did not complete: no credential, a failed enumeration, a lock
//     held elsewhere, a ledger that could not be written, or a filesystem
//     error while acting on a profile;
//  3. the desired set is non-empty and no installation in it ended the run
//     with a profile this formae owns;
//  4. the snapshot was not authoritative — a warning, and a zero exit;
//  5. individual records were skipped — a warning each, and a zero exit;
//  6. the sync did not apply — a notice, and a zero exit;
//  7. otherwise zero.
//
// Rows 4 to 7 are all zero exits whose reporting has already happened by the
// time this is reached, which is why they collapse into one branch here.
func syncExit(r syncResult) error {
	switch {
	case r.Fatal != nil:
		return syncIncompleteError(r)

	case r.DesiredCount > 0 && r.DesiredSatisfied == 0:
		// A profile kept for an unrelated reason does not satisfy a grant this
		// run covers, so the count is of what this run published or verified.
		return fmt.Errorf(
			"you are signed in, but formae wrote no profile for any of the %s your grants cover",
			quantity(r.DesiredCount, "installation"))

	default:
		return nil
	}
}

// syncIncompleteError phrases a sync that did not complete. A single record
// this formae could not read is enough to reach it, so the changes the run did
// make are named alongside the failure rather than left to read as though
// nothing happened.
func syncIncompleteError(r syncResult) error {
	msg := syncIncomplete(changesMade(r))
	if errors.Is(r.Fatal, errLedgerLocked) {
		// The lock's path is not something the user can act on; the other
		// process is. The failure stays in the chain all the same, so a caller
		// can still tell a contended ledger from a run that really failed; the
		// zero precision is what keeps its text out of the message.
		return fmt.Errorf(
			"%s: another formae process is updating them, so run formae login again when it has finished%.0w",
			msg, r.Fatal)
	}
	return fmt.Errorf("%s: %w", msg, r.Fatal)
}

// syncIncomplete states the two facts a failed sync has to state together:
// the sign-in worked, and the profiles were not brought up to date. made, when
// non-empty, names what the run managed before it stopped.
func syncIncomplete(made string) string {
	if made == "" {
		return "you are signed in, but formae could not finish updating your hosted profiles"
	}
	return fmt.Sprintf("you are signed in, but formae could not finish updating your hosted profiles (%s)", made)
}

// changesMade summarises what a run changed, for a message that reports a
// failure without denying the work that preceded it.
func changesMade(r syncResult) string {
	counts := []struct {
		verb string
		n    int
	}{
		{"created", r.Created},
		{"updated", r.Updated},
		{"renamed", r.Renamed},
		{"removed", r.Pruned},
	}
	var parts []string
	for _, c := range counts {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%s %s", c.verb, quantity(c.n, "profile")))
		}
	}
	return strings.Join(parts, ", ")
}

// quantity renders a count with its noun, singular or plural.
func quantity(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// notApplicable reports that no profiles were synced, and why. It carries the
// no-op marker rather than the warning one because nothing was left in a state
// the user has to repair: the sign-in succeeded and the filesystem is
// untouched.
func notApplicable(w io.Writer, tty bool, th *theme.Theme, reason string) {
	ackLine(w, tty, th, components.AckSkip, "no hosted profiles were synced: "+reason)
}
