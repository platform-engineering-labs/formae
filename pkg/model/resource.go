// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/theory/jsonpath"
	"github.com/theory/jsonpath/registry"
	"github.com/tidwall/gjson"
)

// jsonpathParser is a package-level parser with RFC 9535 function extensions
var jsonpathParser = jsonpath.NewParser(jsonpath.WithRegistry(registry.New()))

type Resource struct {
	Label              string          `json:"Label"`
	Group              string          `json:"Group,omitempty"`
	Type               string          `json:"Type"`
	Stack              string          `json:"Stack"`
	Target             string          `json:"Target"`
	Schema             Schema          `json:"Schema"`
	Properties         json.RawMessage `json:"Properties"`
	ReadOnlyProperties json.RawMessage `json:"ReadOnlyProperties,omitempty"`
	PatchDocument      json.RawMessage `json:"PatchDocument,omitempty"` // Need this for CLI patch display
	NativeID           string          `json:"NativeID,omitempty"`
	Managed            bool            `json:"Managed,omitempty"` // Whether the resource is managed by Formae or not
	Ksuid              string          `json:"Ksuid,omitempty"`
	Alias              *Alias          `json:"Alias,omitempty"` // RFC-0041 rename alias (previous identity)
}

// TupleKey returns the lookup key for this resource in the format: type/stack/label
// This is used for reference resolution and replaces FormaeURI usage
func (r *Resource) TupleKey() string {
	return fmt.Sprintf("%s/%s/%s", strings.ToLower(r.Fqn()), r.Stack, r.Label)
}

func (r *Resource) URI() FormaeURI {
	return NewFormaeURI(r.Ksuid, "")
}

func (r *Resource) Fqn() string {
	frags := strings.Split(r.Type, "::")
	slices.Reverse(frags)
	return strings.Join(frags, ".")
}

func (r *Resource) Namespace() string {
	frags := strings.Split(r.Type, "::")

	return frags[0]
}

func (r *Resource) GetPropertyJSONPath(query string) (string, bool) {
	// First try to get from Properties
	if val, found := r.getPropertyFromJSON(query, r.Properties); found {
		return val, true
	}

	// Then try to get from ReadOnlyProperties
	if len(r.ReadOnlyProperties) > 0 {
		if val, found := r.getPropertyFromJSON(query, r.ReadOnlyProperties); found {
			return val, true
		}
	}

	return "", false
}

// GetProperty retrieves a property value from the resource's Properties field using a query path.
// Returns the property value as a string and a boolean indicating whether the property was found.
// Note: null values are treated as not found.
func (r *Resource) GetProperty(query string) (string, bool) {
	value := r.getProperty(query)
	if !value.Exists() || value.Type == gjson.Null {
		return "", false
	}
	return value.String(), true
}

// GetEffectivePropertyValue returns the scalar string value of a property,
// transparently unwrapping the resolved-reference shape ({"$ref": "...",
// "$value": "..."}) to its $value.
//
// Use this when a downstream consumer needs the producer-supplied value
// regardless of whether the property is still wrapped in a Resolvable
// post-apply.
//
// Returns false when:
//   - the property does not exist,
//   - the property is a resolved-reference whose $value has not been
//     populated yet (still-pending resolution),
//   - the property is an object that is neither a scalar nor a resolved
//     reference (e.g. a nested sub-resource — those need direct gjson).
func (r *Resource) GetEffectivePropertyValue(query string) (string, bool) {
	value := r.getProperty(query)
	if !value.Exists() || value.Type == gjson.Null {
		return "", false
	}
	if ref, ok := AsResolvedReference(value); ok {
		if !ref.Value.Exists() || ref.Value.Type == gjson.Null {
			return "", false
		}
		return ref.Value.String(), true
	}
	if value.IsObject() {
		// Non-resolvable object — not a scalar.
		return "", false
	}
	return value.String(), true
}

// GetPropertyReference returns the $ref URI of a property when the property
// carries the resolved-reference shape ({"$ref": "...", ...}), regardless of
// whether $value has been populated yet.
//
// Returns false for literal values, non-object values, and objects without a
// $ref key.
func (r *Resource) GetPropertyReference(query string) (FormaeURI, bool) {
	ref, ok := AsResolvedReference(r.getProperty(query))
	if !ok {
		return "", false
	}
	return ref.Ref, true
}

func (r *Resource) ValidateRequiredOnCreateFields() error {
	missingFields := r.GetMissingRequiredOnCreateFields()

	if len(missingFields) > 0 {
		return fmt.Errorf("resource %s of type %s cannot be created - missing required fields: %v. Please provide values for these fields before creating the resource",
			r.Label, r.Type, missingFields)
	}

	return nil
}

func (r *Resource) GetMissingRequiredOnCreateFields() []string {
	requiredOnCreateFields := r.Schema.RequiredOnCreate()
	if len(requiredOnCreateFields) == 0 {
		return nil
	}

	var missingFields []string
	for _, field := range requiredOnCreateFields {
		value := r.getProperty(field)
		if !value.Exists() || value.Type == gjson.Null || (value.Type == gjson.String && value.Str == "") {
			missingFields = append(missingFields, field)
		}
	}

	return missingFields
}

func (r *Resource) getPropertyFromJSON(query string, properties json.RawMessage) (string, bool) {
	var data any
	if err := json.Unmarshal(properties, &data); err != nil {
		slog.Error("failed to unmarshal properties", "error", err)
		return "", false
	}
	// Normalize simple field names to JSONPath syntax for backward compatibility
	// e.g., "Key1" becomes "$.Key1"
	if !strings.HasPrefix(query, "$") {
		query = "$." + query
	}
	path, err := jsonpathParser.Parse(query)
	if err != nil {
		slog.Error("failed to parse jsonpath query", "query", query, "error", err)
		return "", false
	}
	nodes := path.Select(data)
	if len(nodes) == 0 {
		return "", false
	}
	// Return the first result as a string
	if strVal, ok := nodes[0].(string); ok {
		return strVal, true
	}
	return fmt.Sprintf("%v", nodes[0]), true
}

func (r *Resource) getProperty(query string) gjson.Result {
	// First try to get from Properties
	result := gjson.Get(string(r.Properties), query)
	if result.Exists() {
		return result
	}

	// Then try to get from ReadOnlyProperties
	if len(r.ReadOnlyProperties) > 0 {
		return gjson.Get(string(r.ReadOnlyProperties), query)
	}

	return gjson.Result{}
}
