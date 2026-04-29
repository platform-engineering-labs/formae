// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package json

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/tidwall/pretty"
)

func init() {
	schema.DefaultRegistry.Register(JSON{})
}

var _ schema.SchemaPlugin = JSON{}

type JSON struct{}

func (j JSON) Name() string {
	return "json"
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

func (j JSON) SerializeForma(forma *model.Forma, options *schema.SerializeOptions) (string, error) {
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
			Stacks    []model.Stack     `json:"Stacks,omitempty"`
			Targets   []model.Target    `json:"Targets,omitempty"`
			Policies  []json.RawMessage `json:"Policies,omitempty"`
			Resources []model.Resource  `json:"Resources,omitempty"`
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

func (j JSON) GenerateSourceCode(forma *model.Forma, targetPath string, includes []string, options *schema.SerializeOptions) (schema.GenerateSourcesResult, error) {
	return schema.GenerateSourcesResult{}, errors.ErrUnsupported
}

func (j JSON) ProjectInit(path string, include []string, schemaLocation schema.SchemaLocation) error {
	return errors.ErrUnsupported
}

func (j JSON) ProjectProperties(path string) (map[string]model.Prop, error) {
	return nil, errors.ErrUnsupported
}
