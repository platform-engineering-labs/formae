// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanStaleDev_RemovesMatchingPlugins(t *testing.T) {
	systemDir := t.TempDir()
	devDir := t.TempDir()

	createFakePlugin(t, systemDir, "aws", "v1.0.0", resourceManifest("aws", "AWS"))
	createFakePlugin(t, devDir, "aws", "v0.9.0", resourceManifest("aws", "AWS"))
	createFakePlugin(t, devDir, "my-custom", "v0.1.0", "")

	err := CleanStaleDevPlugins(systemDir, devDir)
	require.NoError(t, err)

	// aws should be removed from dev dir
	_, err = os.Stat(filepath.Join(devDir, "aws"))
	assert.True(t, os.IsNotExist(err))

	// my-custom should remain
	_, err = os.Stat(filepath.Join(devDir, "my-custom"))
	assert.NoError(t, err)
}

func TestCleanStaleDev_SkipsIfAlreadyMigrated(t *testing.T) {
	systemDir := t.TempDir()
	devDir := t.TempDir()

	createFakePlugin(t, systemDir, "aws", "v1.0.0", resourceManifest("aws", "AWS"))
	createFakePlugin(t, devDir, "aws", "v0.9.0", resourceManifest("aws", "AWS"))

	// Write marker
	markerPath := filepath.Join(devDir, ".migrated-v1")
	require.NoError(t, os.WriteFile(markerPath, []byte("done"), 0644))

	err := CleanStaleDevPlugins(systemDir, devDir)
	require.NoError(t, err)

	// aws should still exist — migration was skipped
	_, err = os.Stat(filepath.Join(devDir, "aws"))
	assert.NoError(t, err)
}

func TestCleanStaleDev_CreatesMarker(t *testing.T) {
	systemDir := t.TempDir()
	devDir := t.TempDir()

	createFakePlugin(t, systemDir, "aws", "v1.0.0", resourceManifest("aws", "AWS"))
	createFakePlugin(t, devDir, "aws", "v0.9.0", resourceManifest("aws", "AWS"))

	err := CleanStaleDevPlugins(systemDir, devDir)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(devDir, ".migrated-v1"))
	assert.NoError(t, err)
}

func TestCleanStaleDev_NoOpWhenSystemDirMissing(t *testing.T) {
	devDir := t.TempDir()
	createFakePlugin(t, devDir, "aws", "v0.9.0", resourceManifest("aws", "AWS"))

	err := CleanStaleDevPlugins("/nonexistent/system/dir", devDir)
	require.NoError(t, err)

	// dev plugins untouched
	_, err = os.Stat(filepath.Join(devDir, "aws"))
	assert.NoError(t, err)
}
