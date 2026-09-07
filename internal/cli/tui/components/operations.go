// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// OperationGlyph returns the themed glyph for an operation string (create,
// update, delete, replace), or "" for operations without a distinct glyph.
// Shared by the simulation and status views so the operation column renders
// identically in both.
func OperationGlyph(g theme.Glyphs, op string) string {
	switch op {
	case apimodel.OperationCreate:
		return g.OpCreate
	case apimodel.OperationUpdate:
		return g.OpUpdate
	case apimodel.OperationDelete:
		return g.OpDelete
	case apimodel.OperationReplace:
		return g.OpReplace
	}
	return ""
}

// OperationColor returns the themed per-operation color for an operation string,
// falling back to TextPrimary for operations without a distinct color.
func OperationColor(p theme.Palette, op string) lipgloss.AdaptiveColor {
	switch op {
	case apimodel.OperationCreate:
		return p.OpCreate
	case apimodel.OperationUpdate:
		return p.OpUpdate
	case apimodel.OperationDelete:
		return p.OpDelete
	case apimodel.OperationReplace:
		return p.OpReplace
	}
	return p.TextPrimary
}

// noRotationStatement is the positive statement a plan makes when a generator
// is in scope and none of them will draw.
//
// Rotation is unattended credential mutation, so "will this apply turn a live
// secret over" is the question an operator answers the confirmation prompt
// with. A prompt that is silent both when nothing rotates and when something
// does answers it for neither, which is the defect the rotate clause fixed for
// the affirmative case; this is the same fix for the negative one.
//
// It is stated only when the plan actually carries generator work. An apply
// that touches no generator has nothing to reassure anyone about, and a
// sentence about rotation on every S3 bucket apply is noise.
const noRotationStatement = "No generator will rotate."

// PromptForOperations returns a human-readable prompt summarising the
// operations that will be performed by cmd, followed by a confirmation
// question. Returns "" when there is nothing to do. th supplies the active
// theme's colors for the rendered summary.
func PromptForOperations(th *theme.Theme, cmd *apimodel.Command) string {
	tally := analyzeCommands(cmd)
	if tally.empty() {
		return ""
	}

	summary := operationSummary(th, tally)
	if summary == "" {
		return ""
	}

	if tally.generatorsInScope() && tally.generatorDraws == 0 {
		dimSt := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
		summary += " " + dimSt.Render(noRotationStatement)
	}

	return summary + "\n\nDo you want to continue?"
}

// opTally counts the operations a command performs, by entity and kind. It is
// a struct rather than a return list because the counts are all ints and the
// summary reads a dozen of them: a positional list is a transposition waiting
// to happen.
type opTally struct {
	targetCreates    int
	targetUpdates    int
	stackCreates     int
	stackUpdates     int
	policyCreates    int
	policyUpdates    int
	generatorCreates int
	generatorUpdates int
	generatorDraws   int
	resourceCreates  int
	resourceUpdates  int
	resourceDeletes  int
	resourceReplaces int
}

// empty reports whether the command performs no operation at all.
func (t opTally) empty() bool {
	return t == opTally{}
}

// generatorsInScope reports whether the plan carries any generator work. It is
// what gates noRotationStatement: a plan with no generator in it makes no claim
// about rotation either way.
//
// A delete is not counted. Removing a generator is not a plan that leaves a
// live credential in place, so "nothing will rotate" is not the reassurance
// anyone is looking for there.
func (t opTally) generatorsInScope() bool {
	return t.generatorCreates > 0 || t.generatorUpdates > 0 || t.generatorDraws > 0
}

