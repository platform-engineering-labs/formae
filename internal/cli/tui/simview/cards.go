// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/renderer"
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
func buildPropertyChangeLines(th *theme.Theme, r simRow, doneSt, warnSt, subtleSt lipgloss.Style) []string {
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
				lines = append(lines, formatSimTagChange(tch, doneSt, warnSt, subtleSt))
			}
			for _, ch := range cs.Properties {
				if ch.NoOp {
					continue
				}
				lines = append(lines, formatSimPropertyChange(ch, "immutable", doneSt, warnSt, subtleSt))
			}
		}

		// Mutable carried changes via MutableChangesForReplace
		mcs, err := renderer.MutableChangesForReplace(patchDoc, r.delRes.CreateOnlyPatch, properties, oldProperties, refLabels)
		if err == nil {
			// Tags first (mirrors renderer order)
			for _, tch := range mcs.Tags {
				lines = append(lines, formatSimTagChange(tch, doneSt, warnSt, subtleSt))
			}
			for _, ch := range mcs.Properties {
				if ch.NoOp {
					continue
				}
				lines = append(lines, formatSimPropertyChange(ch, "set", doneSt, warnSt, subtleSt))
			}
		}
	} else {
		// Regular update/create/delete card
		cs, err := renderer.ExtractChanges(patchDoc, properties, oldProperties, refLabels)
		if err == nil {
			// Tags first (mirrors FormatPatchDocument order: cs.Tags then cs.Properties)
			for _, tch := range cs.Tags {
				lines = append(lines, formatSimTagChange(tch, doneSt, warnSt, subtleSt))
			}
			for _, ch := range cs.Properties {
				if ch.NoOp {
					continue
				}
				lines = append(lines, formatSimPropertyChange(ch, "", doneSt, warnSt, subtleSt))
			}
		}
	}

	return lines
}

// formatSimTagChange formats a single TagChange into a card line.
// Mirrors the mockup format from docs/mockups/simulation-preview.txt:
//
//	add  Tags[key]: "value"     — Done style for keyword and value
//	remove  Tags[key]           — Warning style for keyword and key (no value)
//	set  Tags[key]: "old" → "new" — TextSubtle old, Done new (replace operation)
func formatSimTagChange(tch renderer.TagChange, doneSt, warnSt, subtleSt lipgloss.Style) string {
	path := "Tags[" + tch.Key + "]"

	switch tch.Operation {
	case "add":
		kw := doneSt.Render("add")
		pathStr := subtleSt.Render(path)
		val := quoteCardValue(tch.Value)
		return kw + "  " + pathStr + ": " + doneSt.Render(val)

	case "remove":
		kw := warnSt.Render("remove")
		pathStr := warnSt.Render(path)
		return kw + "  " + pathStr

	case "replace":
		kw := subtleSt.Render("set")
		pathStr := subtleSt.Render(path)
		if tch.HasOld {
			oldVal := quoteCardValue(tch.OldValue)
			newVal := quoteCardValue(tch.Value)
			return kw + "  " + pathStr + ": " + subtleSt.Render(oldVal) + " → " + doneSt.Render(newVal)
		}
		newVal := quoteCardValue(tch.Value)
		return kw + "  " + pathStr + ": " + doneSt.Render(newVal)

	default:
		return subtleSt.Render(tch.Operation) + "  " + subtleSt.Render(path)
	}
}

