// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/renderer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// renderCard renders an expanded detail card for a simRow and returns a slice
// of styled lines (no trailing newlines). Reused verbatim by Task 14's driftview.
//
// Contract:
//   - Bordered card with title-in-border (op symbol + label) in PrimaryAccent style
//   - Fields: Operation, Type (if set), Stack (if set)
//   - Changes: tree with ├/└ connectors, ordered:
//     1. "put resource under management (unmanaged → <stack>)" when OldStackName=="$unmanaged"
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
	fieldSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
	valueSt := lipgloss.NewStyle().Foreground(p.TextSecondary)
	treeSt := lipgloss.NewStyle().Foreground(p.TextSecondary)
	doneSt := lipgloss.NewStyle().Foreground(p.Done)
	warnSt := lipgloss.NewStyle().Foreground(p.Warning)
	subtleSt := lipgloss.NewStyle().Foreground(p.TextSubtle)

	kv := func(k, v string) string {
		return fieldSt.Render(k) + " " + valueSt.Render(v)
	}

	// Collect card body lines
	var bodyLines []string

	// --- Operation/Type/Stack fields ---
	opWord := r.op.word()
	bodyLines = append(bodyLines, kv("Operation:", opWord))
	if r.typ != "" {
		bodyLines = append(bodyLines, kv("Type:     ", r.typ))
	}
	if r.stack != "" {
		bodyLines = append(bodyLines, kv("Stack:    ", r.stack))
	}

	// --- Collect change lines ---
	changeLines := collectChangeLines(th, r, doneSt, warnSt, subtleSt)

	if len(changeLines) > 0 {
		bodyLines = append(bodyLines, "")
		bodyLines = append(bodyLines, fieldSt.Render("Changes:"))

		for i, cl := range changeLines {
			connector := "├"
			if i == len(changeLines)-1 {
				connector = "└"
			}
			bodyLines = append(bodyLines, treeSt.Render(" "+connector+" ")+cl)
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
	opSymbol := r.op.symbol()
	titleStr := opSymbol + " " + r.label
	titleSt := lipgloss.NewStyle().Foreground(p.PrimaryAccent)
	titleContent := " " + titleSt.Render(titleStr) + " "
	titleW := lipgloss.Width(titleContent)
	dashW := actualWidth - titleW - 2 // 2 = ╭ + ╮
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
func collectChangeLines(th *theme.Theme, r simRow, doneSt, warnSt, subtleSt lipgloss.Style) []string {
	var lines []string

	// (1) Management line
	if r.res != nil && r.res.OldStackName == "$unmanaged" {
		stackName := r.res.StackName
		if stackName == "" && r.stack != "" {
			stackName = r.stack
		}
		lines = append(lines, warnSt.Render("put resource under management")+" "+
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
		propLines := buildPropertyChangeLines(th, r, doneSt, warnSt, subtleSt)
		lines = append(lines, propLines...)
	}

	// Policy changes (if it's a policy row with config data)
	if r.policy != nil && r.res == nil {
		policyLines := buildPolicyChangeLines(th, r, doneSt, warnSt, subtleSt)
		lines = append(lines, policyLines...)
	}

	return lines
}

// buildPropertyChangeLines extracts and formats property and tag changes for
// resource rows. Order mirrors FormatPatchDocument in internal/cli/renderer/patches.go:
// tag lines first, then property lines.
//
// For replace cards: immutable property lines first (Warning), then mutable set lines.
// For all others: all changes from PatchDocument, tags preceding properties.
func buildPropertyChangeLines(_ *theme.Theme, r simRow, doneSt, warnSt, subtleSt lipgloss.Style) []string {
	if r.res == nil {
		return nil
	}

	// Safely retrieve patch doc and context
	patchDoc := r.res.PatchDocument
	properties := r.res.Properties
	oldProperties := r.res.OldProperties
	refLabels := r.res.ReferenceLabels

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
		cs, err := renderer.ExtractChanges(delPatch, delProps, delOldProps, refLabels)
		if err == nil {
			// Tags first (mirrors renderer order)
			for _, tch := range cs.Tags {
				lines = append(lines, components.FormatTagChange(tch, doneSt, warnSt, subtleSt))
			}
			for _, ch := range cs.Properties {
				if ch.NoOp {
					continue
				}
				lines = append(lines, components.FormatPropertyChange(ch, "immutable", doneSt, warnSt, subtleSt))
			}
		}

		// Mutable carried changes via MutableChangesForReplace
		mcs, err := renderer.MutableChangesForReplace(patchDoc, r.delRes.CreateOnlyPatch, properties, oldProperties, refLabels)
		if err == nil {
			// Tags first (mirrors renderer order)
			for _, tch := range mcs.Tags {
				lines = append(lines, components.FormatTagChange(tch, doneSt, warnSt, subtleSt))
			}
			for _, ch := range mcs.Properties {
				if ch.NoOp {
					continue
				}
				lines = append(lines, components.FormatPropertyChange(ch, "set", doneSt, warnSt, subtleSt))
			}
		}
	} else {
		// Regular update/create/delete card
		cs, err := renderer.ExtractChanges(patchDoc, properties, oldProperties, refLabels)
		if err == nil {
			// Tags first (mirrors FormatPatchDocument order: cs.Tags then cs.Properties)
			for _, tch := range cs.Tags {
				lines = append(lines, components.FormatTagChange(tch, doneSt, warnSt, subtleSt))
			}
			for _, ch := range cs.Properties {
				if ch.NoOp {
					continue
				}
				lines = append(lines, components.FormatPropertyChange(ch, "", doneSt, warnSt, subtleSt))
			}
		}
	}

	return lines
}

// buildPolicyChangeLines renders a minimal policy config diff for policy rows.
// If PolicyConfig/OldPolicyConfig are present, show key-level differences.
// Otherwise, render nothing — the card already shows Operation/Type/Stack.
func buildPolicyChangeLines(_ *theme.Theme, r simRow, doneSt, warnSt, subtleSt lipgloss.Style) []string {
	if r.policy == nil {
		return nil
	}
	pu := r.policy
	if len(pu.PolicyConfig) == 0 && len(pu.OldPolicyConfig) == 0 {
		return nil
	}

	var curr, old map[string]json.RawMessage
	if len(pu.PolicyConfig) > 0 {
		_ = json.Unmarshal(pu.PolicyConfig, &curr)
	}
	if len(pu.OldPolicyConfig) > 0 {
		_ = json.Unmarshal(pu.OldPolicyConfig, &old)
	}
	if curr == nil && old == nil {
		return nil
	}

	// Collect all keys
	allKeys := map[string]struct{}{}
	for k := range curr {
		allKeys[k] = struct{}{}
	}
	for k := range old {
		allKeys[k] = struct{}{}
	}

	var lines []string
	for k := range allKeys {
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
			kw := warnSt.Render("remove")
			lines = append(lines, kw+"  "+warnSt.Render(k))
		}
	}
	return lines
}
