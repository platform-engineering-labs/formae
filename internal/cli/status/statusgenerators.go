// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package status

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// noRotationNote is the positive statement a command makes when generators were
// in scope and none of them drew a value.
//
// Silence is not an answer here. Rotation is unattended credential mutation, so
// "did this command turn a live secret over" is the question a reader opens the
// status for, and a surface that is silent both when nothing rotated and when
// something did answers it for neither.
//
// It is final from submission onward, not only once the command is done: the
// set of draws is decided when the command is planned, so a command that
// carries no draw entry will never produce one.
const noRotationNote = "no generator rotated in this command"

// drawOutcomeNote explains where a draw's outcome is reported.
//
// A draw writes no generator row, so no store holds its result and a status read
// has nothing to project (see apimodel.GeneratorUpdate). Its State field still
// carries the plan's value, which is why the state column is left empty for a
// draw rather than rendering a stale "NotStarted" as though it were an outcome.
const drawOutcomeNote = "a draw's outcome is reported on the resources it feeds"

// generatorStateNotTracked is the state cell for a draw.
const generatorStateNotTracked = "—"

// renderGeneratorSection renders one command's generator work: a section
// header, a column header, one row per generator update, and the rotation note.
// It returns "" for a command with no generator work at all — the rotation
// statement is for commands where generators are demonstrably in scope, not for
// every apply of an S3 bucket.
//
// The generator's config is deliberately not rendered. It is the declared spec,
// which the plan and simulate views already show, and every field of it that a
// status reader could want is already in the four columns here.
func renderGeneratorSection(th *theme.Theme, c apimodel.Command, width int) string {
	if len(c.GeneratorUpdates) == 0 {
		return ""
	}

	p := th.Palette
	dimSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
	headSt := lipgloss.NewStyle().Foreground(p.TextSecondary).Bold(true)

	labelW, typeW, stackW, opW, stateW := generatorColWidths(width)

	var sb strings.Builder
	sb.WriteString("\n  ")
	sb.WriteString(components.SectionHeader(th, "Generators"))
	sb.WriteString("\n")
	sb.WriteString("  ")
	sb.WriteString(headSt.Render(strings.Join([]string{
		components.Pad("LABEL", labelW),
		components.Pad("TYPE", typeW),
		components.Pad("STACK", stackW),
		components.Pad("OP", opW),
		components.Pad("STATE", stateW),
		"DURATION",
	}, "  ")))
	sb.WriteString("\n")

	drew := false
	for _, gu := range c.GeneratorUpdates {
		if gu.Operation == apimodel.OperationDraw {
			drew = true
		}
		sb.WriteString(renderGeneratorRow(th, gu, labelW, typeW, stackW, opW, stateW))
	}

	note := noRotationNote
	if drew {
		note = drawOutcomeNote
	}
	sb.WriteString("  ")
	sb.WriteString(dimSt.Render(note))
	sb.WriteString("\n")

	return sb.String()
}

// generatorColWidths splits the available width across the five padded columns.
// The duration column is last and unpadded, so it needs no budget.
func generatorColWidths(width int) (labelW, typeW, stackW, opW, stateW int) {
	labelW, typeW, stackW, opW, stateW = 24, 12, 16, 8, 10
	// Narrow terminals shrink the two widest columns first; the operation and
	// the state are what the section exists to report and stay legible.
	for labelW > 8 && labelW+typeW+stackW+opW+stateW+20 > width {
		labelW--
		if stackW > 8 {
			stackW--
		}
	}
	return labelW, typeW, stackW, opW, stateW
}

// renderGeneratorRow renders one generator update as a single line.
//
// The state cell is empty for a draw: a draw's State carries the plan's value
// and never advances, so rendering it would report an outcome that does not
// exist. drawOutcomeNote says where the real one is.
func renderGeneratorRow(
	th *theme.Theme,
	gu apimodel.GeneratorUpdate,
	labelW, typeW, stackW, opW, stateW int,
) string {
	p := th.Palette
	textSt := lipgloss.NewStyle().Foreground(p.TextPrimary)
	dimSt := lipgloss.NewStyle().Foreground(p.TextSubtle)

	glyph, glyphSt := generatorStateGlyph(th, gu)
	opSt := lipgloss.NewStyle().Foreground(components.OperationColor(p, gu.Operation))

	state := gu.State
	stateSt := glyphSt
	if gu.Operation == apimodel.OperationDraw {
		state = generatorStateNotTracked
		stateSt = dimSt
	}

	var sb strings.Builder
	sb.WriteString("  ")
	sb.WriteString(glyphSt.Render(components.Pad(glyph, 2)))
	sb.WriteString(textSt.Render(components.Pad(components.Truncate(gu.GeneratorLabel, labelW-2), labelW-2)))
	sb.WriteString("  ")
	sb.WriteString(dimSt.Render(components.Pad(components.Truncate(gu.GeneratorType, typeW), typeW)))
	sb.WriteString("  ")
	sb.WriteString(dimSt.Render(components.Pad(components.Truncate(gu.StackLabel, stackW), stackW)))
	sb.WriteString("  ")
	sb.WriteString(opSt.Render(components.Pad(gu.Operation, opW)))
	sb.WriteString("  ")
	sb.WriteString(stateSt.Render(components.Pad(state, stateW)))
	sb.WriteString("  ")
	sb.WriteString(dimSt.Render(components.FormatDuration(time.Duration(gu.Duration) * time.Millisecond)))
	sb.WriteString("\n")

	if gu.ErrorMessage != "" {
		errSt := lipgloss.NewStyle().Foreground(p.Error)
		_, _ = fmt.Fprintf(&sb, "      %s\n", errSt.Render(gu.ErrorMessage))
	}

	return sb.String()
}

// generatorStateGlyph returns the themed glyph and style for a generator
// update's state. A draw gets no glyph: it reports no outcome of its own.
func generatorStateGlyph(th *theme.Theme, gu apimodel.GeneratorUpdate) (string, lipgloss.Style) {
	p := th.Palette
	if gu.Operation == apimodel.OperationDraw {
		return " ", lipgloss.NewStyle().Foreground(p.TextSubtle)
	}
	switch gu.State {
	case "Success":
		return th.Glyphs.StatusDone, lipgloss.NewStyle().Foreground(p.Done)
	case "Failed":
		return th.Glyphs.StatusFailed, lipgloss.NewStyle().Foreground(p.Error)
	case "Canceled":
		return th.Glyphs.StatusCanceled, lipgloss.NewStyle().Foreground(p.TextSubtle)
	default:
		return th.Glyphs.StatusInProgress, lipgloss.NewStyle().Foreground(p.InProgress)
	}
}
