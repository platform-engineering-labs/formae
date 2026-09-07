// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package opsmgr_test

import (
	"log/slog"
	"net/url"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	"github.com/stretchr/testify/require"
)

// A writable manager can initialize a fresh tree; a read-only manager over
// that initialized tree constructs and lists without any privilege check.
// This is the contract the sudo-less read commands depend on.
func TestReadOnlyManagerOverInitializedTree(t *testing.T) {
	root := t.TempDir()
	t.Setenv(opsmgr.FormaePelRootEnv, root)

	u, err := url.Parse("https://example.invalid/repo#stable")
	require.NoError(t, err)

	// Writable manager initializes the (non-privileged) temp tree.
	w, err := opsmgr.New(slog.Default(), *u, "", false, true)
	require.NoError(t, err)
	if !w.Ready() {
		_, err = w.Initialize()
		require.NoError(t, err)
	}
	require.True(t, w.Ready())

	// Read-only manager over the initialized tree: constructs, no elevation,
	// and reads succeed (empty install set).
	ro, err := opsmgr.New(slog.Default(), *u, "", false, false)
	require.NoError(t, err)
	require.True(t, ro.Ready())

	pkgs, err := ro.List()
	require.NoError(t, err)
	require.Empty(t, pkgs)
}
