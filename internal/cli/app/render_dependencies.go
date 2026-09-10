// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package app

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Resource references in target config or embedded templates can import a
// namespace even when the selected complete stack contains zero resources.
func renderNamespaces(forma *pkgmodel.Forma) ([]string, error) {
	found := map[string]bool{}
	add := func(typ string) {
		ns := strings.ToLower((&pkgmodel.Resource{Type: typ}).Namespace())
		if ns != "" {
			found[ns] = true
		}
	}
	var walk func(any) error
	decode := func(raw []byte) error {
		if len(raw) == 0 {
			return nil
		}
		var value any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&value); err != nil {
			return err
		}
		return walk(value)
	}
	walk = func(value any) error {
		switch node := value.(type) {
		case []any:
			for _, v := range node {
				if err := walk(v); err != nil {
					return err
				}
			}
		case map[string]any:
			if node["$res"] == true {
				if typ, ok := node["$type"].(string); ok {
					add(typ)
				}
			}
			if node["$embed"] == true {
				if template, ok := node["$template"].(string); ok {
					spans, err := pkgmodel.ScanEmbedSpans(template)
					if err != nil {
						return err
					}
					for _, span := range spans {
						if err := decode([]byte(span.EnvelopeJSON)); err != nil {
							return err
						}
					}
				}
			}
			for _, v := range node {
				if err := walk(v); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, r := range forma.Resources {
		add(r.Type)
		if err := decode(r.Properties); err != nil {
			return nil, err
		}
	}
	for _, target := range forma.Targets {
		if err := decode(target.Config); err != nil {
			return nil, err
		}
	}
	namespaces := make([]string, 0, len(found))
	for ns := range found {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	return namespaces, nil
}
