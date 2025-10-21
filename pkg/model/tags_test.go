// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetTags_FromWrappedProperties_AsList(t *testing.T) {
	properties := json.RawMessage(`{
		"Properties": {
		"Tags": [
		{"Key": "Name", "Value": "MyResource"},
		{"Key": "Environment", "Value": "Production"}
		]
		}
		}`)

	tags := GetTagsFromProperties(properties)
	assert.Len(t, tags, 2)
	assert.Equal(t, Tag{Key: "Name", Value: "MyResource"}, tags[0])
	assert.Equal(t, Tag{Key: "Environment", Value: "Production"}, tags[1])
}

func TestGetTags_FromRawProperties_AsList(t *testing.T) {
	properties := json.RawMessage(`{
		"Tags": [
		{"Key": "Name", "Value": "MyResource"},
		{"Key": "Environment", "Value": "Production"}
		]
		}`)

	tags := GetTagsFromProperties(properties)
	assert.Len(t, tags, 2)
	assert.Equal(t, Tag{Key: "Name", Value: "MyResource"}, tags[0])
	assert.Equal(t, Tag{Key: "Environment", Value: "Production"}, tags[1])
}

func TestGetTags_FromWrappedProperties_AsMap(t *testing.T) {
	properties := json.RawMessage(`{
		"Properties": {
		"Tags": {
		"Name": "MyResource",
		"Environment": "Production"
		}
		}
		}`)

	tags := GetTagsFromProperties(properties)
	assert.Len(t, tags, 2)
	assert.Contains(t, tags, Tag{Key: "Name", Value: "MyResource"})
	assert.Contains(t, tags, Tag{Key: "Environment", Value: "Production"})
}

func TestGetTags_FromRawProperties_AsMap(t *testing.T) {
	properties := json.RawMessage(`{
		"Tags": {
		"Name": "MyResource",
		"Environment": "Production"
		}
		}`)

	tags := GetTagsFromProperties(properties)
	assert.Len(t, tags, 2)
	assert.Contains(t, tags, Tag{Key: "Name", Value: "MyResource"})
	assert.Contains(t, tags, Tag{Key: "Environment", Value: "Production"})
}
