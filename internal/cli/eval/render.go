// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package eval

import (
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// renderEvalHeader renders the "Evaluating forma" block shown before the
// serialized output on the human path.
func renderEvalHeader(th *theme.Theme, file, mode string) string {
	return components.SectionHeader(th, "Evaluating forma") + "\n" +
		components.Indent(components.FieldList(th, [][2]string{
			{"File", file},
			{"Mode", mode},
		}), 2)
}
