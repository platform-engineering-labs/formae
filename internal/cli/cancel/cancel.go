// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cancel

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/errfmt"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/statuswatch"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/logging"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// Package-level seams — replaced in tests to avoid TTY / network calls.
var (
	isInteractive = tui.IsInteractive
	runConfirm    = components.RunConfirm
)

// isTerminal returns true when the writer is a real terminal (includes Cygwin).
var isTerminal = tui.IsTerminal

// getCommandsStatusFn is a seam so tests can stub the pre-fetch call.
var getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
	return a.GetCommandsStatus(query, n, fromWatch)
}

// cancelCommandFn is a seam so tests can stub the cancel call.
var cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
	return a.CancelCommand(query, force)
}

// launchCancelWatch is a seam so tests can stub the statuswatch TUI launch after cancel.
var launchCancelWatch = func(a *app.App, th *theme.Theme, opts statuswatch.Options) error {
	model := statuswatch.New(th, a, opts)
	_, err := tui.Run(model, tui.DefaultRunOptions())
	return err
}

// themeFor resolves the active theme from the app config.
func themeFor(a *app.App) *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

type CancelOptions struct {
	Query          string
	Force          bool
	Yes            bool
	Watch          bool
	StatusOutput   status.StatusOutput
	OutputConsumer printer.Consumer
	OutputSchema   string
}

func CancelCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel in-progress commands",
		Long: `Cancel commands that are currently in progress.

If no query is provided, cancels the most recent command.
If a query is provided, cancels all in-progress commands matching the query.

Note: Only commands in 'InProgress' state can be canceled.
Commands that are already executing resources will complete those resources
before transitioning to 'Canceled' state to avoid orphaned resources.

Use --force to abandon in-progress work and drive the command to a terminal
'Canceled' state immediately, instead of waiting for in-progress resources to
finish. This is an escape hatch for operations that will not complete (e.g. a
plugin stuck in an unbounded poll loop). With --force:
  - Cloud-side operations may keep running after the command is canceled.
  - Update/Delete operations are self-healing: the synchronizer reconciles
    formae's state against actual cloud state on its next cycle.
  - A still-running Create may orphan a cloud resource that formae cannot track
    (it has no native id yet). You may need to clean it up manually, or let
    discovery pick it up.`,
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(command *cobra.Command, args []string) error {
			opts := &CancelOptions{}
			query, _ := command.Flags().GetString("query")
			opts.Query = strings.TrimSpace(query)
			opts.Force, _ = command.Flags().GetBool("force")
			opts.Yes, _ = command.Flags().GetBool("yes")
			opts.Watch, _ = command.Flags().GetBool("watch")
			statusOutput, _ := command.Flags().GetString("status-output-layout")
			opts.StatusOutput = status.StatusOutput(statusOutput)
			outputConsumer, _ := command.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(outputConsumer)
			opts.OutputSchema, _ = command.Flags().GetString("output-schema")

			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			return runCancel(app, opts)
		},
		Annotations: map[string]string{
			"type": "Command",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("query", "", "Query to select commands to cancel. If not provided, cancels the most recent command. Use * as a wildcard anywhere (e.g. foo*, *foo, *foo*, foo*bar). ? and regex are not yet supported.")
	command.Flags().Bool("force", false, "Abandon in-progress work and drive the command to a terminal 'Canceled' state immediately, instead of waiting for in-progress resources to finish. Cloud-side operations may continue: Update/Delete are reconciled by the synchronizer, but a still-running Create may orphan a resource that needs manual cleanup.")
	command.Flags().Bool("yes", false, "Allow the command to run without any confirmations")
	command.Flags().BoolP("watch", "w", false, "Watch the status of canceled commands until they complete")
	command.Flags().String("status-output-layout", string(status.StatusOutputSummary), fmt.Sprintf("What to print as status output (%s | %s)", status.StatusOutputSummary, status.StatusOutputDetailed))
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "yaml", "The schema to use for the result output (json | yaml)")
	cmd.AddConfigFlags(command)

	return command
}

func Validate(opts *CancelOptions) error {
	if opts.OutputConsumer != printer.ConsumerHuman && opts.OutputConsumer != printer.ConsumerMachine {
		return cmd.FlagErrorf("output consumer must be either 'human' or 'machine'")
	}
	if opts.OutputConsumer == printer.ConsumerMachine {
		if opts.OutputSchema != "json" && opts.OutputSchema != "yaml" {
			return cmd.FlagErrorf("output schema must be either 'json' or 'yaml' for machine consumer")
		}
	}

	return nil
}

func runCancel(app *app.App, opts *CancelOptions) error {
	err := Validate(opts)
	if err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runCancelForHumans(app, opts)
	}
	return runCancelForMachines(app, opts)
}

func runCancelForHumans(a *app.App, opts *CancelOptions) error {
	a.PrintBanner()

	// TTY: the styled D6 frozen-set flow (--yes only skips the force confirm).
	// Non-TTY: the legacy flow, unchanged.
	if isTerminal(os.Stdout) {
		return runCancelInteractive(a, opts)
	}
	return runCancelLegacy(a, opts)
}

