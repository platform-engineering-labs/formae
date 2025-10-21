// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package props

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource/tags"
)

const TagsField = "Tags"

func HasIdentityTags(resource *pkgmodel.Resource, properties string, id string, stack string) bool {
	tagsField := TagsField
	if resource.Schema.Tags != "" {
		tagsField = resource.Schema.Tags
	}

	if gjson.Get(properties, tagsField+".#(Key="+tags.FormaeResourceLabel+").Value").String() == id &&
		gjson.Get(properties, tagsField+".#(Key="+tags.FormaeStackLabel+").Value").String() == stack {
		return true
	}

	return false
}

func Match(oaProperties json.RawMessage, rProperties string) (bool, error) {
	var propsOA map[string]any
	if err := json.Unmarshal(oaProperties, &propsOA); err != nil {
		return false, fmt.Errorf("failed to unmarshal oa.Properties: %w", err)
	}
	var propsR map[string]any
	if err := json.Unmarshal([]byte(rProperties), &propsR); err != nil {
		return false, fmt.Errorf("failed to unmarshal r.Properties: %w", err)
	}
	for key, valOA := range propsOA {
		valR, exists := propsR[key]
		if !exists || !reflect.DeepEqual(valOA, valR) {
			return false, nil
		}
	}
	return true, nil
}

// RequiresMapTags returns true if the resource type needs map-based tags
func RequiresMapTags(resourceType string) bool {
	switch resourceType {
	case "AWS::EKS::Nodegroup":
		return true
	default:
		return false
	}
}

// TransformTagsToMap transforms array tags to map format
func TransformTagsToMap(properties map[string]any) error {
	tags, ok := properties["Tags"].([]any)
	if !ok || tags == nil {
		return nil
	}

	tagsMap := make(map[string]string)
	for _, tag := range tags {
		if tagMap, ok := tag.(map[string]any); ok {
			if key, ok := tagMap["Key"].(string); ok {
				if value, ok := tagMap["Value"].(string); ok {
					tagsMap[key] = value
				}
			}
		}
	}

	properties["Tags"] = tagsMap
	return nil
}

// TransformTagsToArray transforms map tags back to array format
func TransformTagsToArray(properties map[string]any) error {
	tagsMap, ok := properties["Tags"].(map[string]any)
	if !ok || tagsMap == nil {
		return nil
	}

	tagsArray := make([]map[string]any, 0, len(tagsMap))
	for key, value := range tagsMap {
		if strValue, ok := value.(string); ok {
			tagsArray = append(tagsArray, map[string]any{
				"Key":   key,
				"Value": strValue,
			})
		}
	}

	properties["Tags"] = tagsArray
	return nil
}
