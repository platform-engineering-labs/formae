// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// benchClient seeds n bucket resources plus one unmanaged row (so per-cell
// styling is exercised).
func benchClient(n int) *fakeClient {
	res := make([]pkgmodel.Resource, 0, n+1)
	for i := range n {
		res = append(res, pkgmodel.Resource{
			NativeID: fmt.Sprintf("arn:aws:s3:::bucket-%04d", i),
			Stack:    "production",
			Type:     "AWS::S3::Bucket",
			Label:    fmt.Sprintf("bucket-%04d", i),
		})
	}
	res = append(res, pkgmodel.Resource{
		NativeID: "arn:aws:s3:::old-logs",
		Stack:    "$unmanaged",
		Type:     "AWS::S3::Bucket",
		Label:    "aaa-old-logs", // sorts first, so it stays on screen
	})
	return &fakeClient{forma: &pkgmodel.Forma{Resources: res}}
}

func navModel(tb testing.TB, n int) tea.Model {
	tb.Helper()
	m := New(theme.New("formae"), benchClient(n), Options{
		MaxRows: 200,
		Now:     func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	})
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mm, _ = runInit(mm)
	return mm
}

// TestNav_MovesCursorAndKeepsStyling guards delegateNav's dropped sync(): a
// cursor move must still move the cursor and must not lose the per-cell styling
// (the "⚠ unmanaged" stack cell) that sync recorded for the current row set.
func TestNav_MovesCursorAndKeepsStyling(t *testing.T) {
	mm := navModel(t, 50)
	before := mm.(Model)
	require.Equal(t, 0, before.tabs[before.active].table.Cursor())
	require.Contains(t, mm.View(), "⚠ unmanaged")

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	after := mm.(Model)

	assert.Equal(t, 1, after.tabs[after.active].table.Cursor())
	// Rows are untouched by navigation, styling included.
	assert.Contains(t, mm.View(), "⚠ unmanaged")
	assert.Equal(t,
		len(before.tabs[before.active].styledCells),
		len(after.tabs[after.active].styledCells))
	assert.Equal(t, strings.Count(before.View(), "bucket-"), strings.Count(mm.View(), "bucket-"))
}

// BenchmarkScrollKey measures one arrow-down keypress (Update + View) — the work
// a single mouse-wheel step costs. Terminals expand one wheel notch into
// several arrow keys, so this must stay well under a frame (~16ms) or the input
// queue outruns the renderer and the list keeps scrolling after the user stops.
func BenchmarkScrollKey(b *testing.B) {
	for _, n := range []int{200, 1000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			mm := navModel(b, n)
			key := tea.KeyMsg{Type: tea.KeyDown}
			b.ResetTimer()
			for range b.N {
				mm, _ = mm.Update(key)
				_ = mm.View()
			}
		})
	}
}

// TestWheel_Coalesces pins the wheel contract: notches accumulate travel, the
// settle tick applies the total in one move, and non-wheel messages flush the
// pending travel first so they act on the scrolled-to position.
func TestWheel_Coalesces(t *testing.T) {
	mm := navModel(t, 50)
	down := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}
	up := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}

	// A burst of 5 notches: nothing moves yet, and only the first arms a tick.
	var cmd tea.Cmd
	mm, cmd = mm.Update(down)
	require.NotNil(t, cmd, "first notch arms the settle tick")
	for range 4 {
		var c tea.Cmd
		mm, c = mm.Update(down)
		require.Nil(t, c, "further notches ride the armed tick")
	}
	m := mm.(Model)
	require.Equal(t, 0, m.tabs[m.active].table.Cursor(), "travel is deferred")
	require.Equal(t, 5*wheelStep, m.wheelPending)

	// The settle tick applies the whole burst at once.
	mm, _ = mm.Update(cmd())
	m = mm.(Model)
	assert.Equal(t, 5*wheelStep, m.tabs[m.active].table.Cursor())
	assert.Equal(t, 0, m.wheelPending)

	// Upward notches subtract, and any other message flushes them.
	mm, _ = mm.Update(up)
	mm, _ = mm.Update(up)
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mm.(Model)
	assert.Equal(t, 0, m.wheelPending)
	// 5 down, 2 up, then one arrow-key down.
	assert.Equal(t, 5*wheelStep-2*wheelStep+1, m.tabs[m.active].table.Cursor())

	// Release events are not moves.
	mm, _ = mm.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonWheelDown})
	assert.Equal(t, 0, mm.(Model).wheelPending)
}

// TestWheel_CachedFrameWhilePending checks the mid-burst frame reuse: View must
// hand back the previous paint while travel is pending, then repaint after the
// settle tick.
func TestWheel_CachedFrameWhilePending(t *testing.T) {
	mm := navModel(t, 50)
	first := mm.View()
	require.NotEmpty(t, first)

	mm, cmd := mm.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	assert.Equal(t, first, mm.View(), "pending burst reuses the cached frame")

	mm, _ = mm.Update(cmd())
	assert.NotEqual(t, first, mm.View(), "settled burst repaints")
}

// BenchmarkWheelNotch measures one queued wheel notch mid-burst: the per-event
// cost that has to stay far below the interval between notches, or the list
// keeps scrolling after the user stops.
func BenchmarkWheelNotch(b *testing.B) {
	mm := navModel(b, 1000)
	msg := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}
	b.ResetTimer()
	for range b.N {
		mm, _ = mm.Update(msg)
		_ = mm.View()
	}
}

// BenchmarkWheelSettle measures the settle tick: one move plus one real repaint,
// paid once per burst rather than once per notch.
func BenchmarkWheelSettle(b *testing.B) {
	mm := navModel(b, 1000)
	notch := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}
	b.ResetTimer()
	for range b.N {
		mm, _ = mm.Update(notch)
		mm, _ = mm.Update(wheelSettleMsg{})
		_ = mm.View()
	}
}

// TestScroll_SelectionStaysVisible guards the scroll-follow contract: however
// far the list has travelled, the selected row is in the rendered frame. It used
// to leave the frame after roughly one screen, because the render path resized
// the table every frame and the resize rebuilt the wrapped table's rows, which
// reset its scroll offset while keeping the cursor.
func TestScroll_SelectionStaysVisible(t *testing.T) {
	for _, downs := range []int{5, 40, 150} {
		mm := navModel(t, 200)
		for range downs {
			mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
		}
		m := mm.(Model)
		sel := m.tabs[m.active].table.SelectedRow()[0]
		require.Equal(t, downs, m.tabs[m.active].table.Cursor())
		assert.Contains(t, mm.View(), sel, "selected row must be on screen after %d downs", downs)
	}

	// Same via the wheel, whose travel lands in one move on settle.
	mm := navModel(t, 200)
	for range 60 {
		mm, _ = mm.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	}
	mm, _ = mm.Update(wheelSettleMsg{})
	m := mm.(Model)
	assert.Contains(t, mm.View(), m.tabs[m.active].table.SelectedRow()[0])
}
