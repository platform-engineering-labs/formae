// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// renderCard renders an expanded detail card for a simRow and returns a slice
// of styled lines (no trailing newlines). Reused verbatim by the driftview.
//
// Contract:
//   - Bordered card with title-in-border (op symbol + label) colored per-op via opColor
//   - Fields: Operation, Type (if set), Stack (if set)
//   - Changes: tree with ├/└ connectors, ordered:
//     1. "bring resource under management (unmanaged → <stack>)" when OldStackName=="$unmanaged"
//     2. "rename  <old> → <new>" when OldLabel != ""
//     3. property changes from PatchDocument (replace: immutable lines first, then mutable)
//   - NoOp changes skipped
//   - String scalars quoted; composites as compact JSON
//   - Styling: set old value TextSubtle, new value Done; add value Done;
//     remove Warning; immutable Warning; tree structure TextSecondary
func renderCard(th *theme.Theme, r simRow, width int) []string {
	p := th.Palette

	// Styles
	borderColor := p.PrimaryAccent
	borderSt := lipgloss.NewStyle().Foreground(borderColor)
	treeSt := lipgloss.NewStyle().Foreground(p.TextSecondary)
	doneSt := lipgloss.NewStyle().Foreground(p.Done)
	errSt := lipgloss.NewStyle().Foreground(p.Error)
	subtleSt := lipgloss.NewStyle().Foreground(p.TextSubtle)

	// The card shows the property-level detail — what is being created, changed,
	// or is causing the replace. The row already carries the operation, label and
	// type, so the card does not repeat them.
	var bodyLines []string

	switch {
	case r.op == opCreate && r.res != nil && len(r.res.Properties) > 0:
		// A create simply lists the properties it will set — like the inventory
		// detail screen — rather than a change tree of "add" lines.
		for _, l := range components.PropertyLines(r.res.Properties, 1) {
			bodyLines = append(bodyLines, treeSt.Render(l))
		}
	default:
		changeLines := collectChangeLines(th, r, doneSt, errSt, subtleSt)
		if len(changeLines) > 0 {
			for i, cl := range changeLines {
				connector := "├"
				if i == len(changeLines)-1 {
					connector = "└"
				}
				bodyLines = append(bodyLines, treeSt.Render(" "+connector+" ")+cl)
			}
		} else {
			// Nothing to diff (e.g. a delete or a clean keep) — a one-line summary
			// keeps the expanded card from being an empty box.
			bodyLines = append(bodyLines, subtleSt.Render(cardEmptyNote(r)))
		}
	}

	// --- Build the card ---
	// Inner width: card width minus 2 (borders) minus 2 (padding)
	cardW := width - 4
	if cardW < 30 {
		cardW = 30
	}
	innerW := cardW - 2 // lipgloss Width() adds 2 for border glyphs
	if innerW < 1 {
		innerW = 1
	}

	content := strings.Join(bodyLines, "\n")
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(innerW).
		Padding(0, 1).
		Render(content)

	// Measure actual rendered width from the bottom border
	cardLines := strings.Split(card, "\n")
	actualWidth := 0
	if len(cardLines) > 0 {
		actualWidth = lipgloss.Width(cardLines[len(cardLines)-1])
	}
	if actualWidth == 0 {
		actualWidth = innerW + 2
	}

	// Build title-in-border top line: ╭─ ~ label ─────╮
	// The op glyph always carries its per-op color. The label color is
	// theme-driven, mirroring the row renderer: rich accents it (and tints a
	// delete title with the delete op color); quiet renders it in the plain
	// column color, so nothing but the glyph distinguishes the card.
	opSymbol := opGlyph(th.Glyphs, r.op)
	glyphSt := lipgloss.NewStyle().Foreground(opColor(p, r.op))
	labelColor := p.TextSecondary
	if th.Rows.LabelAccent {
		labelColor = p.PrimaryAccent
	}
	if r.op == opDelete && th.Rows.DeleteWholeRow {
		labelColor = opColor(p, opDelete)
	}
	labelSt := lipgloss.NewStyle().Foreground(labelColor)
	titleContent := " " + glyphSt.Render(opSymbol) + " " + labelSt.Render(r.label) + " "
	titleW := lipgloss.Width(titleContent)
	dashW := actualWidth - titleW - 3 // 3 border glyphs: ╭ + ─ + ╮
	if dashW < 1 {
		dashW = 1
	}

	headerLine := borderSt.Render("╭─") +
		titleContent +
		borderSt.Render(strings.Repeat("─", dashW)+"╮")

	if len(cardLines) > 0 {
		cardLines[0] = headerLine
	}

	// Indent card by 2 spaces (matching row indent)
	result := make([]string, len(cardLines))
	for i, cl := range cardLines {
		result[i] = "  " + cl
	}
	return result
}

