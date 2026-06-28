// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/canonicalize"
	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// EnforceSetOnceAndCompareResourceForUpdate prepares resources for update by applying transformations,
// normalization, and SetOnce filtering, then compares them to detect meaningful changes
func EnforceSetOnceAndCompareResourceForUpdate(existing, new *pkgmodel.Resource, schema pkgmodel.Schema) (bool, json.RawMessage, error) {
	filteredRawProps, err := filterSetOnceProps(existing.Properties, new.Properties, new.Label)
	if err != nil {
		return false, nil, err
	}

	// Hash the filtered properties ONLY for comparison (like verses like)
	transformer := transformations.NewPersistValueTransformer()
	tempResource := &pkgmodel.Resource{Properties: filteredRawProps}
	hashedForComparison, err := transformer.ApplyToResource(tempResource)
	if err != nil {
		return false, nil, err
	}

	// Canonicalize Format-hinted serialized fields on BOTH sides, for comparison
	// only — never feeds the returned filteredRawProps (PLA-196).
	existingForCompare := canonicalizeHintedFields(existing.Properties, schema)
	newForCompare := canonicalizeHintedFields(hashedForComparison.Properties, schema)

	equal, err := util.JsonEqualIgnoreArrayOrder(existingForCompare, newForCompare)
	if err != nil {
		return false, nil, fmt.Errorf("failed to compare properties: %w", err)
	}
	return !equal, filteredRawProps, nil
}

// canonicalizeHintedFields returns a copy of props in which each Format-hinted
// plain-String field is replaced by its canonical form, for comparison only. On
// any per-field error the field is left raw, so the comparison can only ever
// over-report a change, never under-report one. Uses string-returning sjson.Set
// so the input json.RawMessage is never mutated.
func canonicalizeHintedFields(props json.RawMessage, schema pkgmodel.Schema) json.RawMessage {
	formats := schema.FormatHints()
	if len(formats) == 0 || len(props) == 0 {
		return props
	}
	out := string(props)
	for field, format := range formats {
		// The hint key is used as a gjson path (dot = nesting). v1 targets
		// top-level fields whose names contain no gjson-special chars
		// (`.`/`*`/`?`); the grafana `configJson` consumer satisfies this. A
		// top-level field literally named with a `.` would not be matched
		// (canonicalization silently skipped — safe-direction, never drops a
		// real change).
		val := gjson.Get(out, field)
		if !val.Exists() || val.Type != gjson.String {
			continue // absent, or not a plain JSON string (e.g. a resolvable envelope)
		}
		canon, err := canonicalize.Canonicalize(format, val.String())
		if err != nil {
			slog.Warn("skipping canonicalization of hinted field", "field", field, "format", format, "error", err)
			continue
		}
		updated, err := sjson.Set(out, field, canon)
		if err != nil {
			slog.Warn("failed to write canonicalized hinted field", "field", field, "error", err)
			continue
		}
		out = updated
	}
	return json.RawMessage(out)
}

