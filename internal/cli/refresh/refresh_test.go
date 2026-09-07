// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package refresh

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func assertNoSpinner(t *testing.T, out string) {
	t.Helper()
	assert.NotContains(t, out, "\r", "piped output must not carry carriage returns")
	assert.NotContains(t, out, "\x1b[", "piped output must be ANSI-free")
	for _, f := range spinnerFrames {
		assert.NotContains(t, out, f, "piped output must not contain spinner frames")
	}
}

// TestRunRefresh_PipedSuccess verifies the piped (non-TTY) happy path: one ✓
// result line per channel, a final ✓ ack, no ANSI, no spinner frames.
func TestRunRefresh_PipedSuccess(t *testing.T) {
	var buf bytes.Buffer
	err := runRefresh(&buf, theme.New("formae"), []string{"stable", "dev"},
		func(string) (int, error) { return 0, nil })
	require.NoError(t, err)

	out := buf.String()
	assertNoSpinner(t, out)
	assert.Contains(t, out, "✓ refreshed stable")
	assert.Contains(t, out, "✓ refreshed dev")
	assert.Contains(t, out, "✓ package index up to date")
}

// TestRunRefresh_ChannelErrorFlipsToFail verifies a hard error on a channel
// emits ✗, returns the error, and suppresses the final success ack.
func TestRunRefresh_ChannelErrorFlipsToFail(t *testing.T) {
	var buf bytes.Buffer
	boom := errors.New("no managed installation root detected")
	err := runRefresh(&buf, theme.New("formae"), []string{"stable", "dev"},
		func(channel string) (int, error) {
			if channel == "dev" {
				return 0, boom
			}
			return 0, nil
		})
	require.ErrorIs(t, err, boom)

	out := buf.String()
	assert.Contains(t, out, "✓ refreshed stable")
	assert.Contains(t, out, "✗")
	assert.Contains(t, out, "failed to refresh dev")
	assert.NotContains(t, out, "package index up to date")
}

// TestRunRefresh_RepositoryErrorsWarn verifies that repository errors logged
// during a channel's refresh surface as a per-channel ! warning and an
// aggregate returned error, without a false success ack.
func TestRunRefresh_RepositoryErrorsWarn(t *testing.T) {
	var buf bytes.Buffer
	err := runRefresh(&buf, theme.New("formae"), []string{"stable", "dev"},
		func(channel string) (int, error) {
			if channel == "stable" {
				return 2, nil
			}
			return 0, nil
		})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 repository error(s)")

	out := buf.String()
	assert.Contains(t, out, "! refreshed stable (2 repository error(s))")
	assert.Contains(t, out, "✓ refreshed dev")
	assert.NotContains(t, out, "package index up to date")
}

// orbital's Refresh() logs per-repository fetch/validation failures at ERROR
// level but always returns nil. refreshErrorCounter must count exactly those
// ERROR records (not INFO/WARN) so refresh can report a failure instead of a
// false "done.".
func TestRefreshErrorCounter_CountsOnlyErrorRecords(t *testing.T) {
	var n atomic.Int64
	log := slog.New(refreshErrorCounter{Handler: slog.NewTextHandler(io.Discard, nil), count: &n})

	log.Info("refreshing pel#stable")
	log.Warn("no metadata: community#stable")
	log.Error("refresh failed: pel#stable")
	log.Error("metadata validation failed: community#stable")

	if got := n.Load(); got != 2 {
		t.Errorf("expected 2 ERROR records counted, got %d", got)
	}
}