// collectChangeLines builds the ordered list of change line strings for a card.
// Order: (1) management, (2) rename, (3) property changes.
// For replace cards: immutable lines first (from delRes.CreateOnlyPatch), then
// mutable lines (from MutableChangesForReplace).
func collectChangeLines(th *theme.Theme, r simRow, doneSt, errSt, subtleSt lipgloss.Style) []string {
	var lines []string

	// (1) Management line — adopting an unmanaged resource is a benign action,
	// so use the brand accent, not the destructive Error color.
	if r.res != nil && r.res.OldStackName == "$unmanaged" {
		adoptSt := lipgloss.NewStyle().Foreground(th.Palette.SecondaryAccent)
		stackName := r.res.StackName
		if stackName == "" && r.stack != "" {
			stackName = r.stack
		}
		lines = append(lines, adoptSt.Render("bring resource under management")+" "+
			subtleSt.Render("(unmanaged → "+stackName+")"))
	}

	// (2) Rename line
	if r.res != nil && r.res.OldLabel != "" {
		lines = append(lines, subtleSt.Render("rename  ")+
			subtleSt.Render(r.res.OldLabel)+" "+
			subtleSt.Render("→")+" "+
			doneSt.Render(r.res.ResourceLabel))
	}

	// (3) Property changes
	if r.res != nil {
		propLines := buildPropertyChangeLines(th, r, doneSt, errSt, subtleSt)
		lines = append(lines, propLines...)
	}

	// Policy changes (if it's a policy row with config data)
	if r.policy != nil && r.res == nil {
		policyLines := buildPolicyChangeLines(th, r, doneSt, errSt, subtleSt)
		lines = append(lines, policyLines...)
	}

	// Generator changes (if it's a generator row with config data)
	if r.generator != nil && r.res == nil && r.policy == nil {
		generatorLines := buildGeneratorChangeLines(th, r, doneSt, errSt, subtleSt)
		lines = append(lines, generatorLines...)
	}

	return lines
}

// buildPropertyChangeLines extracts and formats property and tag changes for
// resource rows. Order mirrors FormatPatchDocument in internal/cli/renderer/patches.go:
// tag lines first, then property lines.
//
// For replace cards: immutable property lines first (Warning), then mutable set lines.
// For all others: all changes from PatchDocument, tags preceding properties.
func buildPropertyChangeLines(_ *theme.Theme, r simRow, doneSt, errSt, subtleSt lipgloss.Style) []string {
	if r.res == nil {
		return nil
	}

	// Safely retrieve patch doc and context
	patchDoc := r.res.PatchDocument
	properties := r.res.Properties
	oldProperties := r.res.OldProperties
	refLabels := r.res.ReferenceLabels

	// Creates are handled by renderCard directly (a property listing, not a
	// change tree), so an empty patch document here means nothing to render.
	if len(patchDoc) == 0 {
		return nil
	}
	if len(properties) == 0 {
		properties = []byte("{}")
	}
	if len(oldProperties) == 0 {
		oldProperties = []byte("{}")
	}

	var lines []string

	if r.op == opReplace && r.delRes != nil && len(r.delRes.CreateOnlyPatch) > 0 {
		// Replace card: immutable lines from CreateOnlyPatch (on the delete half)
		// Use the delete half's context (immutable fields are the reason for replace)
		delPatch := r.delRes.CreateOnlyPatch
		delProps := r.delRes.Properties
		delOldProps := r.delRes.OldProperties
		if len(delProps) == 0 {
			delProps = properties
		}
		if len(delOldProps) == 0 {
			delOldProps = oldProperties
		}
		cs, err := components.ExtractChanges(delPatch, delProps, delOldProps, refLabels)
		if err == nil {
			for _, ch := range cs.Properties {
				if ch.NoOp {
					continue
				}
				lines = append(lines, components.FormatPropertyChange(ch, "immutable", doneSt, errSt, subtleSt))
			}
		}

		// Mutable carried changes via MutableChangesForReplace
		mcs, err := components.MutableChangesForReplace(patchDoc, r.delRes.CreateOnlyPatch, properties, oldProperties, refLabels)
		if err == nil {
			for _, ch := range mcs.Properties {
				if ch.NoOp {
					continue
				}
				lines = append(lines, components.FormatPropertyChange(ch, "set", doneSt, errSt, subtleSt))
			}
		}
	} else {
		// Regular update/create/delete card
		cs, err := components.ExtractChanges(patchDoc, properties, oldProperties, refLabels)
		if err == nil {
			for _, ch := range cs.Properties {
				if ch.NoOp {
					continue
				}
				lines = append(lines, components.FormatPropertyChange(ch, "", doneSt, errSt, subtleSt))
			}
		}
	}

	return lines
}

