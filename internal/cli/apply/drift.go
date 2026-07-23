// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package apply

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/nag"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/driftview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/errfmt"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/simview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/schema"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// launchDriftView is a package-level var so tests can stub it.
var launchDriftView = func(th *theme.Theme, rejected *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
	model := driftview.New(th, rejected, opts)
	final, err := tui.Run(model, tui.DefaultRunOptions())
	if err != nil {
		return driftview.DecisionAbort{}, err
	}
	return final.(driftview.Model).Decision(), nil
}

// confirmOverwriteFn is seamed so tests can bypass the interactive prompt.
var confirmOverwriteFn = func(th *theme.Theme, path string) (bool, error) {
	return components.RunConfirm(th, fmt.Sprintf("File '%s' already exists. Overwrite?", path), "")
}

// forcedApplyFn submits a real apply with force=true, ignoring opts.Force.
var forcedApplyFn = func(a *app.App, opts *ApplyOptions) (*apimodel.SubmitCommandResponse, []string, error) {
	return a.Apply(opts.FormaFile, opts.Properties, opts.Mode, false, true)
}

// extractResourcesFn is seamed so tests and E2E can stub or intercept it.
var extractResourcesFn = func(a *app.App, query string) (*pkgmodel.Forma, []string, error) {
	return a.ExtractResources(query, false)
}

// generateSourceCodeFn is seamed so tests can bypass file generation.
var generateSourceCodeFn = func(a *app.App, forma *pkgmodel.Forma, path string) error {
	_, err := a.GenerateSourceCode(forma, path, "pkl", schema.SchemaLocationRemote)
	return err
}

// runDriftFlow drives the reconcile-rejected loop; returns nil when handled
// (extracted or self-resolved) and submits+watches on a validated revert.
func runDriftFlow(a *app.App, th *theme.Theme, opts *ApplyOptions, rejected apimodel.FormaReconcileRejectedError) error {
	driftOpts := driftview.Options{SimulateOnly: opts.Simulate, FormaFile: opts.FormaFile}
	for {
		decision, err := launchDriftView(th, &rejected, driftOpts)
		if err != nil {
			return err
		}

		switch d := decision.(type) {
		case driftview.DecisionAbort:
			// The user already reviewed the drift in the TUI — don't dump the
			// verbose rejection to the scrollback on the way out. A concise
			// acknowledgement (matching the simview abort) is enough.
			fmt.Print(lipgloss.NewStyle().Foreground(th.Palette.TextSubtle).Render("Apply aborted.") + "\n")
			return nil

		case driftview.DecisionExtract:
			return handleExtract(a, th, d, opts.FormaFile)

		case driftview.DecisionRevertAll:
			// Hard guard: under --simulate, never perform a real cloud mutation.
			// The UI already hides the revert action (SimulateOnly on driftOpts),
			// but defend in depth here in case the model is bypassed.
			if opts.Simulate {
				fmt.Print(lipgloss.NewStyle().Foreground(a.Theme().Palette.TextSubtle).Render("Command will not continue — simulation only") + "\n")
				return nil
			}
			newRes, _, simErr := applyFn(a, opts, true)
			if simErr != nil {
				if reconcileErr, ok := simErr.(*apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]); ok {
					newRejected := reconcileErr.Data
					if sameDrift(rejected, newRejected) {
						return submitForcedApply(a, th, opts)
					}
					rejected = newRejected
					driftOpts = driftview.Options{
						Notice:       "Drift changed while you were reviewing — re-confirm against the current changes.",
						SimulateOnly: opts.Simulate,
						FormaFile:    opts.FormaFile,
					}
					continue
				}
				msg, renderErr := errfmt.Render(simErr)
				if renderErr != nil {
					return fmt.Errorf("error rendering error message: %v", renderErr)
				}
				return fmt.Errorf("%s", msg)
			}
			fmt.Println("Out-of-band changes were absorbed or reverted since rejection — continuing with a normal apply.")
			return handleSelfResolvedDrift(a, th, opts, newRes)
		}
	}
}

// handleExtract implements the Extract decision: extract each selected resource,
// merge Formas, generate source code, confirm overwrite if needed, and print the
// next-steps guidance. formaFile is the user's original forma, named in the
// re-apply command.
func handleExtract(a *app.App, th *theme.Theme, d driftview.DecisionExtract, formaFile string) error {
	merged := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{},
		Targets:   []pkgmodel.Target{},
		Resources: []pkgmodel.Resource{},
	}
	seenStacks := map[string]bool{}
	seenTargets := map[string]bool{}

	for _, ref := range d.Selected {
		query := fmt.Sprintf("stack:%s type:%s label:%s", ref.Stack, ref.Type, ref.Label)
		forma, _, err := extractResourcesFn(a, query)
		if err != nil {
			return fmt.Errorf("error extracting resource %s/%s/%s: %v", ref.Stack, ref.Type, ref.Label, err)
		}
		if forma == nil {
			continue
		}
		merged.Resources = append(merged.Resources, forma.Resources...)
		// Carry over stacks and targets. The PKL generator requires the Stacks
		// property to be present; omitting it makes serialization fail with
		// "Cannot find property `Stacks`".
		for _, s := range forma.Stacks {
			if !seenStacks[s.Label] {
				seenStacks[s.Label] = true
				merged.Stacks = append(merged.Stacks, s)
			}
		}
		for _, t := range forma.Targets {
			if !seenTargets[t.Label] {
				seenTargets[t.Label] = true
				merged.Targets = append(merged.Targets, t)
			}
		}
	}

	if len(merged.Resources) == 0 {
		fmt.Println("No resources extracted.")
		return nil
	}

	if _, err := os.Stat(d.Path); err == nil {
		ok, promptErr := confirmOverwriteFn(th, d.Path)
		if promptErr != nil {
			fmt.Println("Extract cancelled.")
			return nil
		}
		if !ok {
			fmt.Println("Extract cancelled — file not overwritten. Re-run to choose a different path.")
			return nil
		}
	}

	if err := generateSourceCodeFn(a, merged, d.Path); err != nil {
		return fmt.Errorf("error generating source code: %v", err)
	}

	for _, ln := range buildExtractGuidance(len(merged.Resources), d.Path, formaFile) {
		fmt.Println(ln)
	}

	return nil
}

