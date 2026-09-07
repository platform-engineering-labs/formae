// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package opsmgr

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A formae reached through a shim in a foreign prefix (a Homebrew bin, a
// /usr/local/bin copy) must resolve to the real install root, not to the
// shim's prefix: the derived root is what update installs into and what
// orbital manages, so getting it wrong points package operations at an
// unrelated directory.
func TestTreePathFrom_ResolvesShimToRealRoot(t *testing.T) {
	tmp := t.TempDir()
	realRoot := filepath.Join(tmp, "pel")
	shimRoot := filepath.Join(tmp, "homebrew")
	require.NoError(t, os.MkdirAll(filepath.Join(realRoot, "bin"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(shimRoot, "bin"), 0o755))

	realBin := filepath.Join(realRoot, "bin", "formae")
	require.NoError(t, os.WriteFile(realBin, []byte("#!/bin/sh\n"), 0o755))
	shimBin := filepath.Join(shimRoot, "bin", "formae")
	require.NoError(t, os.Symlink(realBin, shimBin))

	// EvalSymlinks resolves /tmp on darwin; compare against the resolved form.
	want, err := filepath.EvalSymlinks(realRoot)
	require.NoError(t, err)

	require.Equal(t, want, treePathFrom(shimBin), "shim must resolve to the real root")
	require.Equal(t, want, treePathFrom(realBin), "direct invocation unchanged")
}
