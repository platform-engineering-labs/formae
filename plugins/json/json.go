// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/tidwall/pretty"
)

type JSON struct{}

// Version set at compile time
var Version = "0.0.0"

// Compile time checks to satisfy protocol
var _ plugin.Plugin = JSON{}
var _ plugin.SchemaPlugin = JSON{}

func (j JSON) Name() string {
	return "json"
}

func (j JSON) Type() plugin.Type {
	return plugin.Schema
}

func (j JSON) Version() *semver.Version {
	return semver.MustParse(Version)
}

func (j JSON) FileExtension() string {
	return ".json"
}

func (j JSON) SupportsExtract() bool {
	return false
}

func (j JSON) FormaeConfig(path string) (*model.Config, error) {
	return nil, fmt.Errorf("JSON config not supported")
}

func (j JSON) Evaluate(path string, cmd model.Command, mode model.FormaApplyMode, props map[string]string) (*model.Forma, error) {
	return nil, errors.ErrUnsupported
}

func (j JSON) Serialize(resource *model.Resource, options *plugin.SerializeOptions) (string, error) {
	input, err := json.Marshal(resource)
	if err != nil {
		return "", fmt.Errorf("error marshalling JSON for resource: %w", err)
	}

	json := input
	if options.Beautify {
		json = pretty.PrettyOptions(input, &pretty.Options{
			Width:    80,
			Prefix:   "",
			Indent:   "  ",
			SortKeys: true,
		})

		if json == nil {
			return "", fmt.Errorf("error beautifying JSON")
		}
	}

	if options.Colorize {
		json, err = highlight(json)
		if err != nil {
			return "", fmt.Errorf("error colorizing JSON: %w", err)
		}
	}

	return string(json), nil
}

func (j JSON) SerializeForma(forma *model.Forma, options *plugin.SerializeOptions) (string, error) {
	var data any

	if options.Simplified {
		simplifiedResources := make([]map[string]any, 0, len(forma.Resources))

		for _, resource := range forma.Resources {
			simplified := map[string]any{
				"Label": resource.Label,
				"Type":  resource.Type,
			}

			if resource.Stack != "" {
				simplified["Stack"] = resource.Stack
			}

			if resource.Target != "" {
				simplified["Target"] = resource.Target
			}

			// Add Properties if present and not empty
			if resource.Properties != nil {
				simplified["Properties"] = resource.Properties
			}

			simplifiedResources = append(simplifiedResources, simplified)
		}

		data = simplifiedResources
	} else {
		// Full structure with Stacks, Targets, Resources, and Policies
		data = struct {
			Stacks    []model.Stack       `json:"Stacks,omitempty"`
			Targets   []model.Target      `json:"Targets,omitempty"`
			Policies  []json.RawMessage   `json:"Policies,omitempty"`
			Resources []model.Resource    `json:"Resources,omitempty"`
		}{
			Stacks:    forma.Stacks,
			Targets:   forma.Targets,
			Policies:  forma.Policies,
			Resources: forma.Resources,
		}
	}

	input, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("error marshalling JSON: %w", err)
	}

	jsonData := input
	if options.Beautify {
		jsonData = pretty.PrettyOptions(input, &pretty.Options{
			Width:    80,
			Prefix:   "",
			Indent:   "  ",
			SortKeys: true,
		})

		if jsonData == nil {
			return "", fmt.Errorf("error beautifying JSON")
		}
	}

	if options.Colorize {
		jsonData, err = highlight(jsonData)
		if err != nil {
			return "", fmt.Errorf("error colorizing JSON: %w", err)
		}
	}

	return string(jsonData), nil
}

func highlight(code []byte) ([]byte, error) {
	var buf bytes.Buffer
	err := quick.Highlight(&buf, string(code), "yaml", "terminal", "vim")
	if err != nil {
		return nil, fmt.Errorf("highlight %s: %w", "yaml", err)
	}

	return buf.Bytes(), nil
}

func (j JSON) GenerateSourceCode(forma *model.Forma, targetPath string, includes []string, schemaLocation plugin.SchemaLocation) (plugin.GenerateSourcesResult, error) {
	return plugin.GenerateSourcesResult{}, errors.ErrUnsupported
}

func (j JSON) ProjectInit(path string, include []string, schemaLocation plugin.SchemaLocation) error {
	return errors.ErrUnsupported
}

func (j JSON) ProjectProperties(path string) (map[string]model.Prop, error) {
	return nil, errors.ErrUnsupported
}

var Plugin = JSON{}