// SuppressUnchangedOpaqueValues removes opaque leaf properties whose desired
// value is unchanged from what is stored, from BOTH the existing (document) and
// desired (patch) property sets that feed patch generation.
//
// A surfaced opaque field is force-re-added by patch generation when it is
// writeOnly (the provider never returns it on Read, so it is absent from the
// document and jsonpatch emits an "add"), and re-diffed against its stored hash
// when it is not. Either way, an unrelated sibling edit re-emits the opaque
// field: as cleartext (churn — a needless new value/version) or, for a
// setOnce-frozen value, as its own stored hash (corruption). Dropping the
// unchanged opaque leaf from both sides makes it invisible to the diff, so no
// op is produced. Genuinely changed opaque values are left in place, so a real
// rotation still produces a patch op.
//
// "Unchanged" is decided exactly as the update gate decides whole-resource
// change: hash the desired (wrapped) properties with PersistValueTransformer and
// compare the resulting opaque $value to the stored $value. Keyed on
// $visibility == Opaque, so it covers opaque fields whether or not they are also
// writeOnly or createOnly, and never touches non-opaque fields. Inputs are the
// wrapped {$strategy,$visibility,$value} forms, so call this before
// ConvertToPluginFormat unwraps them. Inputs are not mutated; stripped copies
// are returned.
func SuppressUnchangedOpaqueValues(existing, desired json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	if len(existing) == 0 || len(desired) == 0 {
		return existing, desired, nil
	}

	// Hash the desired opaque values the same way the gate and persistence do,
	// so the comparison cannot drift from the gate's decision.
	transformer := transformations.NewPersistValueTransformer()
	hashed, err := transformer.ApplyToResource(&pkgmodel.Resource{Properties: desired})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to hash desired properties for opaque comparison: %w", err)
	}

	hashedResult := gjson.ParseBytes(hashed.Properties)
	existingResult := gjson.ParseBytes(existing)
	desiredResult := gjson.ParseBytes(desired)

	// Collect dotted paths of opaque leaves anywhere in the desired props,
	// recursing through both objects and arrays (PersistValueTransformer and
	// filterSetOnceProps hash/freeze opaque values nested under arrays too, so
	// they are equally subject to the churn this guards against).
	var opaquePaths []string
	var walk func(prefix string, node gjson.Result)
	walk = func(prefix string, node gjson.Result) {
		switch {
		case node.IsObject():
			if node.Get("$visibility").String() == "Opaque" {
				opaquePaths = append(opaquePaths, prefix)
				return
			}
			node.ForEach(func(key, val gjson.Result) bool {
				walk(buildPath(prefix, key.String()), val)
				return true
			})
		case node.IsArray():
			node.ForEach(func(idx, val gjson.Result) bool {
				walk(buildPath(prefix, idx.String()), val)
				return true
			})
		}
	}
	desiredResult.ForEach(func(key, val gjson.Result) bool {
		walk(key.String(), val)
		return true
	})

	strippedExisting := string(existing)
	strippedDesired := string(desired)
	for _, path := range opaquePaths {
		storedVal := existingResult.Get(path + ".$value")
		if !storedVal.Exists() {
			continue // first set: keep so the create op is emitted
		}
		if hashedResult.Get(path+".$value").String() != storedVal.String() {
			continue // changed: keep so the rotation emits an op
		}
		// Unchanged: drop from both sides so no add/replace/remove is produced.
		strippedDesired, err = sjson.Delete(strippedDesired, path)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to drop unchanged opaque path %q from desired: %w", path, err)
		}
		if existingResult.Get(path).Exists() {
			strippedExisting, err = sjson.Delete(strippedExisting, path)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to drop unchanged opaque path %q from existing: %w", path, err)
			}
		}
	}

	return json.RawMessage(strippedExisting), json.RawMessage(strippedDesired), nil
}

// filterSetOnceProps recursively removes SetOnce properties with existing values
func filterSetOnceProps(existing, new json.RawMessage, label string) (json.RawMessage, error) {
	if len(existing) == 0 || len(new) == 0 {
		return new, nil
	}

	existingResult := gjson.Parse(string(existing))
	newResult := gjson.Parse(string(new))
	filtered := string(new)

	// Recursively process all values in the new properties
	var processValue func(path string, newVal gjson.Result)
	processValue = func(path string, newVal gjson.Result) {
		// Get the corresponding existing value at this path
		existingVal := existingResult.Get(path)

		// Special handling for Tags arrays - match by Key instead of index
		if isTagArray(path, newVal) {
			filtered = processTagsArray(path, newVal, existingResult, filtered, label)
			return
		}

		// Check if this value should be preserved due to setOnce
		if shouldPreserveSetOnce(newVal, existingVal) {
			slog.Info("Preserving SetOnce property",
				"resource", label,
				"property", path,
				"value", getPreservedValueString(existingVal))

			var err error
			filtered, err = sjson.SetRaw(filtered, path, existingVal.Raw)
			if err != nil {
				slog.Error("Failed to preserve SetOnce property",
					"resource", label,
					"property", path,
					"error", err)
			}
			return
		}

		// Recurse into nested structures
		if newVal.IsObject() {
			newVal.ForEach(func(key, val gjson.Result) bool {
				processValue(buildPath(path, key.String()), val)
				return true
			})
		} else if newVal.IsArray() {
			newVal.ForEach(func(idx, val gjson.Result) bool {
				processValue(buildPath(path, idx.String()), val)
				return true
			})
		}
	}

	// Start processing from the root
	newResult.ForEach(func(key, val gjson.Result) bool {
		processValue(key.String(), val)
		return true
	})

	return json.RawMessage(filtered), nil
}

