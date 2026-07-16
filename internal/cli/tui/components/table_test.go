// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func testColumns() []Column {
	return []Column{
		{Title: "Label", Width: 20, Priority: 0},
		{Title: "Type", Width: 24, Priority: 1},
		{Title: "Stack", Width: 14, Priority: 2},
		{Title: "State", Width: 10, Priority: 3},
	}
}

func testRows() [][]string {
	return [][]string{
		{"web-1", "AWS::EC2::Instance", "production", "Done"},
		{"api-db", "AWS::RDS::DBInstance", "production", "Failed"},
		{"my-bucket", "AWS::S3::Bucket", "staging", "Done"},
	}
}

func newTestTable(width int) Table {
	th := theme.New("formae")
	tbl := NewTable(th, testColumns())
	tbl = tbl.SetRows(testRows())
	return tbl.SetSize(width, 10)
}

func TestTable_AllColumnsWhenWide(t *testing.T) {
	view := ansi.Strip(newTestTable(120).View())
	for _, title := range []string{"Label", "Type", "Stack", "State"} {
		assert.Contains(t, view, title)
	}
}

func TestTable_DropsLowPriorityColumnsWhenNarrow(t *testing.T) {
	// 40 cells: only Label (always) and one more column can fit
	view := ansi.Strip(newTestTable(40).View())
	assert.Contains(t, view, "Label")
	assert.NotContains(t, view, "State") // Priority 3 dropped first
	assert.NotContains(t, view, "Stack") // then Priority 2
}

func TestTable_SelectedRowReturnsFullRowEvenWhenColumnsHidden(t *testing.T) {
	tbl := newTestTable(40)
	tbl, _ = tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	assert.Equal(t, []string{"api-db", "AWS::RDS::DBInstance", "production", "Failed"}, tbl.SelectedRow())
}

func TestTable_SortByColumn(t *testing.T) {
	tbl := newTestTable(120).SortBy(0, SortAsc)
	assert.Equal(t, []string{"api-db", "AWS::RDS::DBInstance", "production", "Failed"}, tbl.SelectedRow())

	view := ansi.Strip(tbl.View())
	assert.Contains(t, view, "Label ▲")

	tbl = tbl.SortBy(0, SortDesc)
	assert.Contains(t, ansi.Strip(tbl.View()), "Label ▼")
	assert.Equal(t, []string{"web-1", "AWS::EC2::Instance", "production", "Done"}, tbl.SelectedRow())
}

func TestTable_CursorNavigation(t *testing.T) {
	tbl := newTestTable(120)
	assert.Equal(t, 0, tbl.Cursor())
	tbl, _ = tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	tbl, _ = tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	assert.Equal(t, 2, tbl.Cursor())
	tbl, _ = tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	assert.Equal(t, 1, tbl.Cursor())
}

func TestTable_Golden(t *testing.T) {
	tuitest.RequireGolden(t, []byte(newTestTable(80).View()))
}
