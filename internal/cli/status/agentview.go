// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package status

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// agentTermWidth is a package seam so tests can control the width without a pty.
var agentTermWidth = defaultAgentTermWidth

func defaultAgentTermWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return 100
}

// termWidth returns the terminal width for human output: real TTY width or 100
// when not a TTY (so piped output is deterministic).
func termWidth(w io.Writer) int {
	if !tui.IsTerminal(w) {
		return 100
	}
	return agentTermWidth(w)
}

// panelSpec is a panel's content before it is boxed, so a row of panels can be
// height-equalized (all boxes the same height, borders aligned) before render.
type panelSpec struct {
	title string
	color lipgloss.AdaptiveColor
	lines []string
}

// boxWidth returns the widest line of a rendered box.
func boxWidth(box string) int {
	maxW := 0
	for _, l := range strings.Split(box, "\n") {
		if w := lipgloss.Width(l); w > maxW {
			maxW = w
		}
	}
	return maxW
}

// renderPanelRow boxes each spec, padding every panel's content to the row's
// tallest so all boxes share a height and their borders align, then joins them
// horizontally with a gap-column between. Falls back to a vertical stack when
// the row is wider than the terminal.
func renderPanelRow(th *theme.Theme, termW, panelW, gap int, specs ...panelSpec) string {
	if len(specs) == 0 {
		return ""
	}
	maxLines := 0
	for _, s := range specs {
		if len(s.lines) > maxLines {
			maxLines = len(s.lines)
		}
	}
	boxes := make([]string, len(specs))
	totalW := 0
	for i, s := range specs {
		lines := s.lines
		for len(lines) < maxLines {
			lines = append(lines, "")
		}
		boxes[i] = components.Panel(th, s.color, s.title, lines, panelW)
		totalW += boxWidth(boxes[i])
	}
	totalW += gap * (len(boxes) - 1)
	if totalW > termW {
		return strings.Join(boxes, "\n")
	}
	withGaps := make([]string, 0, len(boxes)*2-1)
	spacer := strings.Repeat(" ", gap)
	for i, b := range boxes {
		if i > 0 {
			withGaps = append(withGaps, spacer)
		}
		withGaps = append(withGaps, b)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, withGaps...)
}

func sumMap(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

// renderAgentStats renders the full status agent output as lipgloss panels.
func renderAgentStats(th *theme.Theme, stats apimodel.Stats, width int) string {
	var sb strings.Builder

	// ── Header FieldList: Version, AgentID, Clients ──────────────────────────
	header := components.FieldList(th, [][2]string{
		{"Version", stats.Version},
		{"AgentID", stats.AgentID},
		{"Clients", fmt.Sprintf("%d", stats.Clients)},
	})
	sb.WriteString(header)
	sb.WriteString("\n\n")

	// ── Row 1: Structure / Commands / Resources ───────────────────────────────
	panelWidth := (width - 4) / 3
	if panelWidth < 24 {
		panelWidth = 24
	}
	const gap = 2

	sb.WriteString(renderPanelRow(th, width, panelWidth, gap,
		buildStructurePanel(th, stats),
		buildCommandsPanel(th, stats),
		buildResourcesPanel(th, stats),
	))
	sb.WriteString("\n")

	// ── Row 2: Resource Types / Plugins / Resource Errors (if non-empty) ─────
	row2 := []panelSpec{
		buildResourceTypesPanel(th, stats),
		buildPluginsPanel(th, stats),
	}
	if len(stats.ResourceErrors) > 0 {
		row2 = append(row2, buildResourceErrorsPanel(th, stats))
	}
	sb.WriteString(renderPanelRow(th, width, panelWidth, gap, row2...))

	return sb.String()
}

func buildStructurePanel(th *theme.Theme, stats apimodel.Stats) panelSpec {
	totalTargets := sumMap(stats.Targets)
	lines := []string{
		fieldLine(th, "Stacks", fmt.Sprintf("%d", stats.Stacks)),
		fieldLine(th, "Targets", fmt.Sprintf("%d", totalTargets)),
	}
	// Reap counts are only surfaced when non-zero, so a healthy fleet keeps the
	// Structure panel uncluttered.
	if stats.ReapPendingTargets > 0 {
		warnStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning)
		lines = append(lines, styledFieldLine(
			warnStyle.Render("Reap Pending"),
			warnStyle.Render(fmt.Sprintf("%d", stats.ReapPendingTargets)),
		))
	}
	if stats.ReapedTargets > 0 {
		errStyle := lipgloss.NewStyle().Foreground(th.Palette.Error)
		lines = append(lines, styledFieldLine(
			errStyle.Render("Reaped"),
			errStyle.Render(fmt.Sprintf("%d", stats.ReapedTargets)),
		))
	}
	return panelSpec{
		title: "Structure",
		color: th.Palette.Border,
		lines: lines,
	}
}

func buildCommandsPanel(th *theme.Theme, stats apimodel.Stats) panelSpec {
	totalCommands := sumMap(stats.Commands)

	// A zero count is neutral — nothing to flag — so "Failures  0" no longer
	// renders in alarming red. A non-zero count gets its semantic color.
	countLine := func(style lipgloss.Style, label string, n int) string {
		if n == 0 {
			return fieldLine(th, label, "0")
		}
		return styledFieldLine(style.Render(label), style.Render(fmt.Sprintf("%d", n)))
	}

	return panelSpec{
		title: "Commands",
		color: th.Palette.Border,
		lines: []string{
			fieldLine(th, "Total", fmt.Sprintf("%d", totalCommands)),
			countLine(th.Styles.StatusDone, "Successes", stats.States["Success"]),
			countLine(th.Styles.StatusFailed, "Failures", stats.States["Failed"]),
			countLine(th.Styles.StatusInProgress, "In Progress", stats.States["InProgress"]),
		},
	}
}

