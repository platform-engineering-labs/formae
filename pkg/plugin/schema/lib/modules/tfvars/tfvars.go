// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tfvars

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/extension"
	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/registry"
	"github.com/zclconf/go-cty/cty"
)

type tfvarsLib struct{}

var _ extension.Library = tfvarsLib{}

func init() {
	registry.Register("tfvars", func() extension.Library {
		return tfvarsLib{}
	})
}

func (tfvarsLib) Invoke(uri *url.URL) *extension.Result {
	call, args := extension.NameArgsFrom(uri)

	switch call {
	case "read":
		return read(args)
	default:
		return &extension.Result{
			Error: fmt.Sprintf("unknown function name: %s", call),
		}
	}
}

func read(args url.Values) *extension.Result {
	path := args.Get("path")
	if path == "" {
		return &extension.Result{
			Error: "path parameter is required",
		}
	}

	result, err := ParseTFVarsFile(path)
	if err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("failed to parse tfvars file: %v", err),
		}
	}

	body, err := json.Marshal(result)
	if err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("failed to serialize result: %v", err),
		}
	}

	return &extension.Result{
		Body: body,
	}
}

// ParseTFVarsFile parses a .tfvars file and returns a map of its values.
func ParseTFVarsFile(path string) (map[string]any, error) {
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCLFile(path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parsing %s: %s", path, diags.Error())
	}

	attrs, diags := file.Body.JustAttributes()
	if diags.HasErrors() {
		return nil, fmt.Errorf("extracting attributes: %s", diags.Error())
	}

	result := make(map[string]any, len(attrs))

	// Sort keys for deterministic output
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	ctx := &hcl.EvalContext{}
	for _, k := range keys {
		val, diags := attrs[k].Expr.Value(ctx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("evaluating %q: %s", k, diags.Error())
		}

		goVal, err := ctyToGo(val)
		if err != nil {
			return nil, fmt.Errorf("converting %q: %w", k, err)
		}
		result[k] = goVal
	}

	return result, nil
}

func ctyToGo(val cty.Value) (any, error) {
	if val.IsNull() {
		return nil, nil
	}

	ty := val.Type()

	switch {
	case ty == cty.String:
		return val.AsString(), nil

	case ty == cty.Bool:
		return val.True(), nil

	case ty == cty.Number:
		bf := val.AsBigFloat()
		if bf.IsInt() {
			i, _ := bf.Int64()
			return i, nil
		}
		f, _ := bf.Float64()
		return f, nil

	case ty.IsListType() || ty.IsTupleType() || ty.IsSetType():
		var items []any
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			goVal, err := ctyToGo(v)
			if err != nil {
				return nil, err
			}
			items = append(items, goVal)
		}
		if items == nil {
			items = []any{}
		}
		return items, nil

	case ty.IsMapType() || ty.IsObjectType():
		m := make(map[string]any)
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			goVal, err := ctyToGo(v)
			if err != nil {
				return nil, err
			}
			m[k.AsString()] = goVal
		}
		return m, nil

	default:
		return nil, fmt.Errorf("unsupported cty type: %s", ty.FriendlyName())
	}
}
