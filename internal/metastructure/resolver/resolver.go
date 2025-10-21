// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resolver

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ResolvePropertyReferences resolves a specific reference in resource properties
func ResolvePropertyReferences(ksuidUri pkgmodel.FormaeURI, properties json.RawMessage, value string) (json.RawMessage, error) {
	resolver := newPropertyResolver(properties)
	if resolver.has(ksuidUri) {
		if err := resolver.setRefValue(ksuidUri, value); err != nil {
			return nil, fmt.Errorf("failed to set property value for %s: %w", ksuidUri, err)
		}
	} else {
		slog.Error("Failed to set property value", "uri", ksuidUri)
		return nil, fmt.Errorf("failed to set property value for %s", ksuidUri)
	}
	return resolver.resolveReferences(properties)
}

// ConvertToPluginFormat converts properties to the format expected by cloud provider plugins
func ConvertToPluginFormat(properties json.RawMessage) (json.RawMessage, error) {
	resolver := newPropertyResolver(properties)
	resolved, err := resolver.resolveReferences(properties)
	if err != nil {
		slog.Error("Failed to resolve property references", "error", err)
		return nil, err
	}
	return resolver.toPluginFormat(resolved)
}

// ExtractResolvableURIs extracts all resolvable URIs from a resource
func ExtractResolvableURIs(resource pkgmodel.Resource) []pkgmodel.FormaeURI {
	resolver := newPropertyResolverFromResource(resource)
	return resolver.getResolvableURIs()
}

// propertyParser parses JSON properties to identify references and values
type propertyParser struct {
	HasRef    bool
	HasValue  bool
	Reference string // The $ref value
	Value     any
}

// propertyType defines the type of property being parsed
type propertyType int

const (
	typeReference propertyType = iota // Object with $ref (with or without $value)
	typeValue                         // Object with only $value (no $ref)
	typePlain                         // Regular property (no $ref or $value)
)

func (pp *propertyParser) Parse(result gjson.Result) propertyType {
	pp.HasRef = result.Get("$ref").Exists()
	pp.HasValue = result.Get("$value").Exists()

	if pp.HasRef {
		pp.Reference = result.Get("$ref").String()
		if pp.HasValue {
			pp.Value = result.Get("$value").Value()
		}
		return typeReference
	}

	if pp.HasValue {
		pp.Value = result.Get("$value").Value()
		return typeValue
	}

	return typePlain
}

func (pp *propertyParser) CreateRef(currentPath string, result gjson.Result) pkgmodel.Ref {
	uri := pkgmodel.FormaeURI(pp.Reference) // Parse as KSUID URI

	var rawValue pkgmodel.Value
	if pp.HasValue {
		rawValue = pkgmodel.Value{
			Strategy:   result.Get("$strategy").String(),
			Visibility: result.Get("$visibility").String(),
			Value:      pp.Value,
		}
	} else {
		// Even without a value, we might have strategy and visibility
		rawValue = pkgmodel.Value{
			Strategy:   result.Get("$strategy").String(),
			Visibility: result.Get("$visibility").String(),
		}
	}

	return pkgmodel.Ref{
		PropertyURI:        pp.Reference,
		ResourceURI:        uri.Stripped(),
		SourcePropertyName: uri.PropertyPath(),
		TargetPath:         currentPath,
		ResolvedValue:      rawValue,
	}
}

func (pp *propertyParser) CreateValue(result gjson.Result) *pkgmodel.Value {
	value := &pkgmodel.Value{
		Strategy:   result.Get("$strategy").String(),
		Visibility: result.Get("$visibility").String(),
	}

	if pp.HasValue {
		value.Value = pp.Value
	}

	return value
}

// propertyResolver handles resolution of property references and values
type propertyResolver struct {
	values map[string]pkgmodel.Value
	refs   map[pkgmodel.FormaeURI]pkgmodel.Ref
}

