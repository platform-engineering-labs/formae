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

// panelRow joins panels horizontally (lipgloss.JoinHorizontal) when their
// combined rendered width fits within width. Otherwise stacks them vertically.
func panelRow(width int, panels ...string) string {
	if len(panels) == 0 {
		return ""
	}
	// Sum up total width of all panels
	totalW := 0
	for _, p := range panels {
		lines := strings.Split(p, "\n")
		maxW := 0
		for _, l := range lines {
			w := lipgloss.Width(l)
			if w > maxW {
				maxW = w
			}
		}
		totalW += maxW
	}
	if totalW <= width {
		return lipgloss.JoinHorizontal(lipgloss.Top, panels...)
	}
	return strings.Join(panels, "\n")
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

	structurePanel := buildStructurePanel(th, stats, panelWidth)
	commandsPanel := buildCommandsPanel(th, stats, panelWidth)
	resourcesPanel := buildResourcesPanel(th, stats, panelWidth)

	sb.WriteString(panelRow(width, structurePanel, commandsPanel, resourcesPanel))
	sb.WriteString("\n")

	// ── Row 2: Resource Types / Plugins / Resource Errors (if non-empty) ─────
	resourceTypesPanel := buildResourceTypesPanel(th, stats, panelWidth)
	pluginsPanel := buildPluginsPanel(th, stats, panelWidth)

	row2Panels := []string{resourceTypesPanel, pluginsPanel}
	if len(stats.ResourceErrors) > 0 {
		errorsPanel := buildResourceErrorsPanel(th, stats, panelWidth)
		row2Panels = append(row2Panels, errorsPanel)
	}

	sb.WriteString(panelRow(width, row2Panels...))

	return sb.String()
}

func buildStructurePanel(th *theme.Theme, stats apimodel.Stats, width int) string {
	totalTargets := sumMap(stats.Targets)
	lines := []string{
		fieldLine(th, "Stacks", fmt.Sprintf("%d", stats.Stacks)),
		fieldLine(th, "Targets", fmt.Sprintf("%d", totalTargets)),
	}
	return components.Panel(th, th.Palette.Border, "Structure", lines, width)
}

func buildCommandsPanel(th *theme.Theme, stats apimodel.Stats, width int) string {
	totalCommands := sumMap(stats.Commands)
	successes := stats.States["Success"]
	failures := stats.States["Failed"]
	inProgress := stats.States["InProgress"]

	successStr := th.Styles.StatusDone.Render(fmt.Sprintf("%d", successes))
	failureStr := th.Styles.StatusFailed.Render(fmt.Sprintf("%d", failures))
	inProgressStr := th.Styles.StatusInProgress.Render(fmt.Sprintf("%d", inProgress))

	successLabel := th.Styles.StatusDone.Render("Successes")
	failureLabel := th.Styles.StatusFailed.Render("Failures")
	inProgressLabel := th.Styles.StatusInProgress.Render("In Progress")

	lines := []string{
		fieldLine(th, "Total", fmt.Sprintf("%d", totalCommands)),
		styledFieldLine(successLabel, successStr),
		styledFieldLine(failureLabel, failureStr),
		styledFieldLine(inProgressLabel, inProgressStr),
	}
	return components.Panel(th, th.Palette.Border, "Commands", lines, width)
}

func buildResourcesPanel(th *theme.Theme, stats apimodel.Stats, width int) string {
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

	lines := []string{
		fieldLine(th, "Total", fmt.Sprintf("%d", totalResources)),
		fieldLine(th, "Managed", fmt.Sprintf("%d", totalManaged)),
		fieldLine(th, "Unmanaged", fmt.Sprintf("%d", totalUnmanaged)),
		styledFieldLine(pctLabel, pctStr),
	}
	return components.Panel(th, th.Palette.Border, "Resources", lines, width)
}

func buildResourceTypesPanel(th *theme.Theme, stats apimodel.Stats, width int) string {
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
	return components.Panel(th, th.Palette.Border, "Resource Types", lines, width)
}

func buildPluginsPanel(th *theme.Theme, stats apimodel.Stats, width int) string {
	plugins := make([]apimodel.PluginInfo, len(stats.Plugins))
	copy(plugins, stats.Plugins)
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Namespace < plugins[j].Namespace
	})
	lines := []string{}
	for _, plugin := range plugins {
		lines = append(lines, fieldLine(th, plugin.Namespace, plugin.Version))
	}
	return components.Panel(th, th.Palette.Border, "Plugins", lines, width)
}

func buildResourceErrorsPanel(th *theme.Theme, stats apimodel.Stats, width int) string {
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

	totalErrors := sumMap(stats.ResourceErrors)
	lines := []string{
		fieldLine(th, "Total Errors", fmt.Sprintf("%d", totalErrors)),
	}
	for _, ec := range errs {
		lines = append(lines, fieldLine(th, ec.name, fmt.Sprintf("%d", ec.count)))
	}
	return components.Panel(th, th.Palette.Error, "Resource Errors", lines, width)
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
