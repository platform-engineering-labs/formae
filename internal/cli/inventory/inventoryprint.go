// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventory

import (
	"io"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	tui "github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/inventoryview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// defaultPrintWidth is the fallback width used for non-TTY output when no TTY
// width can be detected (matches status package convention).
const defaultPrintWidth = 100

// inventoryTermWidth returns the terminal width for inventory non-TTY output.
// Returns the real TTY width on a real terminal, or defaultPrintWidth otherwise.
func inventoryTermWidth(w io.Writer) int {
	if !tui.IsTerminal(w) {
		return defaultPrintWidth
	}
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return defaultPrintWidth
}

// renderInventoryResources renders the resources table for non-TTY human output.
// It delegates to inventoryview.RenderResources which reuses the TUI row builders
// and components.Table for consistent column layout.
func renderInventoryResources(th *theme.Theme, forma *pkgmodel.Forma, maxResults, width int) string {
	return inventoryview.RenderResources(th, forma, maxResults, width)
}

// renderInventoryTargets renders the targets table for non-TTY human output.
func renderInventoryTargets(th *theme.Theme, targets []*pkgmodel.Target, maxResults, width int) string {
	return inventoryview.RenderTargets(th, targets, maxResults, width)
}

// renderInventoryStacks renders the stacks table for non-TTY human output.
// now is injected so callers can pass time.Now() (or a fixed time in tests).
func renderInventoryStacks(th *theme.Theme, stacks []*pkgmodel.Stack, now time.Time, maxResults, width int) string {
	return inventoryview.RenderStacks(th, stacks, now, maxResults, width)
}

// renderInventoryPolicies renders the policies table for non-TTY human output.
func renderInventoryPolicies(th *theme.Theme, policies []apimodel.PolicyInventoryItem, maxResults, width int) string {
	return inventoryview.RenderPolicies(th, policies, maxResults, width)
}
