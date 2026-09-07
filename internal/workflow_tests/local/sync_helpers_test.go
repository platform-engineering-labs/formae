// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
)

// waitForApplyComplete blocks until no FormaCommand remains in a non-terminal
// state, or fails the test after a timeout. It replaces fixed sleeps after
// ApplyForma: a sleep either flakes under load (too short) or wastes wall-clock
// (too long). ApplyForma persists the command before returning, so there is
// always at least one incomplete command to observe draining to zero.
func waitForApplyComplete(t *testing.T, m *metastructure.Metastructure) {
	t.Helper()
	require.Eventually(t, func() bool {
		incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
		return err == nil && len(incomplete) == 0
	}, 15*time.Second, 50*time.Millisecond, "forma command(s) did not reach a terminal state in time")
}

// waitForSync forces a one-time synchronization and blocks until effect — the
// sync's observable result — holds, or fails the test after a timeout. A fixed
// sleep after ForceSync is unreliable: ForceSync only signals the Synchronizer,
// which then creates and runs the sync command asynchronously, so there is no
// generic completion the caller can poll immediately (right after ForceSync the
// sync command may not exist yet). Passing the concrete effect — e.g. the plugin
// Read having fired, or a resource reaching its post-sync state — is what makes
// the wait deterministic. effect must be safe to call concurrently with the
// plugin goroutine that produces it.
func waitForSync(t *testing.T, m *metastructure.Metastructure, effect func() bool) {
	t.Helper()
	require.NoError(t, m.ForceSync())
	require.Eventually(t, effect, 15*time.Second, 50*time.Millisecond, "force sync effect was not observed in time")
}
