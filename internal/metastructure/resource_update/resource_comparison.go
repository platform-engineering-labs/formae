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

	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// EnforceSetOnceAndCompareResourceForUpdate prepares resources for update by applying transformations,
// normalization, and SetOnce filtering, then compares them to detect meaningful changes
func EnforceSetOnceAndCompareResourceForUpdate(existing, new *pkgmodel.Resource) (bool, json.RawMessage, error) {
	normalizedExisting, err := normalizeEmptyFields(existing.Properties)
	if err != nil {
		return false, nil, err
	}

	filteredRawProps, err := filterSetOnceProps(normalizedExisting, new.Properties, new.Label)
	if err != nil {
		return false, nil, err
	}

	normalizedFilteredRaw, err := normalizeEmptyFields(filteredRawProps)
	if err != nil {
		return false, nil, err
	}

	// Hash the filtered properties ONLY for comparison (like verses like)
	transformer := transformations.NewPersistValueTransformer()
	tempResource := &pkgmodel.Resource{Properties: normalizedFilteredRaw}
	hashedForComparison, err := transformer.ApplyToResource(tempResource)
	if err != nil {
		return false, nil, err
	}

	equal, err := util.JsonEqualIgnoreArrayOrder(normalizedExisting, hashedForComparison.Properties)
	if err != nil {
		return false, nil, fmt.Errorf("failed to compare properties: %w", err)
	}
	return !equal, normalizedFilteredRaw, nil
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

// normalizeEmptyFields removes empty arrays and null values for consistent comparison
func normalizeEmptyFields(properties json.RawMessage) (json.RawMessage, error) {
	if len(properties) == 0 {
		return properties, nil
	}

	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return properties, err
	}

	normalized := normalizeObject(props)

	result, err := json.Marshal(normalized)
	if err != nil {
		return properties, err
	}

	return result, nil
}

// normalizeObject recursively removes empty arrays and null values
func normalizeObject(obj map[string]any) map[string]any {
	normalized := make(map[string]any)

	for key, value := range obj {
		switch v := value.(type) {
		case []any:
			// skip empty arrays
			if len(v) > 0 {
				normalizedArray := make([]any, 0, len(v))
				for _, item := range v {
					if itemMap, ok := item.(map[string]any); ok {
						normalizedItem := normalizeObject(itemMap)
						if len(normalizedItem) > 0 {
							normalizedArray = append(normalizedArray, normalizedItem)
						}
					} else {
						normalizedArray = append(normalizedArray, item)
					}
				}
				if len(normalizedArray) > 0 {
					normalized[key] = normalizedArray
				}
			}
		case map[string]any:
			normalizedNested := normalizeObject(v)
			if len(normalizedNested) > 0 {
				normalized[key] = normalizedNested
			}
		case nil:
			// skip null values
		default:
			normalized[key] = value
		}
	}

	return normalized
}
