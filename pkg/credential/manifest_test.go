// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package credential

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadManifest_ParsesAndNormalizes(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "formae-plugin.pkl")
	content := `name = "fai"
type = "oidc-credential"
version = "0.1.0"
namespaces { "aws"; "Azure" }
minFormaeVersion = "0.90.0"
`
	require.NoError(t, os.WriteFile(manifestPath, []byte(content), 0644))

	manifest, err := ReadManifest(manifestPath)
	require.NoError(t, err)

	assert.Equal(t, "fai", manifest.Name)
	assert.Equal(t, "0.1.0", manifest.Version)
	assert.Equal(t, PluginTypeOidcCredential, manifest.Type)
	assert.Equal(t, "0.90.0", manifest.MinFormaeVersion)
	assert.Equal(t, []string{"aws", "Azure"}, manifest.Namespaces)
	assert.Equal(t, []string{"AWS", "AZURE"}, manifest.NormalizedNamespaces())
	assert.True(t, manifest.IsOidcCredentialPlugin())
	assert.NoError(t, manifest.Validate())
}

func TestReadManifest_ReturnsErrorForMissingFile(t *testing.T) {
	_, err := ReadManifest("/nonexistent/path/formae-plugin.pkl")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manifest not found")
}

func TestReadManifestFromDir_ReadsManifestFromDirectory(t *testing.T) {
	dir := t.TempDir()
	content := `name = "fai"
type = "oidc-credential"
version = "0.2.0"
namespaces { "gcp" }
minFormaeVersion = "0.90.0"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, DefaultManifestPath), []byte(content), 0644))

	manifest, err := ReadManifestFromDir(dir)
	require.NoError(t, err)
	assert.Equal(t, "fai", manifest.Name)
	assert.Equal(t, []string{"GCP"}, manifest.NormalizedNamespaces())
}

func TestManifest_Validate_RequiresFields(t *testing.T) {
	tests := []struct {
		name        string
		manifest    Manifest
		expectError string
	}{
		{
			name:        "missing name",
			manifest:    Manifest{Version: "1.0.0", Namespaces: []string{"aws"}, MinFormaeVersion: "0.90.0"},
			expectError: "name is required",
		},
		{
			name:        "missing version",
			manifest:    Manifest{Name: "fai", Namespaces: []string{"aws"}, MinFormaeVersion: "0.90.0"},
			expectError: "version is required",
		},
		{
			name:        "missing namespaces",
			manifest:    Manifest{Name: "fai", Version: "1.0.0", MinFormaeVersion: "0.90.0"},
			expectError: "namespaces is required",
		},
		{
			name:        "missing minFormaeVersion",
			manifest:    Manifest{Name: "fai", Version: "1.0.0", Namespaces: []string{"aws"}},
			expectError: "minFormaeVersion is required",
		},
		{
			name:        "valid manifest",
			manifest:    Manifest{Name: "fai", Version: "1.0.0", Namespaces: []string{"aws"}, MinFormaeVersion: "0.90.0"},
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

func TestManifest_IsOidcCredentialPlugin_RequiresExactType(t *testing.T) {
	m := Manifest{Type: "resource"}
	assert.False(t, m.IsOidcCredentialPlugin())

	m.Type = PluginTypeOidcCredential
	assert.True(t, m.IsOidcCredentialPlugin())
}

func TestNormalizedNamespaces_UppercasesEveryEntry(t *testing.T) {
	m := Manifest{Namespaces: []string{"aws", "Gcp", "AZURE"}}
	assert.Equal(t, []string{"AWS", "GCP", "AZURE"}, m.NormalizedNamespaces())
}