// formatSimPropertyChange formats a single PropertyChange into a card line.
// verb overrides the keyword ("immutable" for replace causes). If verb is "",
// the keyword is derived from the operation (add/set/remove).
func formatSimPropertyChange(ch renderer.PropertyChange, verb string, doneSt, warnSt, subtleSt lipgloss.Style) string {
	path := stripCardArrayIndices(ch.Path)

	// Cascade-resolvable: "set  Path → new <label> (current: "value")"
	if ch.IsCascadeResolvable {
		keyword := "set"
		if verb != "" {
			keyword = verb
		}
		kw := warnSt.Render(keyword)
		if keyword == "set" {
			kw = subtleSt.Render(keyword)
		}
		suffix := "new " + ch.CascadeSourceLabel
		if ch.CascadeCurrentValue != "" {
			truncated := truncateCascadeValue(ch.CascadeCurrentValue, 40)
			suffix += ` (current: "` + truncated + `")`
		}
		return kw + "  " + subtleSt.Render(path) + " → " + doneSt.Render(suffix)
	}

	// Determine keyword
	keyword := verb
	if keyword == "" {
		switch ch.Operation {
		case "add":
			if ch.ExistsInPrevious {
				keyword = "set"
			} else {
				keyword = "add"
			}
		case "replace":
			keyword = "set"
		case "remove":
			keyword = "remove"
		default:
			keyword = ch.Operation
		}
	}

	switch keyword {
	case "set":
		kw := subtleSt.Render("set")
		pathStr := subtleSt.Render(path)
		if ch.HasOld {
			oldVal := quoteCardValue(ch.OldValue)
			newVal := quoteCardValue(ch.Value)
			return kw + "  " + pathStr + ": " + subtleSt.Render(oldVal) + " → " + doneSt.Render(newVal)
		}
		newVal := quoteCardValue(ch.Value)
		return kw + "  " + pathStr + ": " + doneSt.Render(newVal)

	case "add":
		kw := doneSt.Render("add")
		pathStr := subtleSt.Render(path)
		val := quoteCardValue(ch.Value)
		return kw + "  " + pathStr + ": " + doneSt.Render(val)

	case "remove":
		kw := warnSt.Render("remove")
		pathStr := warnSt.Render(path)
		return kw + "  " + pathStr

	case "immutable":
		kw := warnSt.Render("immutable")
		pathStr := warnSt.Render(path)
		if ch.HasOld {
			oldVal := quoteCardValue(ch.OldValue)
			newVal := quoteCardValue(ch.Value)
			return kw + "  " + pathStr + ": " + warnSt.Render(oldVal) + " → " + warnSt.Render(newVal)
		}
		val := quoteCardValue(ch.Value)
		return kw + "  " + pathStr + ": " + warnSt.Render(val)

	default:
		return subtleSt.Render(keyword) + "  " + subtleSt.Render(path)
	}
}

// quoteCardValue mirrors how the old renderer's formatPropertyChange decides quoting:
// string scalars get quoted, everything else renders as-is (compact JSON for composites,
// numbers/bools unquoted).
//
// The renderer's extractPropertyChange stores:
//   - string scalars: formatPatchValue returns the raw string (unquoted)
//   - composites: formatPatchValue returns compact JSON
//   - references: formatReferenceValue returns "label.property"
//
// So if Value looks like it came back as a plain string that isn't a composite,
// we quote it. We detect composites by a leading '{' or '['.
func quoteCardValue(v string) string {
	if v == "" || v == "(opaque value)" || v == "null" {
		return v
	}
	// Already a composite (JSON object or array)
	if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
		return v
	}
	// Already quoted
	if strings.HasPrefix(v, `"`) {
		return v
	}
	// Boolean literals
	if v == "true" || v == "false" {
		return v
	}
	// Number check (numeric values shouldn't be quoted)
	if isNumericString(v) {
		return v
	}
	// String scalar — quote it (includes reference labels like "label.property")
	return fmt.Sprintf("%q", v)
}

// isNumericString returns true if s is a valid JSON number.
func isNumericString(s string) bool {
	var n json.Number
	return json.Unmarshal([]byte(s), &n) == nil
}

// stripCardArrayIndices removes array index suffixes like [0] from property paths
// for cleaner display — mirrors the renderer's stripArrayIndices.
func stripCardArrayIndices(path string) string {
	parts := strings.Split(path, "[")
	if len(parts) == 1 {
		return path
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		if bracketEnd := strings.Index(parts[i], "]"); bracketEnd != -1 {
			result += parts[i][bracketEnd+1:]
		}
	}
	return result
}

// truncateCascadeValue truncates the cascade current value to maxLen runes,
// appending "…" if truncated.
func truncateCascadeValue(v string, maxLen int) string {
	runes := []rune(v)
	if len(runes) <= maxLen {
		return v
	}
	return string(runes[:maxLen-1]) + "…"
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
				oldStr := quoteCardValue(string(ov))
				newStr := quoteCardValue(string(cv))
				kw := subtleSt.Render("set")
				lines = append(lines, kw+"  "+subtleSt.Render(k)+": "+subtleSt.Render(oldStr)+" → "+doneSt.Render(newStr))
			}
		case cok:
			kw := doneSt.Render("add")
			lines = append(lines, kw+"  "+subtleSt.Render(k)+": "+doneSt.Render(quoteCardValue(string(cv))))
		case ook:
			kw := warnSt.Render("remove")
			lines = append(lines, kw+"  "+warnSt.Render(k))
		}
	}
	return lines
}
