// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cancel

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// defaultCancelPrintWidth is the fallback width for non-TTY cancel output.
const defaultCancelPrintWidth = 100

// cancelTermWidth returns the terminal width for cancel non-TTY human output.
// Returns the real TTY width on a real terminal, or defaultCancelPrintWidth otherwise.
func cancelTermWidth(w io.Writer) int {
	if !tui.IsTerminal(w) {
		return defaultCancelPrintWidth
	}
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return defaultCancelPrintWidth
}

// renderCancelResult renders the styled non-TTY cancel result for human output.
// It covers:
//   - empty response (no commands canceled)
//   - list of canceled command IDs
//   - force-canceled resource list and warning footer (when Forced)
//   - status hint footer
func renderCancelResult(th *theme.Theme, resp *apimodel.CancelCommandResponse, _ int) string {
	p := th.Palette
	heading := lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true)
	accent := lipgloss.NewStyle().Foreground(p.PrimaryAccent)
	subtle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	warn := lipgloss.NewStyle().Foreground(p.Warning)
	hint := lipgloss.NewStyle().Foreground(p.TextSubtle)

	var b strings.Builder

	if resp == nil || len(resp.CommandIDs) == 0 {
		fmt.Fprintf(&b, "\n  %s\n", heading.Render("No commands to cancel."))
		return b.String()
	}

	// Header
	if resp.Forced {
		fmt.Fprintf(&b, "\n  %s\n\n", heading.Render("Commands have been force-canceled:"))
	} else {
		fmt.Fprintf(&b, "\n  %s\n\n", heading.Render("Commands are being canceled:"))
	}

	// Command IDs
	bullet := subtle.Render("•")
	for _, cmdID := range resp.CommandIDs {
		fmt.Fprintf(&b, "    %s %s\n", bullet, accent.Render(cmdID))
	}

	// Force-canceled resources
	if resp.Forced {
		var abandoned []string
		for uri, rs := range resp.ResourceUpdateStates {
			if rs.ForceCanceled {
				abandoned = append(abandoned, uri)
			}
		}
		sort.Strings(abandoned)

		if len(abandoned) > 0 {
			fmt.Fprintf(&b, "\n  %s\n", warn.Render("The following resources were abandoned mid-operation and may exist in your cloud provider:"))
			for _, uri := range abandoned {
				fmt.Fprintf(&b, "    %s %s\n", warn.Render("⚠"), uri)
			}
		}

		fmt.Fprintf(&b, "\n  %s\n", hint.Render("Force-cancel abandons in-progress work; cloud-side operations may still be running."))
		fmt.Fprintf(&b, "  %s\n", hint.Render("Update/Delete operations are reconciled by the synchronizer on its next cycle."))
		fmt.Fprintf(&b, "  %s\n", hint.Render("A still-running Create may orphan a resource: verify the resources above in your"))
		fmt.Fprintf(&b, "  %s\n", hint.Render("cloud provider and clean up manually, or let discovery pick them up."))
	}

	// Status hint footer
	fmt.Fprintf(&b, "\n  %s\n", hint.Render("Use 'formae status' to check the cancellation progress."))

	return b.String()
}
