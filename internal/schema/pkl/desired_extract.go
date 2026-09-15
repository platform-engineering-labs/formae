// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package pkl

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/platform-engineering-labs/formae/pkg/model"
)

// prepareDesiredExtraction adds renderer-only local names and local-only
// generator dependencies to a private serialization copy. No draw values exist
// in this context and no external generator becomes a Listing entry.
func prepareDesiredExtraction(f *model.Forma) (*model.Forma, error) {
	if f == nil || f.Extraction == nil {
		return f, nil
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	var out model.Forma
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	for _, stack := range out.Stacks {
		for _, raw := range stack.Policies {
			if !model.IsPolicyReference(raw) {
				if _, err := model.ParsePolicy(raw); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, raw := range out.Policies {
		if _, err := model.ParsePolicy(raw); err != nil {
			return nil, err
		}
	}
	names := map[model.GeneratorKey]string{}
	out.Generators = nil
	appendGenerator := func(raw json.RawMessage, localOnly bool) error {
		g, err := model.ParseGenerator(raw)
		if err != nil {
			return err
		}
		key := model.GeneratorKey{Stack: g.GetStack(), Label: g.GetLabel()}
		if _, exists := names[key]; exists {
			return fmt.Errorf("duplicate desired generator %s/%s", key.Stack, key.Label)
		}
		name := "_formaeGenerator_" + hex.EncodeToString([]byte(key.Stack)) + "_" + hex.EncodeToString([]byte(key.Label))
		names[key] = name
		var value map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return err
		}
		value["$localName"] = name
		value["$localOnly"] = localOnly
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out.Generators = append(out.Generators, encoded)
		return nil
	}
	for _, raw := range f.Generators {
		if err := appendGenerator(raw, false); err != nil {
			return nil, err
		}
	}
	for _, raw := range f.Extraction.ReferenceGenerators {
		if err := appendGenerator(raw, true); err != nil {
			return nil, err
		}
	}
	unresolved := map[string]bool{}
	for _, diagnostic := range f.Extraction.Diagnostics {
		if diagnostic.Code == "unresolved_desired_reference" {
			unresolved[diagnostic.Reference] = true
		}
	}
	var walk func(any) error
	walk = func(value any) error {
		switch n := value.(type) {
		case []any:
			for _, v := range n {
				if err := walk(v); err != nil {
					return err
				}
			}
		case map[string]any:
			if _, exists := n["$unresolved"]; exists {
				return fmt.Errorf("reserved desired renderer marker")
			}
			if ref, ok := n["$ref"].(string); ok {
				if !model.FormaeURI(ref).IsValid() {
					return fmt.Errorf("invalid desired resource reference %q", ref)
				}
				if !unresolved[ref] {
					return fmt.Errorf("unclassified desired reference %s", ref)
				}
				if _, exists := n["$transform"]; exists {
					return fmt.Errorf("unsupported desired reference transform")
				}
				if strategy, exists := n["$strategy"]; exists && strategy != nil && strategy != "" && strategy != "Update" {
					return fmt.Errorf("unsupported desired reference strategy %v", strategy)
				}
				if visibility, exists := n["$visibility"]; exists && visibility != nil && visibility != "Clear" && visibility != "Opaque" {
					return fmt.Errorf("unsupported desired reference visibility")
				}
				if selector, exists := n["$json"]; exists && selector != nil {
					if _, ok := selector.(string); !ok {
						return fmt.Errorf("unsupported desired reference JSON selector")
					}
				}
				n["$unresolved"] = "Unresolved desired reference " + ref + "; remove the dependent declaration, rewire this reference, or explicitly restore the dependency before applying."
				return nil
			}
			if n["$embed"] == true {
				if parts, ok := n["$templateParts"].([]any); ok {
					for _, part := range parts {
						if envelope, ok := part.(map[string]any); ok {
							if _, hasSelector := envelope["$json"]; hasSelector {
								return fmt.Errorf("unsupported desired reference JSON selector inside an embed")
							}
						}
					}
				}
			}
			if n["$res"] == true || n["$gen"] == true {
				if _, ok := n["$transform"]; ok {
					return fmt.Errorf("unsupported desired reference transform")
				}
				if strategy, ok := n["$strategy"]; ok && strategy != nil && strategy != "" && strategy != "Update" {
					return fmt.Errorf("unsupported desired reference strategy %v", strategy)
				}
			}
			if n["$gen"] == true {
				label, _ := n["$label"].(string)
				stack, _ := n["$stack"].(string)
				name, ok := names[model.GeneratorKey{Label: label, Stack: stack}]
				if !ok {
					return fmt.Errorf("missing desired generator declaration %s/%s", stack, label)
				}
				n["$localName"] = name
			}
			for _, v := range n {
				if err := walk(v); err != nil {
					return err
				}
			}
		}
		return nil
	}
	var documents []*json.RawMessage
	for i := range out.Resources {
		documents = append(documents, &out.Resources[i].Properties)
	}
	for i := range out.Targets {
		target := &out.Targets[i]
		if len(target.Config) == 0 || string(target.Config) == "null" {
			if !target.ConfigSchema.IsZero() {
				return nil, fmt.Errorf("target %s has ConfigSchema without config", target.Label)
			}
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(target.Config, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("desired target config must be an object")
		}
		canonical := map[string]string{}
		for key := range fields {
			if key == "" || strings.ContainsAny(key, "`\r\n") {
				return nil, fmt.Errorf("unsupported desired target config key %q", key)
			}
			upper := []rune(key)
			upper[0] = unicode.ToUpper(upper[0])
			name := string(upper)
			if prior, ok := canonical[name]; ok && prior != key {
				return nil, fmt.Errorf("ambiguous desired target config schema keys %q and %q", prior, key)
			}
			canonical[name] = key
		}
		for key := range target.ConfigSchema.Hints {
			upper := []rune(key)
			if len(upper) == 0 || unicode.ToUpper(upper[0]) != upper[0] || strings.ContainsAny(key, "`\r\n") {
				return nil, fmt.Errorf("unsupported desired target ConfigSchema key %q", key)
			}
		}
		documents = append(documents, &target.Config)
	}
	for _, document := range documents {
		raw, err := preprocessEmbedInJSON(*document)
		if err != nil {
			return nil, err
		}
		var value any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		if err := walk(value); err != nil {
			return nil, err
		}
		*document, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	return &out, nil
}
