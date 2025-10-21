// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"fmt"
	"strconv"
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const labelSeparator = "-"

type ResourceLabeler struct {
	datastore ResourceDataLookup
}

func NewResourceLabeler(ds ResourceDataLookup) *ResourceLabeler {
	return &ResourceLabeler{
		datastore: ds,
	}
}

func (l *ResourceLabeler) LabelForUnmanagedResource(nativeID string, resourceType string, tags []pkgmodel.Tag, tagLabelKeys []string) string {
	// First check if a resource with this native ID already exists
	// In some cases, such as buckets, the resource may have been discovered already
	labelParts := []string{}
	tagMap := make(map[string]string)
	for _, tag := range tags {
		tagMap[tag.Key] = tag.Value
	}

	for _, key := range tagLabelKeys {
		if value, exists := tagMap[key]; exists && value != "" {
			labelParts = append(labelParts, value)
		}
	}

	label := ""
	if len(labelParts) > 0 {
		label = strings.Join(labelParts, labelSeparator)
	} else {
		label = nativeID
	}

	res, err := l.datastore.LatestLabelForResource(label)
	if err != nil {
		// log error?
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
