// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// renderTable renders a slice of rows using the given column set and theme.
// It returns the table output (header + data rows) as a single string.
// entity is the plural noun used in the status line (e.g. "resources").
// maxResults <= 0 means unlimited. The capping notice is appended when the
// visible set is smaller than the total set.
func renderTable(th *theme.Theme, cols []components.Column, rows []row, styleCell func(*theme.Theme, int, string) string, entity string, maxResults, width int) string {
	tbl := components.NewTable(th, cols)
	tbl = tbl.SetSize(width, len(rows)+4) // generous height — no scrolling in non-TTY

	total := len(rows)
	vis := rows
	if maxResults > 0 && len(vis) > maxResults {
		vis = vis[:maxResults]
	}

	effCols := make([]components.Column, len(cols))
	copy(effCols, cols)

	cells := make([][]string, len(vis))
	styledCells := make([][]styledCell, len(vis))
	for i, r := range vis {
		rowCells := make([]string, len(r.cells))
		styledRow := make([]styledCell, len(r.cells))
		for col, cell := range r.cells {
			colWidth := 0
			if col < len(effCols) {
				colWidth = effCols[col].Width
			}
			plain := cell
			if colWidth > 0 {
				plain = components.Truncate(cell, colWidth)
			}
			rowCells[col] = plain
			sc := styledCell{col: col, plain: plain, styled: plain}
			if styleCell != nil && th != nil {
				sc.styled = styleCell(th, col, plain)
			}
			styledRow[col] = sc
		}
		cells[i] = rowCells
		styledCells[i] = styledRow
	}

	tbl = tbl.SetRows(cells)
	tableOut := tbl.View()
	tableLines := strings.Split(tableOut, "\n")

	// Remove trailing blank lines.
	for len(tableLines) > 0 && tableLines[len(tableLines)-1] == "" {
		tableLines = tableLines[:len(tableLines)-1]
	}

	tableLines = applyCellStyles(tableLines, styledCells, tbl, effCols)

	shown := len(vis)
	var sb strings.Builder
	for _, line := range tableLines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	// Status / capping notice.
	statusMsg := renderStatusNotice(th, shown, total, "total "+entity)
	sb.WriteString(statusMsg)
	sb.WriteString("\n")

	if maxResults > 0 && total > maxResults {
		dimSt := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
		remaining := total - shown
		notice := dimSt.Render(renderMoreNotice(remaining))
		sb.WriteString(notice)
		sb.WriteString("\n")
	}

	return sb.String()
}

// renderStatusNotice returns "Showing N of M <noun>".
func renderStatusNotice(th *theme.Theme, shown, total int, noun string) string {
	msg := renderShowingLine(shown, total, noun)
	dimSt := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
	return dimSt.Render(msg)
}

func renderShowingLine(shown, total int, noun string) string {
	return fmt.Sprintf("Showing %d of %d %s", shown, total, noun)
}

func renderMoreNotice(remaining int) string {
	return fmt.Sprintf("%d more results not shown. Use --max-results to see more.", remaining)
}

// RenderResources renders a Forma's resources as a table string for non-TTY output.
func RenderResources(th *theme.Theme, forma *pkgmodel.Forma, maxResults, width int) string {
	if forma == nil {
		return ""
	}
	specs := newSpecs(nil)
	spec := specs[TabResources]

	rows := make([]row, 0, len(forma.Resources))
	for i := range forma.Resources {
		rows = append(rows, resourceRow(forma.Resources[i]))
	}
	return renderTable(th, spec.columns, rows, spec.styleCell, spec.entity, maxResults, width)
}

// RenderTargets renders a list of targets as a table string for non-TTY output.
func RenderTargets(th *theme.Theme, targets []*pkgmodel.Target, maxResults, width int) string {
	specs := newSpecs(nil)
	spec := specs[TabTargets]

	rows := make([]row, 0, len(targets))
	for _, t := range targets {
		rows = append(rows, targetRow(t))
	}
	return renderTable(th, spec.columns, rows, spec.styleCell, spec.entity, maxResults, width)
}

// RenderStacks renders a list of stacks as a table string for non-TTY output.
// now is injected for deterministic output (policy expiry uses it).
func RenderStacks(th *theme.Theme, stacks []*pkgmodel.Stack, now time.Time, maxResults, width int) string {
	specs := newSpecs(nil)
	spec := specs[TabStacks]

	rows := make([]row, 0, len(stacks))
	for _, s := range stacks {
		rows = append(rows, stackRow(s, now))
	}
	return renderTable(th, spec.columns, rows, spec.styleCell, spec.entity, maxResults, width)
}

// RenderPolicies renders a list of policies as a table string for non-TTY output.
func RenderPolicies(th *theme.Theme, policies []apimodel.PolicyInventoryItem, maxResults, width int) string {
	specs := newSpecs(nil)
	spec := specs[TabPolicies]

	rows := make([]row, 0, len(policies))
	for _, p := range policies {
		rows = append(rows, policyRow(p))
	}
	return renderTable(th, spec.columns, rows, spec.styleCell, spec.entity, maxResults, width)
}