// analyzeCommands counts each operation type in cmd, grouping grouped
// (delete+create) pairs as replaces.
func analyzeCommands(cmd *apimodel.Command) opTally {
	var tally opTally
	// Group operations by GroupId
	groupedOperations := make(map[string][]apimodel.ResourceUpdate)
	ungroupedOperations := make([]apimodel.ResourceUpdate, 0)

	for _, rc := range cmd.ResourceUpdates {
		if rc.Operation == apimodel.OperationRead {
			continue
		}

		if rc.GroupID != "" {
			groupedOperations[rc.GroupID] = append(groupedOperations[rc.GroupID], rc)
		} else {
			ungroupedOperations = append(ungroupedOperations, rc)
		}
	}

	// Count grouped operations
	for _, group := range groupedOperations {
		if len(group) == 0 {
			continue
		}

		hasDelete := false
		hasCreate := false
		hasUpdate := false

		for _, op := range group {
			switch op.Operation {
			case apimodel.OperationDelete:
				hasDelete = true
			case apimodel.OperationCreate:
				hasCreate = true
			case apimodel.OperationUpdate:
				hasUpdate = true
			}
		}

		if hasDelete && hasCreate {
			tally.resourceReplaces++
		} else if hasDelete {
			tally.resourceDeletes++
		} else if hasCreate {
			tally.resourceCreates++
		} else if hasUpdate {
			tally.resourceUpdates++
		}
	}

	// Count ungrouped operations
	for _, rc := range ungroupedOperations {
		switch rc.Operation {
		case apimodel.OperationCreate:
			tally.resourceCreates++
		case apimodel.OperationUpdate:
			tally.resourceUpdates++
		case apimodel.OperationDelete:
			tally.resourceDeletes++
		case apimodel.OperationReplace:
			tally.resourceReplaces++
		}
	}

	for _, tu := range cmd.TargetUpdates {
		switch tu.Operation {
		case "create":
			tally.targetCreates++
		case "update":
			tally.targetUpdates++
		}
	}

	// Count stack updates
	for _, su := range cmd.StackUpdates {
		switch su.Operation {
		case "create":
			tally.stackCreates++
		case "update":
			tally.stackUpdates++
		}
	}

	// Count policy updates
	for _, pu := range cmd.PolicyUpdates {
		switch pu.Operation {
		case "create":
			tally.policyCreates++
		case "update":
			tally.policyUpdates++
		}
	}

	// Count generator updates. A draw counts separately from the three that
	// change the generator's own row: it rotates the secret every resource
	// bound to the generator holds, so an operator confirming the apply has to
	// be told about it even when no row changes.
	for _, gu := range cmd.GeneratorUpdates {
		switch gu.Operation {
		case apimodel.OperationCreate:
			tally.generatorCreates++
		case apimodel.OperationUpdate:
			tally.generatorUpdates++
		case apimodel.OperationDraw:
			tally.generatorDraws++
		}
	}

	return tally
}

// operationSummary builds the colored "This operation will …" sentence.
// Colors use theme roles: Error for destructive ops (delete/replace), gold for
// a credential rotation, Done for creates, and TextPrimary for updates
// (routine updates aren't tinted).
func operationSummary(th *theme.Theme, t opTally) string {
	if t.empty() {
		return ""
	}

	errSt := lipgloss.NewStyle().Foreground(th.Palette.Error)
	rotateSt := lipgloss.NewStyle().Foreground(th.Palette.Warning)
	doneSt := lipgloss.NewStyle().Foreground(th.Palette.Done)
	updateSt := lipgloss.NewStyle().Foreground(th.Palette.TextPrimary)

	var parts []string

	// Destructive resource ops first
	if t.resourceDeletes > 0 {
		parts = append(parts, errSt.Render(fmt.Sprintf("delete %d resource(s)", t.resourceDeletes)))
	}
	if t.resourceReplaces > 0 {
		parts = append(parts, errSt.Render(fmt.Sprintf("replace %d resource(s)", t.resourceReplaces)))
	}

	// A rotation destroys nothing but invalidates a live secret everywhere it
	// is bound, so it leads the non-destructive ops and says what it costs.
	if t.generatorDraws > 0 {
		parts = append(parts, rotateSt.Render(fmt.Sprintf(
			"rotate %d generator(s) (every bound resource takes a new secret)", t.generatorDraws)))
	}

	// Creates (stacks, policies, targets, resources)
	if t.stackCreates > 0 {
		parts = append(parts, doneSt.Render(fmt.Sprintf("create %d stack(s)", t.stackCreates)))
	}
	if t.policyCreates > 0 {
		parts = append(parts, doneSt.Render(fmt.Sprintf("create %d policy(ies)", t.policyCreates)))
	}
	if t.generatorCreates > 0 {
		parts = append(parts, doneSt.Render(fmt.Sprintf("create %d generator(s)", t.generatorCreates)))
	}
	if t.targetCreates > 0 {
		parts = append(parts, doneSt.Render(fmt.Sprintf("create %d target(s)", t.targetCreates)))
	}
	if t.resourceCreates > 0 {
		parts = append(parts, doneSt.Render(fmt.Sprintf("create %d resource(s)", t.resourceCreates)))
	}

	// Updates (stacks, policies, targets, resources)
	if t.stackUpdates > 0 {
		parts = append(parts, updateSt.Render(fmt.Sprintf("update %d stack(s)", t.stackUpdates)))
	}
	if t.policyUpdates > 0 {
		parts = append(parts, updateSt.Render(fmt.Sprintf("update %d policy(ies)", t.policyUpdates)))
	}
	if t.generatorUpdates > 0 {
		parts = append(parts, updateSt.Render(fmt.Sprintf("update %d generator(s)", t.generatorUpdates)))
	}
	if t.targetUpdates > 0 {
		parts = append(parts, updateSt.Render(fmt.Sprintf("update %d target(s)", t.targetUpdates)))
	}
	if t.resourceUpdates > 0 {
		parts = append(parts, updateSt.Render(fmt.Sprintf("update %d resource(s)", t.resourceUpdates)))
	}

	var joinedParts string
	if len(parts) == 1 {
		joinedParts = parts[0]
	} else {
		joinedParts = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}

	return fmt.Sprintf("This operation will %s.", joinedParts)
}