// runCancelInteractive implements the styled TTY cancel flow. The commands to
// cancel are frozen at pre-fetch time (D6): the user cancels exactly the
// commands they were shown, never a re-evaluated query.
func runCancelInteractive(a *app.App, opts *CancelOptions) error {
	th := themeFor(a)
	now := time.Now()

	// Step 1: Pre-fetch non-terminal commands matching the query (D6 frozen set).
	preFetch, _, err := getCommandsStatusFn(a, opts.Query, 50, false)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	// Filter to non-terminal commands only.
	var activeCmds []apimodel.Command
	if preFetch != nil {
		for _, c := range preFetch.Commands {
			if !isTerminalState(c.State) {
				activeCmds = append(activeCmds, c)
			}
		}
	}

	if len(activeCmds) == 0 {
		fmt.Println("No commands to cancel.")
		return nil
	}

	// Step 2: --force && !--yes → show warning panel and ask for confirmation.
	if opts.Force && !opts.Yes {
		// Build the pre-consent summary for the confirmation panel. Each command shows
		// a header line (ID + command + mode) followed by an expectation bullet derived
		// from its pre-fetched ResourceUpdates (per mockup VIEW 2b).
		thForSummary := themeFor(a)
		var summaryLines []string
		for _, c := range activeCmds {
			summaryLines = append(summaryLines, fmt.Sprintf("  %s  %s %s", c.CommandID, c.Command, c.Mode))
			counts := bucketPreCancel(c)
			if line := renderPreCancelExpectationLine(thForSummary, counts); line != "" {
				summaryLines = append(summaryLines, line)
			}
		}
		summary := strings.Join(summaryLines, "\n")

		ok, confirmErr := confirmForceCancel(th, summary)
		if confirmErr != nil {
			// User aborted (ctrl+c / esc) — treat as decline
			return nil
		}
		if !ok {
			return nil
		}
	}

	// Step 3: D6 frozen set — submit one cancel per pre-fetched command ID.
	merged := &apimodel.CancelCommandResponse{
		Forced:               opts.Force,
		ResourceUpdateStates: make(map[string]apimodel.CancelResourceState),
	}
	for _, c := range activeCmds {
		idQuery := fmt.Sprintf("id:%s", c.CommandID)
		res, cancelErr := cancelCommandFn(a, idQuery, opts.Force)
		if cancelErr != nil {
			msg, renderErr := errfmt.Render(cancelErr)
			if renderErr != nil {
				return fmt.Errorf("error rendering error message: %v", renderErr)
			}
			return fmt.Errorf("%s", msg)
		}
		if res == nil || len(res.CommandIDs) == 0 {
			// Command finished between pre-fetch and cancel — skip silently.
			continue
		}
		// Merge CommandIDs (union).
		merged.CommandIDs = append(merged.CommandIDs, res.CommandIDs...)
		// Merge ResourceUpdateStates.
		for k, v := range res.ResourceUpdateStates {
			merged.ResourceUpdateStates[k] = v
		}
	}

	if len(merged.CommandIDs) == 0 {
		fmt.Println("No commands to cancel.")
		return nil
	}

	// Compute expectations from merged response.
	exps := cancelExpectations(merged)

	// Step 4/5: --watch vs. no-watch.
	if opts.Watch {
		fmt.Println(renderCancelSummary(th, activeCmds, exps, opts.Force, now))

		// Build the set of force-abandoned resource ksuids (P3 normalization at call site).
		var abandonedKsuids []string
		for uri, rs := range merged.ResourceUpdateStates {
			if rs.ForceCanceled {
				abandonedKsuids = append(abandonedKsuids, ksuidFromURI(uri))
			}
		}
		sort.Strings(abandonedKsuids)

		if len(merged.CommandIDs) == 1 {
			return launchCancelWatch(a, th, statuswatch.Options{
				Query:              fmt.Sprintf("id:%s", merged.CommandIDs[0]),
				FocusCommandID:     merged.CommandIDs[0],
				ExitWhenDone:       true,
				AbandonedResources: abandonedKsuids,
			})
		}
		idTerms := make([]string, len(merged.CommandIDs))
		for i, id := range merged.CommandIDs {
			idTerms[i] = "id:" + id
		}
		return launchCancelWatch(a, th, statuswatch.Options{
			Query:              strings.Join(idTerms, " "),
			ExitWhenDone:       true,
			AbandonedResources: abandonedKsuids,
		})
	}

	// No --watch: print styled summary.
	fmt.Print(renderCancelSummary(th, activeCmds, exps, opts.Force, now))

	// Force output: list force-canceled resources with warning.
	if opts.Force {
		renderForceCanceledResources(th, merged, activeCmds)
	}

	return nil
}

// isTerminalState returns true for states that can no longer be canceled.
func isTerminalState(state string) bool {
	switch state {
	case "Success", "Failed", "Canceled":
		return true
	}
	return false
}

