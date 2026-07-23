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
	if err := guardNoHashedValues(properties); err != nil {
		return nil, err
	}
	resolver := newPropertyResolver(properties)
	resolved, err := resolver.resolveReferences(properties)
	if err != nil {
		slog.Error("Failed to resolve property references", "error", err)
		return nil, err
	}
	return resolver.toPluginFormat(resolved)
}

// guardNoHashedValues rejects any property carrying a $hashed:true marker.
// Hashed values are terminal: once a secret has been hashed at rest, its
// stored digest must never be sent to a plugin as if it were the live
// secret value.
func guardNoHashedValues(properties json.RawMessage) error {
	if len(properties) == 0 {
		return nil
	}
	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return nil // malformed here is handled elsewhere; guard only checks structure it can read
	}
	return scanHashed(props, "")
}

func scanHashed(v any, path string) error {
	switch val := v.(type) {
	case map[string]any:
		if h, ok := val["$hashed"].(bool); ok && h {
			return fmt.Errorf("refusing to send hashed secret at %q to plugin: hashed values are terminal (needs live resolution)", path)
		}
		for k, child := range val {
			if err := scanHashed(child, path+"/"+k); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range val {
			if err := scanHashed(child, fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ExtractResolvableURIs extracts all resolvable URIs from a resource
func ExtractResolvableURIs(resource pkgmodel.Resource) []pkgmodel.FormaeURI {
	resolver := newPropertyResolverFromResource(resource)
	return resolver.getResolvableURIs()
}

// stringifyValue best-effort stringifies a resolvable's cached $value
// payload (any) into a display string. Returns "" when the value is nil.
// Maps strings to themselves, scalars via fmt, and everything else via
// JSON marshalling so renderers can show concrete user-facing strings.
func stringifyValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	switch v := v.(type) {
	case bool, int, int32, int64, uint, uint32, uint64, float32, float64:
		return fmt.Sprintf("%v", v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// ResolvableRef pairs a resolvable's target resource URI with the field-path
// at which the reference appears in the consuming resource. Consumers that
// need to look up per-field schema hints use this instead of ExtractResolvableURIs.
type ResolvableRef struct {
	URI                pkgmodel.FormaeURI // resource being referenced (no fragment)
	TargetPath         string             // dot-separated path within the consuming resource
	SourcePropertyName string             // property name on the referenced resource (e.g. "TaskDefinitionArn")
	CurrentValue       string             // the cached $value at the consuming TargetPath, or "" if absent
}

// ExtractResolvableRefs returns every resolvable in the resource along with
// the path at which it sits. Supersedes ExtractResolvableURIs for callers that
// need TargetPath; the URI-only helper is retained for existing callers.
func ExtractResolvableRefs(resource pkgmodel.Resource) []ResolvableRef {
	pr := newPropertyResolverFromResource(resource)
	out := make([]ResolvableRef, 0, len(pr.refs))
	for _, refs := range pr.refs {
		for _, ref := range refs {
			// Construct URI without fragment (just scheme://ksuid)
			uri := pkgmodel.FormaeURI(fmt.Sprintf("formae://%s", ref.ResourceURI.KSUID()))
			out = append(out, ResolvableRef{
				URI:                uri,
				TargetPath:         ref.TargetPath,
				SourcePropertyName: ref.SourcePropertyName,
				CurrentValue:       stringifyValue(ref.ResolvedValue.Value),
			})
		}
	}
	return out
}

// ExtractResolvableURIsFromJSON extracts all resolvable URIs from raw JSON.
// Used for target Config fields.
func ExtractResolvableURIsFromJSON(data json.RawMessage) []pkgmodel.FormaeURI {
	if data == nil {
		return nil
	}
	resolver := newPropertyResolver(data)
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
	typeEmbed                         // Object with $embed: true — template with framed spans
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

	if result.Get("$embed").Bool() {
		return typeEmbed
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
	refs   map[pkgmodel.FormaeURI][]pkgmodel.Ref
}

func newPropertyResolver(properties json.RawMessage) *propertyResolver {
	resolver := &propertyResolver{
		values: make(map[string]pkgmodel.Value),
		refs:   make(map[pkgmodel.FormaeURI][]pkgmodel.Ref),
	}

	result := gjson.Parse(string(properties))
	resolver.extractFromJson(result, "")

	return resolver
}

func newPropertyResolverFromResource(resource pkgmodel.Resource) *propertyResolver {
	resolver := &propertyResolver{
		values: make(map[string]pkgmodel.Value),
		refs:   make(map[pkgmodel.FormaeURI][]pkgmodel.Ref),
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
			uri := pkgmodel.FormaeURI(ref.PropertyURI)
			pr.refs[uri] = append(pr.refs[uri], ref)
			return
		case typeValue:
			value := parser.CreateValue(result)
			pr.values[currentPath] = *value
			return
		case typeEmbed:
			tmpl := result.Get("$template").String()
			if tmpl == "" {
				slog.Debug("embed: $template absent or empty, skipping extraction", "path", currentPath)
				return
			}
			spans, err := pkgmodel.ScanEmbedSpans(tmpl)
			if err != nil {
				slog.Warn("embed: failed to scan spans in $template, skipping extraction",
					"path", currentPath,
					"error", err)
				return
			}
			for _, sp := range spans {
				env := gjson.Parse(sp.EnvelopeJSON)
				spanParser := &propertyParser{}
				spanParser.Parse(env)
				if !spanParser.HasRef {
					slog.Debug("embed: span has no $ref, skipping", "path", currentPath)
					continue
				}
				ref := spanParser.CreateRef(currentPath, env)
				ref.Embedded = true
				ref.EmbedFieldPath = currentPath
				uri := pkgmodel.FormaeURI(ref.PropertyURI)
				pr.refs[uri] = append(pr.refs[uri], ref)
			}
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
			extracted := resolvedData.Get("$value")
			if extracted.IsObject() || extracted.IsArray() {
				return json.RawMessage(extracted.Raw)
			}
			return extracted.Value()
		}

		specificProperty := resolvedData.Get(ref.SourcePropertyName)
		if specificProperty.Exists() {
			if specificProperty.IsObject() || specificProperty.IsArray() {
				return json.RawMessage(specificProperty.Raw)
			}
			return specificProperty.Value()
		}

		// Value is itself a JSON object/array (e.g., an endpoints mapping).
		// Return as json.RawMessage to prevent double-encoding by json.Marshal.
		if resolvedData.IsObject() || resolvedData.IsArray() {
			return json.RawMessage(resolvedJSON)
		}
	}

	return ref.ResolvedValue.Value
}

func (pr *propertyResolver) resolveReferences(properties json.RawMessage) (json.RawMessage, error) {
	result := properties

	for _, refs := range pr.refs {
		for _, ref := range refs {
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
	}

	return result, nil
}

// assembleEmbedTemplate iterates over every framed span in tmpl, calling
// valueForEnvelope(envelopeJSON) to obtain the replacement string for that span.
// It returns the assembled string (every span replaced) and whether ALL spans
// had a value. Processing is done in reverse byte order so earlier offsets remain
// valid after later splices.
func assembleEmbedTemplate(tmpl string, valueForEnvelope func(envJSON string) (string, bool)) (string, bool) {
	spans, err := pkgmodel.ScanEmbedSpans(tmpl)
	if err != nil || len(spans) == 0 {
		return tmpl, len(spans) == 0 && err == nil
	}

	// Verify all spans are resolved before doing any work; short-circuit on the
	// first unresolved span so no partial string is produced.
	vals := make([]string, len(spans))
	for i, sp := range spans {
		val, ok := valueForEnvelope(sp.EnvelopeJSON)
		if !ok {
			return "", false
		}
		vals[i] = val
	}
	// All spans resolved — splice in reverse order so earlier offsets stay valid.
	result := tmpl
	for i := len(spans) - 1; i >= 0; i-- {
		sp := spans[i]
		result = result[:sp.Start] + vals[i] + result[sp.End:]
	}
	return result, true
}

// resolveEmbedRef writes the resolved $value for ref inside the matching span
// envelope(s) within the $embed field's $template. The $embed field remains
// structured (still {$embed, $template}); only the span envelopes are updated.
func (pr *propertyResolver) resolveEmbedRef(properties json.RawMessage, ref pkgmodel.Ref) (json.RawMessage, error) {
	fieldPath := ref.EmbedFieldPath
	parsed := gjson.Parse(string(properties))
	embedObj := parsed.Get(fieldPath)
	if !embedObj.Exists() {
		slog.Debug("embed: field not found", "path", fieldPath)
		return properties, nil
	}

	tmpl := embedObj.Get("$template").String()
	if tmpl == "" {
		return properties, nil
	}

	valueToSet := pr.extractResolvedValue(ref)
	valueStr, ok := valueToSet.(string)
	if !ok || valueStr == "" {
		if valueToSet != nil {
			valueStr = fmt.Sprintf("%v", valueToSet)
			ok = true
		}
	}
	if !ok {
		return properties, nil
	}

	// For each span whose envelope URI matches this ref's PropertyURI, encode the
	// envelope with $value added, then splice back (reverse order).
	spans, err := pkgmodel.ScanEmbedSpans(tmpl)
	if err != nil {
		return properties, fmt.Errorf("embed: scan spans for %s: %w", fieldPath, err)
	}

	updatedTmpl := tmpl
	for i := len(spans) - 1; i >= 0; i-- {
		sp := spans[i]
		env := gjson.Parse(sp.EnvelopeJSON)
		if env.Get("$ref").String() != ref.PropertyURI {
			continue
		}
		// Build updated envelope with $value.
		envMap := make(map[string]any)
		env.ForEach(func(k, v gjson.Result) bool {
			envMap[k.String()] = v.Value()
			return true
		})
		envMap["$value"] = valueStr
		envJSON, err := json.Marshal(envMap)
		if err != nil {
			return properties, fmt.Errorf("embed: marshal updated envelope: %w", err)
		}
		framed := pkgmodel.FrameEnvelope(string(envJSON))
		updatedTmpl = updatedTmpl[:sp.Start] + framed + updatedTmpl[sp.End:]
	}

	if updatedTmpl == tmpl {
		// No span matched — nothing to update.
		return properties, nil
	}

	// Write the updated $embed object back (preserving $embed:true and updated $template).
	embedMap := map[string]any{
		"$embed":    true,
		"$template": updatedTmpl,
	}
	embedJSON, err := json.Marshal(embedMap)
	if err != nil {
		return properties, fmt.Errorf("embed: marshal updated embed object: %w", err)
	}
	updatedJSON, err := sjson.SetRaw(string(properties), fieldPath, string(embedJSON))
	if err != nil {
		return properties, fmt.Errorf("embed: set embed field %s: %w", fieldPath, err)
	}
	return json.RawMessage(updatedJSON), nil
}

// resolveReference resolves a single reference in the properties
func (pr *propertyResolver) resolveReference(properties json.RawMessage, ref pkgmodel.Ref) (json.RawMessage, error) {
	// Embedded refs live inside a $embed field's $template; update the span envelope
	// in place rather than replacing the whole field with a scalar.
	if ref.Embedded {
		return pr.resolveEmbedRef(properties, ref)
	}

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

	refs, exists := pr.refs[uri]
	if !exists || len(refs) == 0 {
		return fmt.Errorf("reference not found: %s", uri)
	}

	// Update ALL refs for this URI (same URI can appear in multiple paths)
	for i := range refs {
		ref := &refs[i] // Use pointer to modify in place
		if ref.ResolvedValue.IsSetOnce() && ref.ResolvedValue.Value != nil {
			slog.Debug("Skipping update to SetOnce property that already has a value",
				"uri", uri,
				"targetPath", ref.TargetPath)
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
	}

	return nil
}

func (pr *propertyResolver) has(uri pkgmodel.FormaeURI) bool {
	_, exists := pr.refs[uri]
	return exists
}

func (pr *propertyResolver) isTargetPath(path string) bool {
	for _, refs := range pr.refs {
		for _, ref := range refs {
			if ref.TargetPath == path {
				return true
			}
		}
	}
	return false
}

// toPluginFormat extracts the $value from resolved properties for plugin consumption
func (pr *propertyResolver) toPluginFormat(originalProperties json.RawMessage) (json.RawMessage, error) {
	outputJsonString := string(originalProperties)
	var err error

	// Track embed field paths that have been assembled so we process each once.
	assembledEmbedFields := make(map[string]bool)

	for _, refs := range pr.refs {
		for _, ref := range refs {
			if ref.Embedded {
				// Embed fields are assembled from the $template, not from a single ref value.
				// Process each embed field path exactly once.
				if assembledEmbedFields[ref.EmbedFieldPath] {
					continue
				}
				assembledEmbedFields[ref.EmbedFieldPath] = true

				assembled, ok := pr.assembleEmbedField(outputJsonString, ref.EmbedFieldPath)
				if !ok {
					// Not all spans resolved; leave the field structured.
					continue
				}
				outputJsonString, err = sjson.Set(outputJsonString, ref.EmbedFieldPath, assembled)
				if err != nil {
					slog.Error("ToPluginFormat: failed to set assembled embed field",
						"path", ref.EmbedFieldPath,
						"error", err)
					return nil, err
				}
				continue
			}

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

// assembleEmbedField reads the $embed field at fieldPath from the JSON string,
// scans its $template for framed spans, and assembles the plain string if every
// span carries a $value. Returns the assembled string and true when all spans are
// resolved; returns "", false otherwise (including parse errors).
func (pr *propertyResolver) assembleEmbedField(jsonStr string, fieldPath string) (string, bool) {
	embedObj := gjson.Get(jsonStr, fieldPath)
	if !embedObj.Exists() {
		return "", false
	}
	tmpl := embedObj.Get("$template").String()
	if tmpl == "" {
		return "", false
	}

	assembled, allResolved := assembleEmbedTemplate(tmpl, func(envJSON string) (string, bool) {
		env := gjson.Parse(envJSON)
		val := env.Get("$value")
		if !val.Exists() {
			return "", false
		}
		return val.String(), true
	})
	return assembled, allResolved
}

func (pr *propertyResolver) getResolvableURIs() []pkgmodel.FormaeURI {
	uris := make([]pkgmodel.FormaeURI, 0, len(pr.refs))
	for uri := range pr.refs {
		uris = append(uris, uri)
	}
	return uris
}
