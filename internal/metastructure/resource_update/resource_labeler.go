// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/theory/jsonpath"
	"github.com/theory/jsonpath/registry"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

const labelSeparator = "-"

// labelJSONPathParser is a package-level parser for JSONPath queries used in label extraction.
var labelJSONPathParser = jsonpath.NewParser(jsonpath.WithRegistry(registry.New()))

type ResourceLabeler struct {
	datastore ResourceDataLookup
}

func NewResourceLabeler(ds ResourceDataLookup) *ResourceLabeler {
	return &ResourceLabeler{
		datastore: ds,
	}
}

// LabelForUnmanagedResource generates a label for a discovered resource.
// Uses JSONPath query from labelConfig to extract label value from properties.
// Falls back to legacy tagLabelKeys, then to nativeID if no label can be extracted.
//
// Resolution order:
// 1. Plugin's ResourceOverrides[resourceType] (if exists)
// 2. Plugin's DefaultQuery
// 3. Legacy tagLabelKeys (for backwards compatibility)
// 4. NativeID (fallback)
func (l *ResourceLabeler) LabelForUnmanagedResource(
	nativeID string,
	resourceType string,
	properties json.RawMessage,
	labelConfig plugin.LabelConfig,
	legacyTagKeys []string,
) string {
	// Try JSONPath query first (from LabelConfig)
	query := labelConfig.QueryForResourceType(resourceType)
	if query != "" {
		if label := l.extractLabelFromQuery(properties, query); label != "" {
			return l.ensureUnique(label)
		}
	}

	// Legacy fallback: use tag keys from config
	if len(legacyTagKeys) > 0 {
		if label := l.extractLabelFromLegacyTagKeys(properties, legacyTagKeys); label != "" {
			return l.ensureUnique(label)
		}
	}

	// Final fallback: use nativeID
	return l.ensureUnique(nativeID)
}

// extractLabelFromQuery evaluates a JSONPath query against properties and returns the first string result.
func (l *ResourceLabeler) extractLabelFromQuery(properties json.RawMessage, query string) string {
	if len(properties) == 0 {
		return ""
	}

	path, err := labelJSONPathParser.Parse(query)
	if err != nil {
		return ""
	}

	var data any
	if err := json.Unmarshal(properties, &data); err != nil {
		return ""
	}

	results := path.Select(data)
	if len(results) == 0 {
		return ""
	}

	// Handle array result (e.g., from tag filter query)
	if arr, ok := results[0].([]any); ok && len(arr) > 0 {
		if str, ok := arr[0].(string); ok {
			return str
		}
	}

	// Handle direct string result
	if str, ok := results[0].(string); ok {
		return str
	}

	return ""
}

// extractLabelFromLegacyTagKeys extracts a label using the legacy tag-based approach.
// This is kept for backwards compatibility with existing configurations.
func (l *ResourceLabeler) extractLabelFromLegacyTagKeys(properties json.RawMessage, tagKeys []string) string {
	tags := pkgmodel.GetTagsFromProperties(properties)
	tagMap := make(map[string]string)
	for _, tag := range tags {
		tagMap[tag.Key] = tag.Value
	}

	var labelParts []string
	for _, key := range tagKeys {
		if value, exists := tagMap[key]; exists && value != "" {
			labelParts = append(labelParts, value)
		}
	}

	if len(labelParts) > 0 {
		return strings.Join(labelParts, labelSeparator)
	}
	return ""
}

// ensureUnique checks if a label already exists and increments the version if needed.
func (l *ResourceLabeler) ensureUnique(label string) string {
	res, err := l.datastore.LatestLabelForResource(label)
	if err != nil {
		return label
	}

	// No existing resource on the unmanaged stack with this label
	if res == "" {
		return label
	}

	return incrementVersion(res)
}

func incrementVersion(version string) string {
	lastHyphenIndex := strings.LastIndex(version, "-")

	if lastHyphenIndex != -1 && lastHyphenIndex < len(version)-1 {
		versionStr := version[lastHyphenIndex+1:]
		if num, err := strconv.Atoi(versionStr); err == nil {
			newNum := num + 1
			baseName := version[:lastHyphenIndex]
			return fmt.Sprintf("%s-%d", baseName, newNum)
		}
	}

	return fmt.Sprintf("%s-%d", version, 1)
}
