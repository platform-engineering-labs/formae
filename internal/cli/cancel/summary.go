// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cancel

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// preCancelCounts holds the pre-cancel resource state counts derived from a
// Command's ResourceUpdates, used to build expectation bullets before the user
// consents to a force-cancel.
type preCancelCounts struct {
	Completed  int // Success → already done
	InProgress int // InProgress → will be abandoned (force) or will finish (normal)
	Pending    int // Pending/NotStarted → will cancel
	Failed     int // Failed → already done
}

// bucketPreCancel buckets a Command's ResourceUpdates into preCancelCounts.
// This mirrors how renderCancelSummary builds its progress-bar counts, but
// returns plain integers for use in pre-consent expectation bullets.
func bucketPreCancel(c apimodel.Command) preCancelCounts {
	var counts preCancelCounts
	for _, ru := range c.ResourceUpdates {
		switch ru.State {
		case "Success":
			counts.Completed++
		case "Failed":
			counts.Failed++
		case "InProgress":
			counts.InProgress++
		default: // "Pending", "NotStarted", or empty
			counts.Pending++
		}
	}
	return counts
}

// renderPreCancelExpectationLine returns the compact expectation bullet line
// for a single command shown in the force-cancel confirmation panel.
// Example: "  · 8 completed · 2 in progress (will be abandoned) · 5 pending (will cancel)"
func renderPreCancelExpectationLine(th *theme.Theme, counts preCancelCounts) string {
	p := th.Palette
	subtle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	sep := subtle.Render(" · ")

	var parts []string
	if counts.Completed > 0 {
		parts = append(parts, subtle.Render(fmt.Sprintf("%d completed", counts.Completed)))
	}
	if counts.Failed > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(p.Error).Render(fmt.Sprintf("%d failed", counts.Failed)))
	}
	if counts.InProgress > 0 {
		parts = append(parts, subtle.Render(fmt.Sprintf("%d in progress (will be abandoned)", counts.InProgress)))
	}
	if counts.Pending > 0 {
		parts = append(parts, subtle.Render(fmt.Sprintf("%d pending (will cancel)", counts.Pending)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "    " + strings.Join(parts, sep)
}

// ksuidFromURI normalizes a formae URI to just the ksuid portion.
// "formae://2abc#"     → "2abc"
// "formae://x#/prop"  → "x"
// "2abc"              → "2abc" (bare id passes through)
func ksuidFromURI(uri string) string {
	const prefix = "formae://"
	if !strings.HasPrefix(uri, prefix) {
		return uri
	}
	rest := uri[len(prefix):]
	// strip everything from '#' onward
	if idx := strings.IndexByte(rest, '#'); idx >= 0 {
		rest = rest[:idx]
	}
	return rest
}

// expectation holds the bucketed counts for a single command's resources.
type expectation struct {
	Completed  int // Success → already done, not rolled back
	Failed     int // Failed
	WillFinish int // InProgress + !ForceCanceled → will finish before stopping
	Abandoned  int // InProgress+ForceCanceled OR Canceled+ForceCanceled → abandoned mid-op
	WillCancel int // Canceled + !ForceCanceled → pending that will be canceled
}

// cancelExpectations buckets CancelResourceState entries by CommandID.
func cancelExpectations(resp *apimodel.CancelCommandResponse) map[string]expectation {
	result := make(map[string]expectation)
	for _, rs := range resp.ResourceUpdateStates {
		exp := result[rs.CommandID]
		switch {
		case rs.State == "Success":
			exp.Completed++
		case rs.State == "Failed":
			exp.Failed++
		case rs.State == "InProgress" && !rs.ForceCanceled:
			exp.WillFinish++
		case rs.State == "InProgress" && rs.ForceCanceled:
			exp.Abandoned++
		case rs.State == "Canceled" && rs.ForceCanceled:
			exp.Abandoned++
		case rs.State == "Canceled" && !rs.ForceCanceled:
			exp.WillCancel++
		}
		result[rs.CommandID] = exp
	}
	return result
}

// renderCancelSummary renders the styled cancel summary for human output.
// For a single command: shows detailed "What to expect" block.
// For multiple commands: shows a compact per-command summary line.
func renderCancelSummary(th *theme.Theme, cmds []apimodel.Command, exps map[string]expectation, forced bool, now time.Time) string {
	p := th.Palette
	var b strings.Builder

	heading := lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true)
	subtle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	accent := lipgloss.NewStyle().Foreground(p.PrimaryAccent)
	hint := lipgloss.NewStyle().Foreground(p.TextSubtle)
	bullet := subtle.Render("·")

	n := len(cmds)
	if n == 1 {
		fmt.Fprintf(&b, "\n  %s\n\n", heading.Render("Canceling 1 command:"))
	} else {
		fmt.Fprintf(&b, "\n  %s\n\n", heading.Render(fmt.Sprintf("Canceling %d commands:", n)))
	}

	for _, cmd := range cmds {
		exp := exps[cmd.CommandID]
		elapsed := now.Sub(cmd.StartTs)
		elapsedStr := components.FormatDuration(elapsed)

		// Build progress bar counts
		counts := make(map[components.State]int)
		total := 0
		for _, ru := range cmd.ResourceUpdates {
			total++
			switch ru.State {
			case "Success":
				counts[components.StateDone]++
			case "InProgress":
				counts[components.StateInProgress]++
			case "Failed":
				counts[components.StateFailed]++
			case "Canceled":
				counts[components.StateSkipped]++
			default: // "Pending" or empty
				counts[components.StatePending]++
			}
		}
		done := counts[components.StateDone]
		failed := counts[components.StateFailed] > 0 || counts[components.StateSkipped] > 0 ||
			cmd.State == "Failed" || cmd.State == "Canceled"
		bar := components.ProgressBar(th, 10, counts, failed)

		cmdLine := fmt.Sprintf("    %s  %s %s  %s %d/%d  %s",
			accent.Render(cmd.CommandID),
			subtle.Render(cmd.Command),
			subtle.Render(cmd.Mode),
			bar,
			done, total,
			hint.Render(elapsedStr),
		)
		b.WriteString(cmdLine + "\n")

		// For multiple commands, render compact inline expectation line
		if n > 1 {
			b.WriteString(renderExpectationInline(th, exp, forced) + "\n")
		}
		b.WriteString("\n")
	}

	// For single command: render detailed "What to expect" block
	if n == 1 && len(cmds) > 0 {
		exp := exps[cmds[0].CommandID]
		fmt.Fprintf(&b, "  %s\n", heading.Render("What to expect:"))
		if exp.Completed > 0 {
			fmt.Fprintf(&b, "    %s  %d updates already completed — these will not be rolled back\n",
				bullet, exp.Completed)
		}
		if exp.Failed > 0 {
			fmt.Fprintf(&b, "    %s  %d updates failed\n", bullet, exp.Failed)
		}
		if forced {
			if exp.Abandoned > 0 {
				fmt.Fprintf(&b, "    %s  %d updates currently in progress — these will be abandoned\n",
					bullet, exp.Abandoned)
			}
		} else {
			if exp.WillFinish > 0 {
				fmt.Fprintf(&b, "    %s  %d updates currently in progress — these will finish before stopping\n",
					bullet, exp.WillFinish)
			}
		}
		if exp.WillCancel > 0 {
			fmt.Fprintf(&b, "    %s  %d updates pending — these will be canceled\n",
				bullet, exp.WillCancel)
		}
		b.WriteString("\n")
	}

	// Watch hints
	fmt.Fprintf(&b, "  %s\n", heading.Render("To watch the cancellation progress:"))
	for _, cmd := range cmds {
		query := fmt.Sprintf("id:%s", cmd.CommandID)
		watchSuffix := ""
		if n == 1 {
			watchSuffix = " --watch"
		}
		fmt.Fprintf(&b, "    %s\n",
			hint.Render(fmt.Sprintf("formae status command --query='%s'%s", query, watchSuffix)))
	}

	return b.String()
}

// renderExpectationInline renders a compact one-line expectation summary
// for multi-command display.
func renderExpectationInline(th *theme.Theme, exp expectation, forced bool) string {
	p := th.Palette
	subtle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	sep := subtle.Render(" · ")

	var parts []string
	if exp.Completed > 0 {
		parts = append(parts, subtle.Render(fmt.Sprintf("%d completed", exp.Completed)))
	}
	if exp.Failed > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(p.Error).Render(fmt.Sprintf("%d failed", exp.Failed)))
	}
	if forced {
		if exp.Abandoned > 0 {
			parts = append(parts, subtle.Render(fmt.Sprintf("%d in progress (will be abandoned)", exp.Abandoned)))
		}
	} else {
		if exp.WillFinish > 0 {
			parts = append(parts, subtle.Render(fmt.Sprintf("%d in progress (will finish)", exp.WillFinish)))
		}
	}
	if exp.WillCancel > 0 {
		parts = append(parts, subtle.Render(fmt.Sprintf("%d pending (will cancel)", exp.WillCancel)))
	}

	if len(parts) == 0 {
		return ""
	}
	return "      " + strings.Join(parts, sep)
}

// confirmForceCancel is a seam for the force cancellation confirmation prompt.
// It renders the destructive-escape-hatch warning and asks for confirmation.
var confirmForceCancel = func(th *theme.Theme, summary string) (bool, error) {
	lines := []string{
		"--force is a destructive escape hatch. It abandons in-progress work",
		"and drives the command to a terminal 'Canceled' state immediately,",
		"instead of waiting for in-progress resources to finish.",
		"",
		"Cloud-side operations may continue:",
		"  ·  in-flight Update/Delete operations are reconciled by the",
		"     synchronizer on its next pass",
		"  ·  a still-running Create may orphan a resource that needs",
		"     manual cleanup",
	}
	if summary != "" {
		lines = append(lines, "")
		lines = append(lines, strings.Split(summary, "\n")...)
	}

	panel := components.Panel(th, th.Palette.Warning, "Warning: --force", lines, 80)
	fmt.Println(panel)

	return components.RunConfirm(th, "Force-cancel anyway?", "")
}
