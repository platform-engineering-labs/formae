// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func cmdFix(id, cmdType, state string, started time.Time, failed int) apimodel.Command {
	c := apimodel.Command{CommandID: id, Command: cmdType, State: state, StartTs: started}
	for i := 0; i < failed; i++ {
		c.ResourceUpdates = append(c.ResourceUpdates, apimodel.ResourceUpdate{State: "Failed"})
	}
	c.ResourceUpdates = append(c.ResourceUpdates, apimodel.ResourceUpdate{State: "Success"})
	return c
}

func TestSortRows_DefaultUrgency(t *testing.T) {
	now := time.Now()
	rows := buildRows([]apimodel.Command{
		cmdFix("finished-ok", "apply", "Success", now, 0),
		cmdFix("running-failing", "apply", "InProgress", now, 1),
		cmdFix("finished-failed", "apply", "Failed", now, 1),
		cmdFix("running-healthy", "apply", "InProgress", now, 0),
	})
	sortRows(rows, colStatus, components.SortAsc, now)
	got := []string{rows[0].cmd.CommandID, rows[1].cmd.CommandID, rows[2].cmd.CommandID, rows[3].cmd.CommandID}
	assert.Equal(t, []string{"running-failing", "running-healthy", "finished-failed", "finished-ok"}, got)
}

func TestSortRows_ByAgeUsesRealTimestamp(t *testing.T) {
	now := time.Now()
	rows := buildRows([]apimodel.Command{
		cmdFix("older", "apply", "Success", now.Add(-2*time.Hour), 0),
		cmdFix("newer", "apply", "Success", now.Add(-1*time.Hour), 0),
	})
	sortRows(rows, colAge, components.SortDesc, now) // newest first
	assert.Equal(t, "newer", rows[0].cmd.CommandID)
	sortRows(rows, colAge, components.SortAsc, now)
	assert.Equal(t, "older", rows[0].cmd.CommandID)
}

func TestVisibleColumns_DropTiers(t *testing.T) {
	wide := visibleColumns(120)
	for c := 0; c < colCount; c++ {
		assert.True(t, wide[c], "wide terminal shows all columns")
	}

	medium := visibleColumns(80)
	assert.False(t, medium[colMode])
	assert.False(t, medium[colInProg])
	assert.False(t, medium[colPending])
	assert.False(t, medium[colAge])
	assert.True(t, medium[colCommand])

	narrow := visibleColumns(56)
	assert.False(t, narrow[colCommand])
	for _, c := range []int{colStatus, colID, colProgress, colDone, colFailed, colTime} {
		assert.True(t, narrow[c], "always-on column %d", c)
	}
}

func TestBarWidth_ElasticWithFloor(t *testing.T) {
	vis := visibleColumns(120)
	assert.GreaterOrEqual(t, barWidth(120, vis), 10)
	assert.Greater(t, barWidth(160, vis), barWidth(120, vis))
	assert.Equal(t, 10, barWidth(40, visibleColumns(40))) // floor
}
