// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Manifest represents a parsed formae-plugin.pkl file.
// This is the contract between plugin developers and formae.
// Keep this minimal - add fields only when truly needed.
type Manifest struct {
	// Name is the unique plugin identifier (e.g., "aws", "localstack")
	// Used in repository naming: formae-plugin-<name>
	Name string `json:"name"`

	// Version is the semantic version of the plugin
	Version string `json:"version"`

	// Namespace is the resource type prefix (e.g., "AWS" for "AWS::EC2::Instance")
	// Multiple plugins can share a namespace (e.g., localstack uses "AWS")
	Namespace string `json:"namespace"`

	// License is the SPDX license identifier (e.g., "Apache-2.0", "MIT")
	License string `json:"license"`

	// MinFormaeVersion is the minimum formae version this plugin supports
	// Used for compatibility checking and matrix testing
	MinFormaeVersion string `json:"minFormaeVersion"`
}

// DefaultManifestPath is the expected location of the plugin manifest.
const DefaultManifestPath = "formae-plugin.pkl"

// ReadManifest reads and parses a formae-plugin.pkl manifest file.
// It uses the Pkl CLI to evaluate the manifest and extract the rendered JSON.
func ReadManifest(path string) (*Manifest, error) {
	if path == "" {
		path = DefaultManifestPath
	}

	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("manifest not found: %s", path)
	}

	// Use pkl eval to render the manifest as JSON
	cmd := exec.Command("pkl", "eval", "-f", "json", path)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("failed to evaluate manifest: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("failed to run pkl: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(output, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}

	return &manifest, nil
}

// ReadManifestFromDir reads the manifest from the plugin's directory.
// This is useful when the current working directory is not the plugin root.
func ReadManifestFromDir(dir string) (*Manifest, error) {
	path := filepath.Join(dir, DefaultManifestPath)
	return ReadManifest(path)
}

// Validate checks that the manifest has all required fields.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if m.Version == "" {
		return fmt.Errorf("manifest: version is required")
	}
	if m.Namespace == "" {
		return fmt.Errorf("manifest: namespace is required")
	}
	if m.License == "" {
		return fmt.Errorf("manifest: license is required")
	}
	if m.MinFormaeVersion == "" {
		return fmt.Errorf("manifest: minFormaeVersion is required")
	}
	return nil
}
