// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// RenderChangeLinesFromPatch extracts changes from a JSON-patch document and
// returns styled change lines (no tree connectors). Tag changes precede
// property changes, mirroring the renderer's FormatPatchDocument order.
// refLabels, when non-empty, is a JSON-encoded map[string]string of
// reference labels. Returns nil, nil when patchDoc is empty.
func RenderChangeLinesFromPatch(th *theme.Theme, patchDoc, properties, oldProperties, refLabels json.RawMessage) ([]string, error) {
	if len(patchDoc) == 0 {
		return nil, nil
	}
	if len(properties) == 0 {
		properties = []byte("{}")
	}
	if len(oldProperties) == 0 {
		oldProperties = []byte("{}")
	}

	var refs map[string]string
	if len(refLabels) > 0 {
		_ = json.Unmarshal(refLabels, &refs)
	}

	cs, err := ExtractChanges(patchDoc, properties, oldProperties, refs)
	if err != nil {
		return nil, err
	}

	p := th.Palette
	doneSt := lipgloss.NewStyle().Foreground(p.Done)
	warnSt := lipgloss.NewStyle().Foreground(p.Warning)
	subtleSt := lipgloss.NewStyle().Foreground(p.TextSubtle)

	var lines []string
	for _, tch := range cs.Tags {
		lines = append(lines, FormatTagChange(tch, doneSt, warnSt, subtleSt))
	}
	for _, ch := range cs.Properties {
		if ch.NoOp {
			continue
		}
		lines = append(lines, FormatPropertyChange(ch, "", doneSt, warnSt, subtleSt))
	}
	return lines, nil
}

// FormatTagChange formats a single TagChange into a card line.
// Mirrors the mockup format from docs/mockups/simulation-preview.txt:
//
//	add  Tags[key]: "value"     — Done style for keyword and value
//	remove  Tags[key]           — Warning style for keyword and key (no value)
//	set  Tags[key]: "old" → "new" — TextSubtle old, Done new (replace operation)
func FormatTagChange(tch TagChange, doneSt, warnSt, subtleSt lipgloss.Style) string {
	path := "Tags[" + tch.Key + "]"

	switch tch.Operation {
	case "add":
		kw := doneSt.Render("add")
		pathStr := subtleSt.Render(path)
		val := QuoteCardValue(tch.Value)
		return kw + "  " + pathStr + ": " + doneSt.Render(val)

	case "remove":
		kw := warnSt.Render("remove")
		pathStr := warnSt.Render(path)
		return kw + "  " + pathStr

	case "replace":
		kw := subtleSt.Render("set")
		pathStr := subtleSt.Render(path)
		if tch.HasOld {
			oldVal := QuoteCardValue(tch.OldValue)
			newVal := QuoteCardValue(tch.Value)
			return kw + "  " + pathStr + ": " + subtleSt.Render(oldVal) + " → " + doneSt.Render(newVal)
		}
		newVal := QuoteCardValue(tch.Value)
		return kw + "  " + pathStr + ": " + doneSt.Render(newVal)

	default:
		return subtleSt.Render(tch.Operation) + "  " + subtleSt.Render(path)
	}
}

// FormatPropertyChange formats a single PropertyChange into a card line.
// verb overrides the keyword ("immutable" for replace causes). If verb is "",
// the keyword is derived from the operation (add/set/remove).
func FormatPropertyChange(ch PropertyChange, verb string, doneSt, warnSt, subtleSt lipgloss.Style) string {
	path := StripCardArrayIndices(ch.Path)

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
			truncated := TruncateCascadeValue(ch.CascadeCurrentValue, 40)
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
			oldVal := QuoteCardValue(ch.OldValue)
			newVal := QuoteCardValue(ch.Value)
			return kw + "  " + pathStr + ": " + subtleSt.Render(oldVal) + " → " + doneSt.Render(newVal)
		}
		newVal := QuoteCardValue(ch.Value)
		return kw + "  " + pathStr + ": " + doneSt.Render(newVal)

	case "add":
		kw := doneSt.Render("add")
		pathStr := subtleSt.Render(path)
		val := QuoteCardValue(ch.Value)
		return kw + "  " + pathStr + ": " + doneSt.Render(val)

	case "remove":
		kw := warnSt.Render("remove")
		pathStr := warnSt.Render(path)
		return kw + "  " + pathStr

	case "immutable":
		kw := warnSt.Render("immutable")
		pathStr := warnSt.Render(path)
		if ch.HasOld {
			oldVal := QuoteCardValue(ch.OldValue)
			newVal := QuoteCardValue(ch.Value)
			return kw + "  " + pathStr + ": " + warnSt.Render(oldVal) + " → " + warnSt.Render(newVal)
		}
		val := QuoteCardValue(ch.Value)
		return kw + "  " + pathStr + ": " + warnSt.Render(val)

	default:
		return subtleSt.Render(keyword) + "  " + subtleSt.Render(path)
	}
}

// QuoteCardValue mirrors how the old renderer's formatPropertyChange decides quoting:
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
func QuoteCardValue(v string) string {
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
	if IsNumericString(v) {
		return v
	}
	// String scalar — quote it (includes reference labels like "label.property")
	return fmt.Sprintf("%q", v)
}

// IsNumericString returns true if s is a valid JSON number.
func IsNumericString(s string) bool {
	var n json.Number
	return json.Unmarshal([]byte(s), &n) == nil
}

// StripCardArrayIndices removes array index suffixes like [0] from property paths
// for cleaner display — mirrors the renderer's stripArrayIndices.
func StripCardArrayIndices(path string) string {
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

// TruncateCascadeValue truncates the cascade current value to maxLen runes,
// appending "…" if truncated.
func TruncateCascadeValue(v string, maxLen int) string {
	runes := []rune(v)
	if len(runes) <= maxLen {
		return v
	}
	return string(runes[:maxLen-1]) + "…"
}
