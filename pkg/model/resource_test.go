// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// GetPropertyJSONPath tests - supports both top-level and nested JSON paths using JSONPath syntax

func TestGetPropertyJSONPath_ReturnsTopLevelValueFromProperties(t *testing.T) {
	resource := Resource{
		Properties: json.RawMessage(`{"Key1": "Value1", "Key2": "Value2"}`),
	}

	value, exists := resource.GetPropertyJSONPath("Key1")
	assert.True(t, exists)
	assert.Equal(t, "Value1", value)
}

func TestGetPropertyJSONPath_ReturnsNestedValueFromProperties(t *testing.T) {
	resource := Resource{
		Properties: json.RawMessage(`{"Config": {"Nested": {"Field": "NestedValue"}}}`),
	}

	value, exists := resource.GetPropertyJSONPath("$.Config.Nested.Field")
	assert.True(t, exists)
	assert.Equal(t, "NestedValue", value)
}

func TestGetPropertyJSONPath_ReturnsValueFromReadOnlyPropertiesWhenNotInProperties(t *testing.T) {
	resource := Resource{
		Properties:         json.RawMessage(`{"Key1": "Value1"}`),
		ReadOnlyProperties: json.RawMessage(`{"Key2": "Value2"}`),
	}

	value, exists := resource.GetPropertyJSONPath("Key2")
	assert.True(t, exists)
	assert.Equal(t, "Value2", value)
}

func TestGetPropertyJSONPath_ReturnsNestedValueFromReadOnlyProperties(t *testing.T) {
	resource := Resource{
		Properties:         json.RawMessage(`{"Key1": "Value1"}`),
		ReadOnlyProperties: json.RawMessage(`{"Status": {"State": "Running"}}`),
	}

	value, exists := resource.GetPropertyJSONPath("$.Status.State")
	assert.True(t, exists)
	assert.Equal(t, "Running", value)
}

// GetProperty tests - only handles top-level JSON fields using simple field names

func TestGetProperty_ReturnsTopLevelValueFromProperties(t *testing.T) {
	resource := Resource{
		Properties: json.RawMessage(`{"InstanceId": "i-1234567890abcdef0", "InstanceType": "t3.micro"}`),
	}

	value, found := resource.GetProperty("InstanceId")
	assert.True(t, found)
	assert.Equal(t, "i-1234567890abcdef0", value)
}

func TestGetProperty_ReturnsEmptyWhenPropertyNotFound(t *testing.T) {
	resource := Resource{
		Properties: json.RawMessage(`{"Key1": "Value1"}`),
	}

	value, found := resource.GetProperty("NonExistentKey")
	assert.False(t, found)
	assert.Equal(t, "", value)
}

func TestGetProperty_ReturnsEmptyWhenPropertyIsNull(t *testing.T) {
	resource := Resource{
		Properties: json.RawMessage(`{"Key1": null}`),
	}

	value, found := resource.GetProperty("Key1")
	assert.False(t, found)
	assert.Equal(t, "", value)
}

func TestGetProperty_HandlesNumericValues(t *testing.T) {
	resource := Resource{
		Properties: json.RawMessage(`{"Port": 8080, "Count": 42}`),
	}

	value, found := resource.GetProperty("Port")
	assert.True(t, found)
	assert.Equal(t, "8080", value)
}

func TestGetProperty_HandlesBooleanValues(t *testing.T) {
	resource := Resource{
		Properties: json.RawMessage(`{"Enabled": true, "Disabled": false}`),
	}

	value, found := resource.GetProperty("Enabled")
	assert.True(t, found)
	assert.Equal(t, "true", value)
}

func TestGetProperty_HandlesNestedObjectsAsJSON(t *testing.T) {
	resource := Resource{
		Properties: json.RawMessage(`{"Config": {"Nested": "Value"}}`),
	}

	// GetProperty returns the entire nested object as JSON string
	value, found := resource.GetProperty("Config")
	assert.True(t, found)
	assert.Contains(t, value, "Nested")
	assert.Contains(t, value, "Value")
}

func TestValidateRequiredOnCreateFieldsAllRequiredFieldsPresentReturnsNoError(t *testing.T) {
	resource := Resource{
		Label: "test-resource",
		Type:  "AWS::EC2::Instance",
		Schema: Schema{
			Hints: map[string]FieldHint{
				"ImageId":      {RequiredOnCreate: true},
				"InstanceType": {RequiredOnCreate: true},
				"KeyName":      {Required: true},
			},
		},
		Properties: json.RawMessage(`{
            "ImageId": "ami-12345678",
            "InstanceType": "t3.micro",
            "KeyName": "my-key"
        }`),
	}

	err := resource.ValidateRequiredOnCreateFields()
	assert.NoError(t, err)
}

func TestValidateRequiredOnCreateFieldsMissingRequiredFieldOnCreateReturnsNoError(t *testing.T) {
	resource := Resource{
		Label: "test-resource",
		Type:  "AWS::EC2::Instance",
		Schema: Schema{
			Hints: map[string]FieldHint{
				"ImageId":      {RequiredOnCreate: true},
				"InstanceType": {RequiredOnCreate: true},
				"KeyName":      {Required: true},
			},
		},
		Properties: json.RawMessage(`{
            "ImageId": "ami-12345678",
            "KeyName": "my-key"
        }`),
	}

	err := resource.ValidateRequiredOnCreateFields()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "test-resource")
	assert.Contains(t, err.Error(), "AWS::EC2::Instance")
	assert.Contains(t, err.Error(), "missing required fields: [InstanceType]")
	assert.Contains(t, err.Error(), "cannot be created")
}
