// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"fmt"
)

type Tag struct {
	Key   string
	Value string
}

// FlexibleTags is a custom type to handle both slice and map JSON formats.
type FlexibleTags []Tag

func (t *FlexibleTags) UnmarshalJSON(data []byte) error {
	// Most resources model Tags as an array
	var tagsAsSlice []Tag
	if err := json.Unmarshal(data, &tagsAsSlice); err == nil {
		*t = tagsAsSlice
		return nil
	}

	// Some resources model Tags as a map
	var tagsAsMap map[string]string
	if err := json.Unmarshal(data, &tagsAsMap); err == nil {
		var tags []Tag
		for key, value := range tagsAsMap {
			tags = append(tags, Tag{Key: key, Value: value})
		}
		*t = tags
		return nil
	}

	return fmt.Errorf("tags field is neither a slice of objects nor a map")
}

type Properties struct {
	Tags FlexibleTags `json:"Tags"`
}

type Data struct {
	Properties Properties `json:"Properties"`
}

type TopLevelData struct {
	Tags FlexibleTags `json:"Tags"`
}

func GetTagsFromProperties(payload json.RawMessage) []Tag {
	if len(payload) == 0 {
		return nil
	}

	// Raw properties have the Tags field at the top level.
	var topLevelData TopLevelData
	if err := json.Unmarshal(payload, &topLevelData); err == nil && len(topLevelData.Tags) > 0 {
		return topLevelData.Tags
	}

	// Wrapped properties have the Tags field nested under Properties.
	var nestedData Data
	if err := json.Unmarshal(payload, &nestedData); err == nil && len(nestedData.Properties.Tags) > 0 {
		return nestedData.Properties.Tags
	}

	return nil
}