func newPropertyResolver(properties json.RawMessage) *propertyResolver {
	resolver := &propertyResolver{
		values: make(map[string]pkgmodel.Value),
		refs:   make(map[pkgmodel.FormaeURI]pkgmodel.Ref),
	}

	result := gjson.Parse(string(properties))
	resolver.extractFromJson(result, "")

	return resolver
}

func newPropertyResolverFromResource(resource pkgmodel.Resource) *propertyResolver {
	resolver := &propertyResolver{
		values: make(map[string]pkgmodel.Value),
		refs:   make(map[pkgmodel.FormaeURI]pkgmodel.Ref),
	}

	if resource.Properties != nil {
		result := gjson.Parse(string(resource.Properties))
		resolver.extractFromJson(result, "")
	}

	if resource.ReadOnlyProperties != nil {
		result := gjson.Parse(string(resource.ReadOnlyProperties))
		resolver.extractFromJson(result, "")
	}

	return resolver
}

// Helper function for building paths
func buildPath(currentPath, key string) string {
	if currentPath == "" {
		return key
	}
	return currentPath + "." + key
}

func (pr *propertyResolver) marshalWithLogging(value any, context string, path string) ([]byte, error) {
	result, err := json.Marshal(value)
	if err != nil {
		slog.Error("Failed to marshal value",
			"context", context,
			"path", path,
			"value", value,
			"error", err)
	}
	return result, err
}

func (pr *propertyResolver) extractFromJson(result gjson.Result, currentPath string) {
	if result.IsObject() {
		parser := &propertyParser{}
		propertyType := parser.Parse(result)

		switch propertyType {
		case typeReference:
			ref := parser.CreateRef(currentPath, result)
			pr.refs[pkgmodel.FormaeURI(ref.PropertyURI)] = ref
			return
		case typeValue:
			value := parser.CreateValue(result)
			pr.values[currentPath] = *value
			return
		case typePlain:
			result.ForEach(func(key, val gjson.Result) bool {
				pr.extractFromJson(val, buildPath(currentPath, key.String()))
				return true
			})
		}
	} else if result.IsArray() {
		result.ForEach(func(key, val gjson.Result) bool {
			pr.extractFromJson(val, buildPath(currentPath, key.String()))
			return true
		})
	}
}

func (pr *propertyResolver) extractResolvedValue(ref pkgmodel.Ref) any {
	if ref.ResolvedValue.Value == nil {
		return nil
	}

	resolvedJSON, ok := ref.ResolvedValue.Value.(string)
	if ok {
		resolvedData := gjson.Parse(resolvedJSON)

		if resolvedData.Get("$value").Exists() {
			return resolvedData.Get("$value").Value()
		}

		specificProperty := resolvedData.Get(ref.SourcePropertyName)
		if specificProperty.Exists() {
			return specificProperty.Value()
		}
	}

	return ref.ResolvedValue.Value
}

func (pr *propertyResolver) resolveReferences(properties json.RawMessage) (json.RawMessage, error) {
	result := properties

	for _, ref := range pr.refs {
		if ref.ResolvedValue.Value == nil {
			slog.Debug("No resolved value set for ref", "uri", ref.ResourceURI, "targetPath", ref.TargetPath)
			continue
		}

		updated, err := pr.resolveReference(result, ref)
		if err != nil {
			return nil, err
		}
		result = updated
	}

	return result, nil
}

// resolveReference resolves a single reference in the properties
func (pr *propertyResolver) resolveReference(properties json.RawMessage, ref pkgmodel.Ref) (json.RawMessage, error) {
	parsed := gjson.Parse(string(properties))
	targetObj := parsed.Get(ref.TargetPath)
	if !targetObj.Exists() {
		slog.Debug("Target object not found", "path", ref.TargetPath)
		return properties, nil
	}

	refObject := make(map[string]any)
	targetObj.ForEach(func(key, value gjson.Result) bool {
		refObject[key.String()] = value.Value()
		return true
	})

	valueToSet := pr.extractResolvedValue(ref)
	if valueToSet != nil {
		refObject["$value"] = valueToSet
	}
	if ref.ResolvedValue.Strategy != "" {
		refObject["$strategy"] = ref.ResolvedValue.Strategy
	}
	if ref.ResolvedValue.Visibility != "" {
		refObject["$visibility"] = ref.ResolvedValue.Visibility
	}

	marshalledObj, err := pr.marshalWithLogging(refObject, "reference resolution", ref.TargetPath)
	if err != nil {
		return properties, err
	}

	updatedJson, err := sjson.SetRaw(string(properties), ref.TargetPath, string(marshalledObj))
	if err != nil {
		slog.Error("Failed to set reference object", "path", ref.TargetPath, "error", err)
		return properties, err
	}

	return json.RawMessage(updatedJson), nil
}