// buildExtractGuidance returns the plain post-extract next-steps lines. The
// extracted file only captures the selected resources' current state; applying
// it directly would reconcile a partial stack and destroy everything else.
// The correct workflow is to fold the wanted values into the original forma and
// re-apply the whole stack with --force — which absorbs the kept changes as
// no-ops and overwrites the rest back to the code.
func buildExtractGuidance(count int, path, formaFile string) []string {
	if formaFile == "" {
		formaFile = "your forma"
	}
	return []string{
		fmt.Sprintf("Extracted %d resource(s) to %s", count, path),
		"Fold the values you want to keep into your forma, then re-apply with --force:",
		fmt.Sprintf("  formae apply --mode reconcile --force %s", formaFile),
	}
}

// submitForcedApply submits a force apply after re-validated identical drift.
func submitForcedApply(a *app.App, th *theme.Theme, opts *ApplyOptions) error {
	realRes, nags, err := forcedApplyFn(a, opts)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	fmt.Printf("Force apply submitted — command %s\n", realRes.CommandID)
	if err := launchWatch(a, realRes.CommandID); err != nil {
		return err
	}
	fmt.Printf("\nRun the following command to check status:\n\n  formae status command --query='id:%s' --watch\n", realRes.CommandID)
	nag.MaybePrintNags(th, nags)

	return nil
}

// handleSelfResolvedDrift falls through to the normal simview preview flow
// when a re-simulate after RevertAll succeeds (drift self-resolved).
func handleSelfResolvedDrift(a *app.App, th *theme.Theme, opts *ApplyOptions, res *apimodel.SubmitCommandResponse) error {
	if res == nil || !res.Simulation.ChangesRequired {
		panel := components.Panel(th, th.Palette.Border, "formae apply", []string{
			"No changes needed",
			"",
			"The specified forma resources are up to date.",
		}, 80)
		fmt.Println(panel)
		return nil
	}

	decision, err := launchSimView(th, &res.Simulation, simview.Options{
		Kind:         simview.KindApply,
		Mode:         string(opts.Mode),
		Source:       opts.FormaFile,
		SimulateOnly: opts.Simulate,
		Description:  res.Description,
	})
	if err != nil {
		return err
	}

	if opts.Simulate {
		return nil
	}

	if decision == simview.DecisionAborted {
		fmt.Print(lipgloss.NewStyle().Foreground(a.Theme().Palette.TextSubtle).Render("Apply aborted.") + "\n")
		return nil
	}

	realRes, nags, err := applyFn(a, opts, false)
	if err != nil {
		msg, renderErr := errfmt.Render(err)
		if renderErr != nil {
			return fmt.Errorf("error rendering error message: %v", renderErr)
		}
		return fmt.Errorf("%s", msg)
	}

	raw := components.PromptForOperations(&res.Simulation.Command)
	summary := ""
	if raw != "" {
		stripped := ansiEscape.ReplaceAllString(raw, "")
		summary = strings.SplitN(stripped, "\n\n", 2)[0]
		summary = strings.ReplaceAll(summary, "\n", " ")
	}
	fmt.Printf("Confirmed: %s — command %s submitted\n", summary, realRes.CommandID)

	if err := launchWatch(a, realRes.CommandID); err != nil {
		return err
	}

	fmt.Printf("\nRun the following command to check status:\n\n  formae status command --query='id:%s' --watch\n", realRes.CommandID)
	nag.MaybePrintNags(th, nags)

	return nil
}

// sameDrift reports whether two reconcile-rejected payloads describe exactly
// the same drift. Per stack, the set of (Type, Label, Operation,
// string(PatchDocument)) tuples must be equal.
func sameDrift(a, b apimodel.FormaReconcileRejectedError) bool {
	if len(a.ModifiedStacks) != len(b.ModifiedStacks) {
		return false
	}
	for stackName, stackA := range a.ModifiedStacks {
		stackB, ok := b.ModifiedStacks[stackName]
		if !ok {
			return false
		}
		if !sameModifications(stackA.ModifiedResources, stackB.ModifiedResources) {
			return false
		}
	}
	return true
}

type driftTuple struct{ typ, label, op, patch string }

func sameModifications(a, b []apimodel.ResourceModification) bool {
	if len(a) != len(b) {
		return false
	}
	setA := make(map[driftTuple]bool, len(a))
	for _, m := range a {
		setA[driftTuple{m.Type, m.Label, m.Operation, string(m.PatchDocument)}] = true
	}
	for _, m := range b {
		if !setA[driftTuple{m.Type, m.Label, m.Operation, string(m.PatchDocument)}] {
			return false
		}
	}
	return true
}
