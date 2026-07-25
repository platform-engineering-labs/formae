// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadManifest_ParsesValidManifest(t *testing.T) {
	// Get path to test fixture
	wd, err := os.Getwd()
	require.NoError(t, err)
	manifestPath := filepath.Join(wd, "testdata", "valid-manifest.pkl")

	manifest, err := ReadManifest(manifestPath)
	require.NoError(t, err)

	assert.Equal(t, "test-plugin", manifest.Name)
	assert.Equal(t, "1.2.3", manifest.Version)
	assert.Equal(t, "Test", manifest.Namespace)
	assert.Equal(t, "Apache-2.0", manifest.License)
	assert.Equal(t, "0.80.0", manifest.MinFormaeVersion)
}

func TestReadManifest_ReturnsErrorForMissingFile(t *testing.T) {
	_, err := ReadManifest("/nonexistent/path/manifest.pkl")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manifest not found")
}

func TestReadManifestFromDir_ReadsManifestFromDirectory(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)

	// Create a temp directory with a manifest
	tempDir := t.TempDir()
	manifestContent := `
name = "dir-test"
version = "0.1.0"
namespace = "DirTest"
license = "MIT"
minFormaeVersion = "0.75.0"
output { renderer = new JsonRenderer {} }
`
	err = os.WriteFile(filepath.Join(tempDir, "formae-plugin.pkl"), []byte(manifestContent), 0644)
	require.NoError(t, err)

	// Change back to original dir after test
	defer func() { _ = os.Chdir(wd) }()

	manifest, err := ReadManifestFromDir(tempDir)
	require.NoError(t, err)

	assert.Equal(t, "dir-test", manifest.Name)
	assert.Equal(t, "DirTest", manifest.Namespace)
}

func TestReadManifest_ParsesSummaryAndCategory(t *testing.T) {
	tempDir := t.TempDir()
	manifestContent := `
name = "summary-test"
version = "1.0.0"
namespace = "Test"
license = "MIT"
minFormaeVersion = "0.85.0"
summary = "A short one-liner for testing"
category = "cloud"
output { renderer = new JsonRenderer {} }
`
	err := os.WriteFile(filepath.Join(tempDir, "formae-plugin.pkl"), []byte(manifestContent), 0644)
	require.NoError(t, err)

	manifest, err := ReadManifestFromDir(tempDir)
	require.NoError(t, err)
	assert.Equal(t, "A short one-liner for testing", manifest.Summary)
	assert.Equal(t, "cloud", manifest.Category)
}

func TestReadManifest_SummaryAndCategoryOptional(t *testing.T) {
	tempDir := t.TempDir()
	manifestContent := `
name = "no-extras"
version = "1.0.0"
namespace = "Test"
license = "MIT"
minFormaeVersion = "0.85.0"
output { renderer = new JsonRenderer {} }
`
	err := os.WriteFile(filepath.Join(tempDir, "formae-plugin.pkl"), []byte(manifestContent), 0644)
	require.NoError(t, err)

	manifest, err := ReadManifestFromDir(tempDir)
	require.NoError(t, err)
	assert.Equal(t, "", manifest.Summary)
	assert.Equal(t, "", manifest.Category)
	assert.NoError(t, manifest.Validate())
}

func TestManifest_Validate_RequiresAllFields(t *testing.T) {
	tests := []struct {
		name        string
		manifest    Manifest
		expectError string
	}{
		{
			name:        "missing name",
			manifest:    Manifest{Version: "1.0.0", Namespace: "Test", License: "MIT", MinFormaeVersion: "0.80.0"},
			expectError: "name is required",
		},
		{
			name:        "missing version",
			manifest:    Manifest{Name: "test", Namespace: "Test", License: "MIT", MinFormaeVersion: "0.80.0"},
			expectError: "version is required",
		},
		{
			name:        "missing namespace",
			manifest:    Manifest{Name: "test", Version: "1.0.0", License: "MIT", MinFormaeVersion: "0.80.0"},
			expectError: "namespace is required",
		},
		{
			name:        "missing license",
			manifest:    Manifest{Name: "test", Version: "1.0.0", Namespace: "Test", MinFormaeVersion: "0.80.0"},
			expectError: "license is required",
		},
		{
			name:        "missing minFormaeVersion",
			manifest:    Manifest{Name: "test", Version: "1.0.0", Namespace: "Test", License: "MIT"},
			expectError: "minFormaeVersion is required",
		},
		{
			name:        "valid manifest without summary or category",
			manifest:    Manifest{Name: "test", Version: "1.0.0", Namespace: "Test", License: "MIT", MinFormaeVersion: "0.80.0"},
			expectError: "",
		},
		{
			name:        "valid manifest with summary and category",
			manifest:    Manifest{Name: "test", Version: "1.0.0", Namespace: "Test", License: "MIT", MinFormaeVersion: "0.80.0", Summary: "A plugin", Category: "cloud"},
			expectError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.manifest.Validate()
			if tt.expectError == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectError)
			}
		})
	}
}

func TestManifest_DefaultReap_Absent(t *testing.T) {
	var m Manifest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","version":"1","namespace":"X","license":"MIT","minFormaeVersion":"1"}`), &m))

	reaping, err := m.DefaultReap()
	require.NoError(t, err)
	assert.Nil(t, reaping, "absent defaultReap must yield a nil reaping behaviour")
}

func TestManifest_DefaultReap_Never(t *testing.T) {
	var m Manifest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","version":"1","namespace":"X","license":"MIT","minFormaeVersion":"1","defaultReap":{"Kind":"never"}}`), &m))

	reaping, err := m.DefaultReap()
	require.NoError(t, err)
	require.NotNil(t, reaping)
	assert.Equal(t, "never", reaping.GetKind())
}

func TestManifest_DefaultReap_After(t *testing.T) {
	var m Manifest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","version":"1","namespace":"X","license":"MIT","minFormaeVersion":"1","defaultReap":{"Kind":"after","MaxUnreachableSeconds":7200}}`), &m))

	reaping, err := m.DefaultReap()
	require.NoError(t, err)
	require.NotNil(t, reaping)
	after, ok := reaping.(*pkgmodel.ReapAfter)
	require.True(t, ok)
	assert.Equal(t, int64(7200), after.MaxUnreachableSeconds)
}