// renderForceCanceledResources prints the list of resources that were abandoned
// due to --force, followed by the abandoned/orphan reminder lines (the copy
// from the legacy RenderCancelCommandResponse, restyled via theme roles).
func renderForceCanceledResources(th *theme.Theme, merged *apimodel.CancelCommandResponse, cmds []apimodel.Command) {
	warnStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning)
	subtle := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary)

	// Resolve response keys (FormaeURIs) to resource labels via the pre-fetched
	// commands: the URI's ksuid matches ResourceUpdate.ResourceID.
	labels := make(map[string]string)
	for _, c := range cmds {
		for _, ru := range c.ResourceUpdates {
			if ru.ResourceLabel != "" {
				labels[ru.ResourceID] = fmt.Sprintf("%s (%s)", ru.ResourceLabel, ru.ResourceType)
			}
		}
	}

	var abandoned []string
	for uri, rs := range merged.ResourceUpdateStates {
		if !rs.ForceCanceled {
			continue
		}
		ksuid := ksuidFromURI(uri)
		if label, ok := labels[ksuid]; ok {
			abandoned = append(abandoned, label)
		} else {
			abandoned = append(abandoned, ksuid)
		}
	}
	sort.Strings(abandoned)

	if len(abandoned) > 0 {
		fmt.Println()
		fmt.Printf("  %s\n", warnStyle.Render("The following resources were abandoned mid-operation and may exist in your cloud provider:"))
		for _, res := range abandoned {
			fmt.Printf("    %s %s\n", warnStyle.Render("⚠"), res)
		}
	}

	fmt.Println()
	fmt.Printf("  %s\n", subtle.Render("Force-cancel abandons in-progress work; cloud-side operations may still be running."))
	fmt.Printf("  %s\n", subtle.Render("Update/Delete operations are reconciled by the synchronizer on its next cycle."))
	fmt.Printf("  %s\n", subtle.Render("A still-running Create may orphan a resource: verify the resources above in your"))
	fmt.Printf("  %s\n", subtle.Render("cloud provider and clean up manually, or let discovery pick them up."))
}

// runCancelLegacy is the pre-TUI human flow, used on non-TTY output. It
// submits the user's query as-is and prints via the human-readable printer.
func runCancelLegacy(a *app.App, opts *CancelOptions) error {
	// A plain cancel is safe (it waits for in-progress resources to finish). A
	// --force cancel is a destructive escape hatch: it abandons in-progress work,
	// can leave cloud-side operations running, and may orphan resources. Confirm
	// before proceeding unless --yes was given.
	if opts.Force && !opts.Yes {
		target := "the most recent in-progress command"
		if opts.Query != "" {
			target = fmt.Sprintf("all in-progress commands matching %q", opts.Query)
		}
		prompt := fmt.Sprintf(
			"%s\n\n"+
				"This abandons in-progress work and drives %s straight to 'Canceled' "+
				"without waiting for running resources to finish.\n\n"+
				"  • Cloud-side operations already in flight may keep running.\n"+
				"  • A still-running Create can orphan a resource formae cannot track — manual cleanup may be needed.\n"+
				"  • Update/Delete operations are reconciled by the synchronizer on its next cycle.\n\n"+
				"Only use this for operations that will not complete on their own.\n\n"+
				"Force-cancel anyway?",
			lipgloss.NewStyle().Foreground(themeFor(a).Palette.Warning).Render("Warning: --force is a destructive escape hatch."),
			target,
		)
		if !isInteractive() {
			return fmt.Errorf("interactive input requires a TTY — pass --yes")
		}
		ok, confirmErr := runConfirm(themeFor(a), prompt, "")
		if confirmErr != nil {
			return confirmErr
		}
		if !ok {
			fmt.Print(lipgloss.NewStyle().Foreground(themeFor(a).Palette.Error).Render("\nCommand aborted") + "\n")
			return nil
		}
	}

	res, err := cancelCommandFn(a, opts.Query, opts.Force)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	_, _ = fmt.Print(renderCancelResult(themeFor(a), res, cancelTermWidth(os.Stdout)))

	// If no commands were canceled, nothing to watch
	if res == nil || len(res.CommandIDs) == 0 {
		return nil
	}

	if opts.Watch {
		fmt.Println() // Add spacing before watch output

		// For single command, watch by ID
		if len(res.CommandIDs) == 1 {
			query := fmt.Sprintf("id:%s", res.CommandIDs[0])
			return status.WatchCommandsStatus(a, query, 1, opts.StatusOutput)
		}

		// For multiple commands, watch without filter to see all recent commands
		// (which will include all the canceling/canceled commands)
		return status.WatchCommandsStatus(a, "", len(res.CommandIDs), opts.StatusOutput)
	}

	return nil
}

func runCancelForMachines(app *app.App, opts *CancelOptions) error {
	res, err := app.CancelCommand(opts.Query, opts.Force)
	if err != nil {
		return fmt.Errorf("error canceling commands: %v", err)
	}

	printer := printer.NewMachineReadablePrinter[apimodel.CancelCommandResponse](os.Stdout, opts.OutputSchema)

	return printer.Print(res)
}
