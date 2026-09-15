// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package extract

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/schema"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RenderBundle contains already retrieved declarations and installed plugin
// metadata. No connection settings or credentials are accepted or needed.
type RenderBundle struct {
	Forma   *pkgmodel.Forma   `json:"Forma"`
	Plugins []apimodel.Plugin `json:"Plugins"`
}

func readRenderBundle(reader io.Reader) (*RenderBundle, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var bundle *RenderBundle
	if err := decoder.Decode(&bundle); err != nil {
		return nil, fmt.Errorf("invalid rendering bundle: %w", err)
	}
	if bundle == nil || bundle.Forma == nil || bundle.Plugins == nil {
		return nil, fmt.Errorf("rendering bundle requires Forma and Plugins (use [] when no plugins are required)")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("rendering bundle must contain one JSON object")
	}
	return bundle, nil
}
func renderOffline(bundle *RenderBundle, opts *ExtractOptions) (schema.GenerateSourcesResult, error) {
	plugins := map[string]app.PluginInfo{}
	for _, plugin := range bundle.Plugins {
		if plugin.Type != "resource" {
			continue
		}
		key := strings.ToLower(plugin.Namespace)
		if key == "" {
			key = strings.ToLower(plugin.Name)
		}
		if key == "" {
			return schema.GenerateSourcesResult{}, fmt.Errorf("resource plugin has no namespace or name")
		}
		if _, exists := plugins[key]; exists {
			return schema.GenerateSourcesResult{}, fmt.Errorf("duplicate resource plugin namespace %q", key)
		}
		plugins[key] = app.PluginInfo{Version: plugin.InstalledVersion, LocalPath: plugin.LocalPath}
	}
	deps, err := app.BuildDependencyStrings(bundle.Forma, plugins, opts.SchemaLocation)
	if err != nil {
		return schema.GenerateSourcesResult{}, err
	}
	plugin, err := schema.DefaultRegistry.Get(opts.OutputSchema)
	if err != nil {
		return schema.GenerateSourcesResult{}, err
	}
	return plugin.GenerateSourceCode(bundle.Forma, opts.TargetPath, nil, &schema.SerializeOptions{Schema: opts.OutputSchema, SchemaLocation: opts.SchemaLocation, Dependencies: deps})
}
