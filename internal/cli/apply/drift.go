// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package apply

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/huh"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/errfmt"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/simview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

var errResolutionAborted = errors.New("drift resolution aborted")
var chooseDrift = defaultChooseDrift
var desiredDeltaFn = func(a *app.App, id string) (*apimodel.CommandDesiredDelta, error) {
	return a.ExtractCommandDesiredDelta(id)
}

type driftChoice struct {
	modification apimodel.ResourceModification
	action       string
}

// Each radio selector starts on an invalid placeholder. Neither an untouched
// selector nor quitting the form can authorize a decision.
var runDecisionForm = func(th *theme.Theme, items []driftChoice) error {
	groups := make([]*huh.Group, 0, len(items))
	for i := range items {
		item := &items[i]
		mod := item.modification
		lines, err := components.RenderChangeLinesFromPatch(th, mod.PatchDocument, mod.Properties, mod.OldProperties, nil)
		if err != nil {
			return err
		}
		description := fmt.Sprintf("%s drift. All changed fields on this resource are resolved together.\n%s", mod.Operation, strings.Join(lines, "\n"))
		description = "Origin: " + driftOrigin(mod) + "\n" + description
		groups = append(groups, huh.NewGroup(huh.NewSelect[string]().Title(fmt.Sprintf("%s / %s / %s", driftDisplayIdentity(mod.Stack), driftDisplayIdentity(mod.Type), driftDisplayIdentity(mod.Label))).Description(description).Options(huh.NewOption("Choose a resolution…", ""), huh.NewOption("Absorb: keep the observed state as desired intent", "absorb"), huh.NewOption("Revert: restore the previous desired intent", "revert")).Value(&item.action).Validate(func(value string) error {
			if value != "absorb" && value != "revert" {
				return fmt.Errorf("choose absorb or revert")
			}
			return nil
		})))
	}
	return components.NewThemedForm(th, groups...).Run()
}

func driftItems(rejected apimodel.FormaReconcileRejectedError) []driftChoice {
	var items []driftChoice
	for stack, group := range rejected.ModifiedStacks {
		for _, mod := range group.ModifiedResources {
			if mod.Operation == "create" || mod.Operation == "update" || mod.Operation == "delete" {
				mod.Stack = stack
				items = append(items, driftChoice{modification: mod})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].modification.ResourceID < items[j].modification.ResourceID })
	return items
}
func validateDecisions(rejected apimodel.FormaReconcileRejectedError, decisions []pkgmodel.DriftDecision) error {
	if rejected.ObservationID == "" {
		return fmt.Errorf("agent did not provide a shared drift observation; upgrade the agent to resolve drift")
	}
	items := driftItems(rejected)
	expected := map[string]bool{}
	for _, item := range items {
		id := item.modification.ResourceID
		if id == "" || expected[id] {
			return fmt.Errorf("drift has missing or duplicate resource identity")
		}
		expected[id] = true
	}
	if len(expected) == 0 || len(decisions) != len(expected) {
		return fmt.Errorf("choose exactly one absorb or revert action for every drifted resource")
	}
	for _, d := range decisions {
		if !expected[d.ResourceID] || (d.Action != "absorb" && d.Action != "revert") {
			return fmt.Errorf("invalid or duplicate drift decision for %q", d.ResourceID)
		}
		delete(expected, d.ResourceID)
	}
	return nil
}
func defaultChooseDrift(th *theme.Theme, rejected apimodel.FormaReconcileRejectedError) ([]pkgmodel.DriftDecision, error) {
	if rejected.ObservationID == "" {
		return nil, fmt.Errorf("agent does not provide shared drift resolution observations; upgrade the agent")
	}
	items := driftItems(rejected)
	if len(items) == 0 {
		return nil, fmt.Errorf("no actionable drift observations")
	}
	if err := runDecisionForm(th, items); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil, errResolutionAborted
		}
		return nil, err
	}
	decisions := make([]pkgmodel.DriftDecision, 0, len(items))
	for _, item := range items {
		decisions = append(decisions, pkgmodel.DriftDecision{ResourceID: item.modification.ResourceID, Action: item.action})
	}
	if err := validateDecisions(rejected, decisions); err != nil {
		return nil, err
	}
	return decisions, nil
}

