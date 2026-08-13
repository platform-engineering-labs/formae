// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// isTerminal is a package-level var so tests can stub it.
var isTerminal = tui.IsTerminal

// renderProfileList renders the profile listing with the active profile
// marked ● (mockup profile.txt View 1). Empty input renders the hint state.
func renderProfileList(th *theme.Theme, names []string, active string) string {
	if len(names) == 0 {
		hint := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary)
		return hint.Render("No profiles yet. Create one: formae profile create <name>")
	}
	activeStyle := lipgloss.NewStyle().Foreground(th.Palette.Done)
	subtle := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
	body := lipgloss.NewStyle().Foreground(th.Palette.TextPrimary)
	lines := make([]string, 0, len(names))
	for _, n := range names {
		if n == active {
			// Marker at indent 2; name aligns at 4 (after "● ").
			lines = append(lines, "  "+activeStyle.Render("● "+n)+" "+subtle.Render("(active)"))
			continue
		}
		lines = append(lines, "    "+body.Render(n))
	}
	// Listing → section header at col 0, entries indented beneath (CLI convention).
	return components.SectionHeader(th, "Profiles") + "\n" + strings.Join(lines, "\n")
}

// renderCurrentHuman renders the current-profile output for a human reader.
// When tty is false (piped) it returns just the bare name — scripts depend on
// this. When the profile is unset and piped it returns a plain two-line hint;
// when unset and on a TTY it returns the styled hint.
func renderCurrentHuman(th *theme.Theme, active string, tty bool) string {
	if active == "" {
		if !tty {
			return "no active profile yet\nactivate one: formae profile use <name>"
		}
		secondary := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary)
		subtle := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
		return secondary.Render("no active profile yet") + "\n" + subtle.Render("activate one: formae profile use <name>")
	}
	if !tty {
		return active
	}
	return lipgloss.NewStyle().Foreground(th.Palette.TextPrimary).Render(active)
}

// renderAck renders a checkmark acknowledgment line using the shared
// components.AckLine helper.
func renderAck(th *theme.Theme, text string) string {
	return components.AckLine(th, components.AckDone, text)
}

// printConfigWarnings writes a config's warnings, one per line, in the same
// shape App.PrintBanner uses. It takes the writer rather than reaching for
// os.Stderr so the caller decides the stream and tests can read it back.
func printConfigWarnings(w io.Writer, th *theme.Theme, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	label := lipgloss.NewStyle().Foreground(th.Palette.Warning).Render("Warning:")
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(w, "%s %s\n", label, warning)
	}
	_, _ = fmt.Fprintln(w)
}

// renderConfigView renders a config view as themed sections in a fixed order,
// so two runs of the same profile read identically. Section headers sit at
// column 0 with their fields indented beneath, matching the CLI convention in
// components.SectionHeader.
func renderConfigView(th *theme.Theme, view map[string]any) string {
	var sections []string

	if cli, ok := view["cli"].(map[string]any); ok {
		if conn, ok := cli["connection"].(map[string]any); ok {
			sections = append(sections, section(th, "Connection", pairsOf(conn)))
		}
		sections = append(sections, section(th, "CLI", pairsOf(withoutKey(cli, "connection"))))
	}
	for _, s := range []struct{ key, title string }{
		{"agent", "Agent"},
		{"artifacts", "Artifacts"},
		{"network", "Network"},
	} {
		if m, ok := view[s.key].(map[string]any); ok {
			sections = append(sections, section(th, s.title, pairsOf(m)))
		}
	}
	if dir, ok := view["pluginDir"].(string); ok && dir != "" {
		sections = append(sections, section(th, "Plugins", [][2]string{{"pluginDir", dir}}))
	}

	name, _ := view["profile"].(string)
	head := lipgloss.NewStyle().Foreground(th.Palette.TextPrimary).Render(name)
	return head + "\n\n" + strings.Join(sections, "\n\n")
}

func section(th *theme.Theme, title string, pairs [][2]string) string {
	return components.SectionHeader(th, title) + "\n" +
		components.Indent(components.FieldList(th, pairs), 2)
}

// pairsOf flattens a view section into sorted label/value pairs, rendering a
// nested object as dotted keys and a list as one indexed entry per item, so a
// whole section fits one aligned field list.
func pairsOf(m map[string]any) [][2]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs [][2]string
	for _, k := range keys {
		switch v := m[k].(type) {
		case map[string]any:
			for _, nested := range pairsOf(v) {
				pairs = append(pairs, [2]string{k + "." + nested[0], nested[1]})
			}
		case []any:
			for i, item := range v {
				label := fmt.Sprintf("%s[%d]", k, i)
				if nested, ok := item.(map[string]any); ok {
					for _, n := range pairsOf(nested) {
						pairs = append(pairs, [2]string{label + "." + n[0], n[1]})
					}
					continue
				}
				pairs = append(pairs, [2]string{label, fmt.Sprintf("%v", item)})
			}
		case []string:
			for i, item := range v {
				pairs = append(pairs, [2]string{fmt.Sprintf("%s[%d]", k, i), item})
			}
		case nil:
			// omit: an absent optional is not a field
		case string:
			if v != "" {
				pairs = append(pairs, [2]string{k, v})
			}
		default:
			pairs = append(pairs, [2]string{k, fmt.Sprintf("%v", v)})
		}
	}
	return pairs
}

func withoutKey(m map[string]any, key string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k != key {
			out[k] = v
		}
	}
	return out
}
