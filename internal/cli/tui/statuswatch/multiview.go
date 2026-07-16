// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"sort"
	"time"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// row is a multi-command table row: command + health + resource counts.
type row struct {
	cmd    apimodel.Command
	counts map[components.State]int
	health health
}

// Column indexes for the multi-command table.
const (
	colStatus = iota
	colID
	colCommand
	colMode
	colProgress
	colDone
	colFailed
	colInProg
	colPending
	colTime
	colAge
	colCount
)

// colSpec describes a column: title, width, drop priority, sortability.
type colSpec struct {
	title    string
	width    int // fixed rendered width incl. separating gap; colProgress is elastic
	priority int // 0 always visible; higher drops first as the terminal narrows
	sortable bool
}

// multiCols defines the column specifications.
// Widths follow the prototype's fixed layout (sym 4 + gap, ID 14, Command 9,
// Mode 11, counts 4 each, Time 7, Age 5); Progress is elastic with a floor
// of 10 cells.
var multiCols = [colCount]colSpec{
	colStatus:   {"", 5, 0, true},
	colID:       {"ID", 15, 0, true},
	colCommand:  {"Command", 10, 1, true},
	colMode:     {"Mode", 12, 2, true},
	colProgress: {"Progress", 0, 0, true},
	colDone:     {"✓", 5, 0, false},
	colFailed:   {"✗", 5, 0, false},
	colInProg:   {"◐", 5, 2, false},
	colPending:  {"○", 5, 2, false},
	colTime:     {"Time", 8, 0, true},
	colAge:      {"Age", 6, 2, true},
}

const minBarWidth = 10

// buildRows constructs a row for each command, computing counts and health.
func buildRows(cmds []apimodel.Command) []row {
	rows := make([]row, 0, len(cmds))
	for _, c := range cmds {
		counts := commandCounts(c)
		rows = append(rows, row{cmd: c, counts: counts, health: commandHealth(c, counts)})
	}
	return rows
}

// visibleColumns drops priority-2 columns first (Mode, ◐, ○, Age), then
// priority-1 (Command), until the fixed columns plus the bar floor fit.
func visibleColumns(termWidth int) map[int]bool {
	vis := make(map[int]bool, colCount)
	for c := 0; c < colCount; c++ {
		vis[c] = true
	}
	for _, dropTier := range []int{2, 1} {
		if fixedWidth(vis)+minBarWidth > termWidth {
			for c := 0; c < colCount; c++ {
				if multiCols[c].priority == dropTier {
					vis[c] = false
				}
			}
		}
	}
	return vis
}

// fixedWidth computes the total width of non-elastic columns.
func fixedWidth(vis map[int]bool) int {
	w := 0
	for c := 0; c < colCount; c++ {
		if vis[c] && c != colProgress {
			w += multiCols[c].width
		}
	}
	return w
}

// barWidth returns the width available for the progress bar, with a floor of minBarWidth.
func barWidth(termWidth int, vis map[int]bool) int {
	bw := termWidth - fixedWidth(vis)
	if bw < minBarWidth {
		return minBarWidth
	}
	return bw
}

// sortableColumns returns the column indexes that support sorting.
// Consumed by Tasks 8/9 for sort navigation.
//
//nolint:unused
func sortableColumns() []int {
	cols := make([]int, 0, colCount)
	for c := 0; c < colCount; c++ {
		if multiCols[c].sortable {
			cols = append(cols, c)
		}
	}
	return cols
}

// lessRows compares two rows for a given column. Returns true if row a
// should sort before row b.
func lessRows(a, b row, col int, now time.Time) bool {
	switch col {
	case colStatus:
		return a.health < b.health
	case colID:
		return a.cmd.CommandID < b.cmd.CommandID
	case colCommand:
		return a.cmd.Command < b.cmd.Command
	case colMode:
		return a.cmd.Mode < b.cmd.Mode
	case colProgress:
		return progressFraction(a.counts) < progressFraction(b.counts)
	case colTime:
		return commandDuration(a.cmd, now) < commandDuration(b.cmd, now)
	case colAge:
		return a.cmd.StartTs.Before(b.cmd.StartTs)
	}
	return false
}

// sortRows sorts the rows in-place by the given column and direction.
func sortRows(rows []row, col int, dir components.SortDirection, now time.Time) {
	sort.SliceStable(rows, func(i, j int) bool {
		if dir == components.SortDesc && col != colStatus {
			return lessRows(rows[j], rows[i], col, now)
		}
		return lessRows(rows[i], rows[j], col, now)
	})
}