// cardEmptyNote returns a one-line summary for cards that have no property
// detail to show (a delete has nothing to diff; a keep is a no-op; a draw
// carries no declared config).
func cardEmptyNote(r simRow) string {
	switch r.op {
	case opDelete:
		return "This resource will be removed."
	case opDetach:
		return "This policy will be detached."
	case opDraw:
		return "A new value is drawn: every resource bound to this generator takes it."
	case opKeep:
		return "No changes."
	default:
		return "No property changes."
	}
}

// jsonKeyDiffLines renders a minimal key-level diff between two JSON objects
// as "set"/"add"/"remove" lines, sorted by key for deterministic output.
// Shared by buildPolicyChangeLines and buildGeneratorChangeLines, which
// differ only in which raw JSON pair (config, old config) they hand it.
func jsonKeyDiffLines(currRaw, oldRaw json.RawMessage, doneSt, errSt, subtleSt lipgloss.Style) []string {
	if len(currRaw) == 0 && len(oldRaw) == 0 {
		return nil
	}

	var curr, old map[string]json.RawMessage
	if len(currRaw) > 0 {
		_ = json.Unmarshal(currRaw, &curr)
	}
	if len(oldRaw) > 0 {
		_ = json.Unmarshal(oldRaw, &old)
	}
	if curr == nil && old == nil {
		return nil
	}

	// Collect all keys into a sorted slice for deterministic output order.
	allKeysSet := map[string]struct{}{}
	for k := range curr {
		allKeysSet[k] = struct{}{}
	}
	for k := range old {
		allKeysSet[k] = struct{}{}
	}
	allKeys := make([]string, 0, len(allKeysSet))
	for k := range allKeysSet {
		allKeys = append(allKeys, k)
	}
	sort.Strings(allKeys)

	var lines []string
	for _, k := range allKeys {
		cv, cok := curr[k]
		ov, ook := old[k]
		switch {
		case cok && ook:
			if string(cv) != string(ov) {
				oldStr := components.QuoteCardValue(string(ov))
				newStr := components.QuoteCardValue(string(cv))
				kw := subtleSt.Render("set")
				lines = append(lines, kw+"  "+subtleSt.Render(k)+": "+subtleSt.Render(oldStr)+" → "+doneSt.Render(newStr))
			}
		case cok:
			kw := doneSt.Render("add")
			lines = append(lines, kw+"  "+subtleSt.Render(k)+": "+doneSt.Render(components.QuoteCardValue(string(cv))))
		case ook:
			kw := errSt.Render("remove")
			lines = append(lines, kw+"  "+errSt.Render(k))
		}
	}
	return lines
}

// buildPolicyChangeLines renders a minimal policy config diff for policy rows.
// If PolicyConfig/OldPolicyConfig are present, show key-level differences.
// Otherwise, render nothing — the card already shows Operation/Type/Stack.
func buildPolicyChangeLines(_ *theme.Theme, r simRow, doneSt, errSt, subtleSt lipgloss.Style) []string {
	if r.policy == nil {
		return nil
	}
	return jsonKeyDiffLines(r.policy.PolicyConfig, r.policy.OldPolicyConfig, doneSt, errSt, subtleSt)
}

// buildGeneratorChangeLines renders a minimal generator config diff for
// generator rows, the same key-level format as buildPolicyChangeLines. On a
// Create every declared field renders as "add" (there is no old config); on
// Update, a "set" line per changed field. On Delete, GeneratorConfig still
// carries the doomed generator's spec with no OldGeneratorConfig to diff
// against, so its fields render as "add" too — the row's own opDelete
// operation glyph and cardEmptyNote's delete wording are what tell the user
// this is a removal; this mirrors buildPolicyChangeLines's identical
// behavior for a policy delete (pu.Policy holds the existing policy there
// as well). GeneratorConfig/OldGeneratorConfig carry declared configuration
// only (see apimodel.GeneratorUpdate) — nothing here can render a
// generator's identity or a drawn value.
func buildGeneratorChangeLines(_ *theme.Theme, r simRow, doneSt, errSt, subtleSt lipgloss.Style) []string {
	if r.generator == nil {
		return nil
	}
	return jsonKeyDiffLines(r.generator.GeneratorConfig, r.generator.OldGeneratorConfig, doneSt, errSt, subtleSt)
}