// isTagArray checks if this is a Tags array (array of objects with Key/Value fields)
func isTagArray(path string, val gjson.Result) bool {
	if !strings.Contains(path, "Tags") || !val.IsArray() {
		return false
	}

	arr := val.Array()
	if len(arr) == 0 {
		return false
	}

	firstTag := arr[0]
	return firstTag.IsObject() &&
		firstTag.Get("Key").Exists() &&
		firstTag.Get("Value").Exists()
}

// processTagsArray handles Tags arrays by matching tags by Key field
func processTagsArray(path string, newTags gjson.Result, existingResult gjson.Result, filtered string, label string) string {
	existingTagsPath := path
	existingTags := existingResult.Get(existingTagsPath)

	if !existingTags.Exists() || !existingTags.IsArray() {
		return filtered
	}

	// Build a map of existing tags by Key for fast lookup
	existingTagMap := make(map[string]gjson.Result)
	existingTags.ForEach(func(_, tag gjson.Result) bool {
		if tag.IsObject() {
			key := tag.Get("Key").String()
			if key != "" {
				existingTagMap[key] = tag
			}
		}
		return true
	})

	// Process each new tag
	newTags.ForEach(func(idx, newTag gjson.Result) bool {
		if !newTag.IsObject() {
			return true
		}

		tagKey := newTag.Get("Key").String()
		tagValue := newTag.Get("Value")

		// Check if the Value has setOnce strategy
		if tagValue.IsObject() && tagValue.Get("$strategy").String() == "SetOnce" {
			if existingTag, found := existingTagMap[tagKey]; found {
				existingValue := existingTag.Get("Value")

				if shouldPreserveSetOnce(tagValue, existingValue) {
					slog.Debug("Preserving SetOnce tag",
						"resource", label,
						"tag", tagKey,
						"value", getPreservedValueString(existingValue))

					tagPath := fmt.Sprintf("%s.%s.Value", path, idx.String())
					var err error
					filtered, err = sjson.SetRaw(filtered, tagPath, existingValue.Raw)
					if err != nil {
						slog.Error("Failed to preserve SetOnce tag",
							"resource", label,
							"tag", tagKey,
							"error", err)
					}
				}
			}
		}

		return true
	})

	return filtered
}

// shouldPreserveSetOnce checks if a SetOnce property should preserve its existing value
func shouldPreserveSetOnce(newProp, existingProp gjson.Result) bool {
	if !newProp.IsObject() {
		return false
	}

	if newProp.Get("$strategy").String() != "SetOnce" {
		return false
	}

	if !existingProp.Exists() {
		return false
	}

	// If existing is a Value object with setOnce, preserve it
	if existingProp.IsObject() {
		existingVal := existingProp.Get("$value")
		return existingVal.Exists() && existingVal.Value() != nil
	}

	// If existing is a plain value (string, number, etc.) and new has setOnce,
	// preserve the existing plain value by not returning true here.
	// We want to preserve ANY existing value when setOnce is present.
	// This handles the transition from plain values to setOnce values.
	return existingProp.Value() != nil
}

// getPreservedValueString extracts the display value from a gjson.Result for logging
func getPreservedValueString(val gjson.Result) string {
	if val.IsObject() {
		return val.Get("$value").String()
	}
	return val.String()
}

// buildPath constructs a property path, handling empty root paths
func buildPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}