func buildResourcesPanel(th *theme.Theme, stats apimodel.Stats) panelSpec {
	totalManaged := sumMap(stats.ManagedResources)
	totalUnmanaged := sumMap(stats.UnmanagedResources)
	totalResources := totalManaged + totalUnmanaged

	p := 0
	if totalResources > 0 {
		p = int((float64(totalUnmanaged)/float64(totalResources))*100 + 0.5)
	}

	var unmanagedPctColor lipgloss.AdaptiveColor
	var unmanagedPctSymbol string
	if p == 0 {
		unmanagedPctColor = th.Palette.Done
		unmanagedPctSymbol = "✓"
	} else if p <= 75 {
		unmanagedPctColor = th.Palette.Warning
		unmanagedPctSymbol = "⚠"
	} else {
		unmanagedPctColor = th.Palette.Error
		unmanagedPctSymbol = "✗"
	}

	pctStyle := lipgloss.NewStyle().Foreground(unmanagedPctColor)
	pctStr := pctStyle.Render(fmt.Sprintf("%s %d%%", unmanagedPctSymbol, p))
	pctLabel := pctStyle.Render("Unmanaged %")

	return panelSpec{
		title: "Resources",
		color: th.Palette.Border,
		lines: []string{
			fieldLine(th, "Total", fmt.Sprintf("%d", totalResources)),
			fieldLine(th, "Managed", fmt.Sprintf("%d", totalManaged)),
			fieldLine(th, "Unmanaged", fmt.Sprintf("%d", totalUnmanaged)),
			styledFieldLine(pctLabel, pctStr),
		},
	}
}

func buildResourceTypesPanel(th *theme.Theme, stats apimodel.Stats) panelSpec {
	type typeCount struct {
		name  string
		count int
	}
	types := make([]typeCount, 0, len(stats.ResourceTypes))
	for name, count := range stats.ResourceTypes {
		types = append(types, typeCount{name, count})
	}
	// Sort: count DESC, then name ASC (deterministic for goldens)
	sort.Slice(types, func(i, j int) bool {
		if types[i].count != types[j].count {
			return types[i].count > types[j].count
		}
		return types[i].name < types[j].name
	})

	const maxRows = 10
	lines := []string{
		fieldLine(th, "Total Types", fmt.Sprintf("%d", len(stats.ResourceTypes))),
	}

	shown := types
	overflow := 0
	if len(types) > maxRows {
		overflow = len(types) - maxRows
		shown = types[:maxRows]
	}
	for _, tc := range shown {
		lines = append(lines, fieldLine(th, tc.name, fmt.Sprintf("%d", tc.count)))
	}
	if overflow > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
		lines = append(lines, moreStyle.Render(fmt.Sprintf("… and %d more types", overflow)))
	}
	return panelSpec{title: "Resource Types", color: th.Palette.Border, lines: lines}
}

func buildPluginsPanel(th *theme.Theme, stats apimodel.Stats) panelSpec {
	plugins := make([]apimodel.PluginInfo, len(stats.Plugins))
	copy(plugins, stats.Plugins)
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Namespace < plugins[j].Namespace
	})

	const maxRows = 10
	shown := plugins
	overflow := 0
	if len(plugins) > maxRows {
		overflow = len(plugins) - maxRows
		shown = plugins[:maxRows]
	}

	lines := []string{}
	for _, plugin := range shown {
		lines = append(lines, fieldLine(th, plugin.Namespace, plugin.Version))
	}
	if overflow > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
		lines = append(lines, moreStyle.Render(fmt.Sprintf("… and %d more plugins", overflow)))
	}
	return panelSpec{title: "Plugins", color: th.Palette.Border, lines: lines}
}

func buildResourceErrorsPanel(th *theme.Theme, stats apimodel.Stats) panelSpec {
	type errCount struct {
		name  string
		count int
	}
	errs := make([]errCount, 0, len(stats.ResourceErrors))
	for name, count := range stats.ResourceErrors {
		errs = append(errs, errCount{name, count})
	}
	sort.Slice(errs, func(i, j int) bool {
		if errs[i].count != errs[j].count {
			return errs[i].count > errs[j].count
		}
		return errs[i].name < errs[j].name
	})

	const maxRows = 10
	shown := errs
	overflow := 0
	if len(errs) > maxRows {
		overflow = len(errs) - maxRows
		shown = errs[:maxRows]
	}

	totalErrors := sumMap(stats.ResourceErrors)
	lines := []string{
		fieldLine(th, "Total Errors", fmt.Sprintf("%d", totalErrors)),
	}
	for _, ec := range shown {
		lines = append(lines, fieldLine(th, ec.name, fmt.Sprintf("%d", ec.count)))
	}
	if overflow > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
		lines = append(lines, moreStyle.Render(fmt.Sprintf("… and %d more errors", overflow)))
	}
	return panelSpec{title: "Resource Errors", color: th.Palette.Error, lines: lines}
}

// fieldLine renders a plain label + plain value pair as a styled line.
func fieldLine(th *theme.Theme, label, value string) string {
	labelStyle := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary)
	valueStyle := lipgloss.NewStyle().Foreground(th.Palette.TextPrimary)
	return labelStyle.Render(label) + "  " + valueStyle.Render(value)
}

// styledFieldLine renders two pre-styled strings side-by-side.
// Use when label and/or value are already styled.
func styledFieldLine(styledLabel, styledValue string) string {
	return styledLabel + "  " + styledValue
}
