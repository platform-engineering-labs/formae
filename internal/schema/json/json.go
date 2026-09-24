// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package json

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
	return true
}

func (j JSON) FormaeConfig(path string) (*model.Config, error) {
	return nil, fmt.Errorf("JSON config not supported")
}

func (j JSON) Evaluate(path string, cmd model.Command, mode model.FormaApplyMode, props map[string]string) (*model.Forma, error) {
	if cmd != model.CommandApply && cmd != model.CommandEval {
		return nil, fmt.Errorf("JSON evaluated Forma does not support command %q", cmd)
	}
	if len(props) != 0 {
		return nil, fmt.Errorf("JSON evaluated Forma does not support property overrides")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read JSON evaluated Forma: %w", err)
	}
	if err := validateSingleJSONObject(data); err != nil {
		return nil, err
	}
	if err := validateCustomTypedFields(data); err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var forma model.Forma
	if err := decoder.Decode(&forma); err != nil {
		return nil, fmt.Errorf("decode JSON evaluated Forma: %w", err)
	}
	if err := expectJSONEOF(decoder); err != nil {
		return nil, err
	}
	return &forma, nil
}

// validateCustomTypedFields restores strict unknown-field handling for model
// types with compatibility UnmarshalJSON methods. Those methods intentionally
// accept extra fields in stored data, while a frozen review artifact must not.
func validateCustomTypedFields(data []byte) error {
	var view struct {
		Properties map[string]json.RawMessage
		Resources  []struct {
			Schema struct {
				Hints map[string]json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(data, &view); err != nil {
		return fmt.Errorf("decode JSON evaluated Forma: %w", err)
	}
	type strictProp model.Prop
	for name, raw := range view.Properties {
		var prop strictProp
		if err := strictDecode(raw, &prop); err != nil {
			return fmt.Errorf("decode JSON evaluated Forma at $.Properties.%s: %w", name, err)
		}
	}
	type strictFieldHint model.FieldHint
	for resourceIndex, resource := range view.Resources {
		for field, raw := range resource.Schema.Hints {
			var hint strictFieldHint
			if err := strictDecode(raw, &hint); err != nil {
				return fmt.Errorf("decode JSON evaluated Forma at $.Resources[%d].Schema.Hints.%s: %w", resourceIndex, field, err)
			}
		}
	}
	return nil
}

func strictDecode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return expectJSONEOF(decoder)
}

// validateSingleJSONObject rejects ambiguous JSON before typed decoding. The
// token walk covers opaque plugin payloads too, where typed decoding cannot see
// duplicate members. Errors name structure only and never include values.
func validateSingleJSONObject(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON evaluated Forma: %w", err)
	}
	root, ok := token.(json.Delim)
	if !ok || root != '{' {
		return fmt.Errorf("decode JSON evaluated Forma: top-level value must be an object")
	}
	if err := scanJSONObject(decoder, "$"); err != nil {
		return err
	}
	return expectJSONEOF(decoder)
}

func scanJSONObject(decoder *json.Decoder, path string) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode JSON evaluated Forma at %s: %w", path, err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("decode JSON evaluated Forma at %s: object member name must be a string", path)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("decode JSON evaluated Forma at %s: duplicate object member", path)
		}
		seen[key] = struct{}{}
		if err := scanJSONValue(decoder, path+"."+key); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("decode JSON evaluated Forma at %s: %w", path, err)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON evaluated Forma at %s: %w", path, err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return scanJSONObject(decoder, path)
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := scanJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("decode JSON evaluated Forma at %s: %w", path, err)
		}
		return nil
	default:
		return fmt.Errorf("decode JSON evaluated Forma at %s: unexpected delimiter", path)
	}
}

func expectJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode JSON evaluated Forma: %w", err)
	}
	return fmt.Errorf("decode JSON evaluated Forma: multiple top-level values")
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
		full := *forma
		if full.Properties == nil {
			full.Properties = map[string]model.Prop{}
		}
		data = &full
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
	contents, err := j.SerializeForma(forma, &schema.SerializeOptions{Schema: "json", Beautify: true})
	if err != nil {
		return schema.GenerateSourcesResult{}, err
	}

	dir := filepath.Dir(targetPath)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(targetPath)+"-*")
	if err != nil {
		return schema.GenerateSourcesResult{}, fmt.Errorf("create temporary JSON source: %w", err)
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if err := temp.Chmod(0600); err != nil {
		cleanup()
		return schema.GenerateSourcesResult{}, fmt.Errorf("protect temporary JSON source: %w", err)
	}
	if _, err := temp.WriteString(contents); err != nil {
		cleanup()
		return schema.GenerateSourcesResult{}, fmt.Errorf("write temporary JSON source: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return schema.GenerateSourcesResult{}, fmt.Errorf("sync temporary JSON source: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return schema.GenerateSourcesResult{}, fmt.Errorf("close temporary JSON source: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		_ = os.Remove(tempPath)
		return schema.GenerateSourcesResult{}, fmt.Errorf("replace JSON source: %w", err)
	}
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}

	return schema.GenerateSourcesResult{TargetPath: targetPath, ResourceCount: len(forma.Resources)}, nil
}

func (j JSON) ProjectInit(path string, include []string, schemaLocation schema.SchemaLocation) error {
	return errors.ErrUnsupported
}

func (j JSON) ProjectProperties(path string) (map[string]model.Prop, error) {
	return map[string]model.Prop{}, nil
}
