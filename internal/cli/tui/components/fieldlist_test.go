// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	"github.com/stretchr/testify/assert"
)

func TestFieldList_Golden(t *testing.T) {
	th := theme.New("formae")
	out := FieldList(th, [][2]string{
		{"Type", "resource"},
		{"Namespace", "CLOUDFLARE"},
		{"Publisher", "Acme Corp"},
	})
	tuitest.RequireGolden(t, []byte(out))
}

func TestFieldList_AlignsValuesAcrossRows(t *testing.T) {
	th := theme.New("formae")
	out := FieldList(th, [][2]string{{"A", "v1"}, {"Longer", "v2"}})
	lines := strings.Split(out, "\n")
	// Values start at the same display column on every line.
	assert.Equal(t, lipgloss.Width(lines[0]), lipgloss.Width(lines[1]))
}