func resolutionStale(err error) bool {
	var e *apimodel.ErrorResponse[apimodel.DriftResolutionError]
	return errors.As(err, &e) && e.Data.Code == "stale-review"
}
func humanApplyError(err error) error {
	message, renderErr := errfmt.Render(err)
	if renderErr != nil {
		return renderErr
	}
	return errors.New(message)
}

func runRecordedDriftFlow(a *app.App, th *theme.Theme, opts *ApplyOptions, rejected apimodel.FormaReconcileRejectedError) error {
	for {
		decisions, err := chooseDrift(th, rejected)
		if errors.Is(err, errResolutionAborted) {
			fmt.Println("Apply aborted.")
			return nil
		}
		if err != nil {
			return err
		}
		if err := validateDecisions(rejected, decisions); err != nil {
			return err
		}
		opts.Resolution = &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: decisions}
		res, _, err := applyFn(a, opts, true)
		if err == nil {
			err = confirmAndSubmitResolution(a, th, opts, res)
		}
		if !resolutionStale(err) {
			if err != nil {
				return humanApplyError(err)
			}
			return nil
		}
		fmt.Println("Drift or the final plan changed. Review fresh observations and confirm a new plan.")
		opts.Resolution = nil
		fresh, _, freshErr := applyFn(a, opts, true)
		var rejection *apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]
		if errors.As(freshErr, &rejection) {
			rejected = rejection.Data
			continue
		}
		if freshErr != nil {
			return humanApplyError(freshErr)
		}
		return confirmAndSubmitResolution(a, th, opts, fresh)
	}
}

func prepareMessage(opts *ApplyOptions, review *pkgmodel.DriftReview) {
	if opts.MessageExplicit || opts.messageEdited || (opts.Message != "" && opts.Message != opts.suggestedMessage) {
		return
	}
	suggestion := "Apply " + string(opts.Mode) + " changes"
	if review != nil {
		absorb, revert := 0, 0
		stacks := map[string]bool{}
		for _, d := range review.Decisions {
			if d.Action == "absorb" {
				absorb++
			} else if d.Action == "revert" {
				revert++
			}
		}
		for _, o := range review.Observations {
			stacks[o.Stack] = true
		}
		labels := make([]string, 0, len(stacks))
		for s := range stacks {
			labels = append(labels, s)
		}
		sort.Strings(labels)
		suggestion = fmt.Sprintf("Resolve %s drift: absorb %d, revert %d", strings.Join(labels, ", "), absorb, revert)
	}
	opts.Message = suggestion
	opts.suggestedMessage = suggestion
}

func previewOptions(opts *ApplyOptions, res *apimodel.SubmitCommandResponse) simview.Options {
	options := simview.Options{Kind: simview.KindApply, Mode: string(opts.Mode), Source: opts.FormaFile, SimulateOnly: opts.Simulate, Description: res.Description}
	if !opts.Simulate {
		prepareMessage(opts, res.Review)
		if !opts.MessageExplicit {
			options.Message = &opts.Message
		}
	}
	return options
}