func (pr *propertyResolver) setRefValue(uri pkgmodel.FormaeURI, value string) error {
	var actualValue string
	var inheritedVisibility, inheritedStrategy string

	if parsed := gjson.Parse(value); parsed.IsObject() {
		if parsed.Get("$value").Exists() {
			actualValue = parsed.Get("$value").String()
		} else {
			actualValue = value
		}

		inheritedVisibility = parsed.Get("$visibility").String()
		inheritedStrategy = parsed.Get("$strategy").String()
	} else {
		actualValue = value
	}

	var found bool
	for propertyURI, ref := range pr.refs {
		if propertyURI == uri {
			if ref.ResolvedValue.IsSetOnce() && ref.ResolvedValue.Value != nil {
				slog.Debug("Skipping update to SetOnce property that already has a value",
					"uri", uri,
					"targetPath", ref.TargetPath)
				found = true
				continue
			}

			newValue := pkgmodel.Value{Value: actualValue}

			if ref.ResolvedValue.Strategy != "" {
				newValue.Strategy = ref.ResolvedValue.Strategy
			} else if inheritedStrategy != "" {
				newValue.Strategy = inheritedStrategy
			}

			if inheritedVisibility == "Opaque" {
				newValue.Visibility = "Opaque"
			} else if ref.ResolvedValue.Visibility != "" {
				newValue.Visibility = ref.ResolvedValue.Visibility
			}

			ref.ResolvedValue = newValue
			pr.refs[propertyURI] = ref
			found = true
		}
	}

	if !found {
		return fmt.Errorf("reference not found: %s", uri)
	}

	return nil
}

func (pr *propertyResolver) has(uri pkgmodel.FormaeURI) bool {
	_, exists := pr.refs[uri]
	return exists
}

func (pr *propertyResolver) isTargetPath(path string) bool {
	for _, ref := range pr.refs {
		if ref.TargetPath == path {
			return true
		}
	}
	return false
}

// toPluginFormat extracts the $value from resolved properties for plugin consumption
func (pr *propertyResolver) toPluginFormat(originalProperties json.RawMessage) (json.RawMessage, error) {
	outputJsonString := string(originalProperties)
	var err error

	for _, ref := range pr.refs {
		if ref.ResolvedValue.Value == nil {
			continue
		}

		finalValueToSet := pr.extractResolvedValue(ref)
		if finalValueToSet == nil {
			continue
		}

		outputJsonString, err = sjson.Set(outputJsonString, ref.TargetPath, finalValueToSet)
		if err != nil {
			slog.Error("ToPluginFormat: failed to set value for ref",
				"path", ref.TargetPath,
				"error", err)
			return nil, err
		}
	}

	for path, value := range pr.values {
		if pr.isTargetPath(path) {
			continue
		}

		if value.Value != nil {
			outputJsonString, err = sjson.Set(outputJsonString, path, value.Value)
			if err != nil {
				slog.Error("ToPluginFormat: failed to set value",
					"path", path,
					"error", err)
				return nil, err
			}
		}
	}

	return json.RawMessage(outputJsonString), nil
}

func (pr *propertyResolver) getResolvableURIs() []pkgmodel.FormaeURI {
	uris := make([]pkgmodel.FormaeURI, 0, len(pr.refs))
	for _, ref := range pr.refs {
		uris = append(uris, pkgmodel.FormaeURI(ref.PropertyURI))
	}
	return uris
}
