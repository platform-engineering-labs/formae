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
	"gopkg.in/yaml.v3"
)

type YAML struct{}

// Version set at compile time
var Version = "0.0.0"

// Compile time checks to satisfy protocol
var _ plugin.Plugin = YAML{}
var _ plugin.SchemaPlugin = YAML{}

func (j YAML) Name() string {
	return "yaml"
}

func (j YAML) Type() plugin.Type {
	return plugin.Schema
}

func (j YAML) Version() *semver.Version {
	return semver.MustParse(Version)
}

func (j YAML) FileExtension() string {
	return ".yaml"
}

func (j YAML) SupportsExtract() bool {
	return false
}

func (j YAML) FormaeConfig(path string) (*model.Config, error) {
	return nil, fmt.Errorf("YAML config not supported")
}

func (j YAML) Evaluate(path string, cmd model.Command, mode model.FormaApplyMode, props map[string]string) (*model.Forma, error) {
	return nil, errors.ErrUnsupported
}

func (j YAML) Serialize(resource *model.Resource, options *plugin.SerializeOptions) (string, error) {
	yaml, err := encodeYAML(resource)
	if err != nil {
		return "", fmt.Errorf("error encoding YAML: %w", err)
	}

	if options.Colorize {
		yaml, err = highlight(yaml)
		if err != nil {
			return "", fmt.Errorf("error colorizing YAML: %w", err)
		}
	}

	return string(yaml), nil
}

func (j YAML) SerializeForma(forma *model.Forma, options *plugin.SerializeOptions) (string, error) {
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
		// Full structure with Targets and Resources
		data = struct {
			Targets   []model.Target   `yaml:"Targets,omitempty"`
			Resources []model.Resource `yaml:"Resources,omitempty"`
		}{
			Targets:   forma.Targets,
			Resources: forma.Resources,
		}
	}

	yamlData, err := encodeYAML(data)
	if err != nil {
		return "", fmt.Errorf("error encoding YAML: %w", err)
	}

	if options.Colorize {
		yamlData, err = highlight(yamlData)
		if err != nil {
			return "", fmt.Errorf("error colorizing YAML: %w", err)
		}
	}

	return string(yamlData), nil
}

// encodeYAML encodes the given value to YAML format with proper indentation.
// by marshalling to json first, we ensure that any json.RawMessage fields are handled correctly.
func encodeYAML(v any) ([]byte, error) {
	intermediate, err := convertRawMessages(v)
	if err != nil {
		return nil, fmt.Errorf("convert raw messages: %w", err)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(intermediate); err != nil {
		return nil, fmt.Errorf("yaml encode: %w", err)
	}
	return buf.Bytes(), nil
}

func convertRawMessages(v any) (any, error) {
	jsonData, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var result any
	if err := json.Unmarshal(jsonData, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func highlight(code []byte) ([]byte, error) {
	var buf bytes.Buffer
	err := quick.Highlight(&buf, string(code), "yaml", "terminal", "vim")
	if err != nil {
		return nil, fmt.Errorf("highlight %s: %w", "yaml", err)
	}

	return buf.Bytes(), nil
}

func (j YAML) GenerateSourceCode(forma *model.Forma, targetPath string, includes []string, schemaLocation plugin.SchemaLocation) (plugin.GenerateSourcesResult, error) {
	return plugin.GenerateSourcesResult{}, errors.ErrUnsupported
}

func (j YAML) ProjectInit(path string, include []string, schemaLocation plugin.SchemaLocation) error {
	return errors.ErrUnsupported
}

func (j YAML) ProjectProperties(path string) (map[string]model.Prop, error) {
	return nil, errors.ErrUnsupported
}

var Plugin = YAML{}