func confirmAndSubmitResolution(a *app.App, th *theme.Theme, opts *ApplyOptions, res *apimodel.SubmitCommandResponse) error {
	if opts.Resolution != nil && (res.Review == nil || res.Review.ReviewID == "") {
		return fmt.Errorf("agent returned no final resolution review; command was not submitted")
	}
	if !res.Simulation.ChangesRequired {
		fmt.Println("No changes needed.")
		return nil
	}
	decision, err := launchSimView(th, &res.Simulation, previewOptions(opts, res))
	if err != nil {
		return err
	}
	if opts.Message != opts.suggestedMessage {
		opts.messageEdited = true
	}
	if opts.Simulate {
		return nil
	}
	if decision != simview.DecisionConfirmed {
		fmt.Println("Apply aborted.")
		return nil
	}
	if opts.Resolution != nil {
		opts.Resolution.ReviewID = res.Review.ReviewID
		if opts.Resolution.IdempotencyKey == "" {
			opts.Resolution.IdempotencyKey = util.NewID()
		}
	}
	real, _, err := applyFn(a, opts, false)
	if err != nil {
		if resolutionStale(err) {
			return err
		}
		return humanApplyError(err)
	}
	if real.Simulation.Command.State == "Success" || real.Simulation.Command.State == "Failed" {
		fmt.Printf("Command %s: %s\n", real.CommandID, real.Simulation.Command.State)
	} else {
		finished, err := launchWatch(a, real.CommandID)
		if err != nil {
			return err
		}
		if !finished {
			printAsyncNotice(real.CommandID)
			return nil
		}
	}
	if opts.Resolution != nil {
		printRecordedGuidance(a, real.CommandID)
	}
	return nil
}

// Source catch-up is read-only and pinned to command contributions, never a
// fresh inventory snapshot. Extraction is explicit and never edits user files.
func printRecordedGuidance(a *app.App, commandID string) {
	delta, err := desiredDeltaFn(a, commandID)
	if err != nil {
		fmt.Printf("Command %s is recorded. Source guidance unavailable: %v\n", commandID, err)
		return
	}
	fmt.Printf("Source catch-up for command %s (%s):\n", commandID, delta.State)
	if delta.Forma != nil {
		for _, r := range delta.Forma.Resources {
			fmt.Printf("  Recorded resource: %s\n", resourceQuery(r.Stack, r.Type, r.Label, r.Ksuid))
		}
	}
	for _, r := range delta.DeletedResources {
		fmt.Printf("  Remove this declaration from your code: stack=%q type=%q label=%q\n", r.Stack, r.Type, r.Label)
	}
	fmt.Printf("  formae extract --command %s ./accepted-delta.pkl\n", shellQuote(commandID))
	fmt.Println("This is a partial source-edit snippet. Merge its values and removals into your complete stack declaration; do not apply the snippet as a full reconcile.")
}

// The ordinary resource query translator accepts escaped terms, not quoted
// phrase nodes. Wildcard stars cannot be made literal by that query grammar.
func queryLiteral(value string) string {
	var escaped strings.Builder
	for _, r := range value {
		if strings.ContainsRune(`+-=&|><!(){}[]^"~*?:\/ `, r) {
			escaped.WriteRune('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}
func resourceQuery(stack, typ, label, id string) string {
	unsafe := false
	for _, r := range stack + typ + label {
		if r == '*' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			unsafe = true
		}
	}
	if unsafe {
		return fmt.Sprintf("resource=%q stack=%q type=%q label=%q (exact query unavailable)", id, stack, typ, label)
	}

	return "stack:" + queryLiteral(stack) + " type:" + queryLiteral(typ) + " label:" + queryLiteral(label)
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func driftOrigin(mod apimodel.ResourceModification) string {
	if mod.ObservedSource == "synchronizer" {
		return "synchronizer"
	}
	if mod.ObservedCommand == pkgmodel.CommandApply && mod.ObservedMode == pkgmodel.FormaApplyModePatch {
		return "patch"
	}
	if mod.ObservedCommand != "" {
		return strings.TrimSpace(string(mod.ObservedCommand) + " " + string(mod.ObservedMode) + " " + mod.ObservedSource)
	}
	return "unavailable"
}

// Keep ordinary labels readable while preventing terminal control sequences.
func driftDisplayIdentity(value string) string {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fmt.Sprintf("%q", value)
		}
	}
	return value
}
