// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"strings"
	"time"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// RenderSummaryTable renders the multi-command summary table (VIEW 1 layout)
// as a plain string suitable for non-TTY output. The table uses the multiView
// renderer so the column set, progress bar and status glyphs are identical to
// the TUI.
//
// now is used to compute elapsed/age columns; pass time.Now() from call sites.
func RenderSummaryTable(th *theme.Theme, cmds []apimodel.Command, width int, now time.Time) string {
	rows := buildRows(cmds)
	if len(rows) == 0 {
		return "(no commands)\n"
	}
	mv := multiView{
		th:       th,
		rows:     rows,
		cursor:   -1, // no cursor selection in non-TTY output
		sortHi:   -1,
		width:    width,
		spinView: th.Spinner.StaticFrame, // static spinner frame for non-TTY
		now:      now,
	}
	header := mv.headerRow()
	dataRows := mv.renderRows(len(rows))
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n")
	for _, r := range dataRows {
		sb.WriteString(r)
		sb.WriteString("\n")
	}
	return sb.String()
}

// RenderDetailTable renders the per-resource detail view for a single command
// as a plain string suitable for non-TTY output. Each group (Targets, Stacks,
// Policies, Resources) is rendered as a section with a column header and rows.
//
// now is used to compute elapsed columns; pass time.Now() from call sites.
func RenderDetailTable(th *theme.Theme, c apimodel.Command, width int, now time.Time) string {
	counts := commandCounts(c)
	r := row{cmd: c, counts: counts, health: commandHealth(c, counts)}
	dm := newDetailModel(th, width, 9999) // large height — viewport not used in non-TTY
	dm = dm.SetCommand(c, r, th.Spinner.StaticFrame, now, nil)

	var sb strings.Builder
	for _, g := range dm.groups {
		labelW, typeW, stackW := groupLayout(g.kind, width)
		sb.WriteString("\n  ")
		sb.WriteString(components.SectionHeader(th, g.title))
		sb.WriteString("\n")
		sb.WriteString(dm.renderGroupColHeader(g.kind, labelW, typeW, stackW))
		sb.WriteString("\n")
		for _, ur := range g.rows {
			sb.WriteString(dm.renderSummaryRow(ur, g.kind, labelW, typeW, stackW, false))
		}
	}
	return sb.String()
}
