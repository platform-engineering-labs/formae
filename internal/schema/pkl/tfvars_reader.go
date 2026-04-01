// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"

	pklgo "github.com/apple/pkl-go/pkl"
	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/modules/tfvars"
)

// tfvarsReader is a PKL resource reader for the "tfvars:" scheme.
// It resolves relative paths against baseDir (typically the project directory).
type tfvarsReader struct {
	baseDir string
}

var _ pklgo.ResourceReader = tfvarsReader{}

func (tfvarsReader) Scheme() string {
	return "tfvars"
}

func (tfvarsReader) HasHierarchicalUris() bool {
	return true
}

func (tfvarsReader) IsGlobbable() bool {
	return false
}

func (tfvarsReader) ListElements(_ url.URL) ([]pklgo.PathElement, error) {
	return nil, nil
}

func (r tfvarsReader) Read(uri url.URL) ([]byte, error) {
	path := uri.Opaque
	if path == "" {
		path = filepath.Join(uri.Host, uri.Path)
	}

	if filepath.Ext(path) != ".tfvars" {
		return nil, fmt.Errorf("only .tfvars files are supported: %s", path)
	}

	// Resolve relative paths against the base directory
	if !filepath.IsAbs(path) && r.baseDir != "" {
		path = filepath.Join(r.baseDir, path)
	}

	result, err := tfvars.ParseTFVarsFile(path)
	if err != nil {
		return nil, err
	}

	return json.Marshal(result)
}
