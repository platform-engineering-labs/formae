// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package credential

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PluginTypeOidcCredential is the only manifest Type value this package
// understands. See pkg/plugin.PluginTypeOidcCredential for the resource-plugin
// side's copy of the same string; the two packages don't share a Go type so
// each keeps its own constant.
const PluginTypeOidcCredential = "oidc-credential"

// DefaultManifestPath is the expected location of the plugin manifest,
// relative to the broker binary's own directory.
const DefaultManifestPath = "formae-plugin.pkl"

// Manifest is the minimal subset of a formae-plugin.pkl manifest an
// oidc-credential broker needs: enough to identify itself and the
// credential namespaces it issues. Keep this minimal - add fields only when
// truly needed.
type Manifest struct {
	// Name is the unique plugin identifier (e.g., "fai").
	Name string `json:"name"`

	// Version is the semantic version of the plugin.
	Version string `json:"version"`

	// Type is the plugin type. Run refuses to serve a manifest whose Type
	// isn't PluginTypeOidcCredential.
	Type string `json:"type,omitempty"`

	// Namespaces lists the credential namespaces the broker issues (e.g.
	// "AWS", "GCP").
	Namespaces []string `json:"namespaces,omitempty"`

	// MinFormaeVersion is the minimum formae version this plugin supports.
	MinFormaeVersion string `json:"minFormaeVersion"`
}

// IsOidcCredentialPlugin returns true if this manifest declares itself an
// oidc-credential broker.
func (m *Manifest) IsOidcCredentialPlugin() bool {
	return m.Type == PluginTypeOidcCredential
}

// NormalizedNamespaces returns a copy of Namespaces with every entry
// upper-cased, leaving the original manifest untouched.
func (m *Manifest) NormalizedNamespaces() []string {
	normalized := make([]string, len(m.Namespaces))
	for i, ns := range m.Namespaces {
		normalized[i] = strings.ToUpper(ns)
	}
	return normalized
}

// Validate checks that the manifest carries the fields an oidc-credential
// broker needs to run, independent of whether Type is actually
// PluginTypeOidcCredential (Run checks that separately, since a manifest
// read by this package is always expected to describe a credential broker).
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if m.Version == "" {
		return fmt.Errorf("manifest: version is required")
	}
	if len(m.Namespaces) == 0 {
		return fmt.Errorf("manifest: namespaces is required")
	}
	if m.MinFormaeVersion == "" {
		return fmt.Errorf("manifest: minFormaeVersion is required")
	}
	return nil
}

// ReadManifest reads and parses a formae-plugin.pkl manifest file. It uses
// the Pkl CLI to evaluate the manifest and extract the rendered JSON. Same
// mechanism as pkg/plugin.ReadManifest.
func ReadManifest(path string) (*Manifest, error) {
	if path == "" {
		path = DefaultManifestPath
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("manifest not found: %s", path)
	}

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

// ReadManifestFromDir reads the manifest from the plugin's directory. This
// is useful when the current working directory is not the plugin root.
func ReadManifestFromDir(dir string) (*Manifest, error) {
	path := filepath.Join(dir, DefaultManifestPath)
	return ReadManifest(path)
}
