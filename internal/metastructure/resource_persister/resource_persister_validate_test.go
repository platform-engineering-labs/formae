// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_persister

import (
	"encoding/json"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestValidateRequiredFields_TopLevel(t *testing.T) {
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Properties: json.RawMessage(`{"name": "foo"}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"name": {Required: true},
			},
		},
	}
	assert.NoError(t, validateRequiredFields(resource))
}

func TestValidateRequiredFields_TopLevel_Missing(t *testing.T) {
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Properties: json.RawMessage(`{}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"name": {Required: true},
			},
		},
	}
	err := validateRequiredFields(resource)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestValidateRequiredFields_NestedObject(t *testing.T) {
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Properties: json.RawMessage(`{"integration": {"type": "AWS"}}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"integration.type": {Required: true},
			},
		},
	}
	assert.NoError(t, validateRequiredFields(resource))
}

func TestValidateRequiredFields_NestedObject_ParentAbsent(t *testing.T) {
	// If parent doesn't exist, nested required field is N/A
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Properties: json.RawMessage(`{}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"integration.type": {Required: true},
			},
		},
	}
	assert.NoError(t, validateRequiredFields(resource))
}

func TestValidateRequiredFields_ArrayWithRequiredField(t *testing.T) {
	// All array elements have the required field
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Properties: json.RawMessage(`{"traffic": [{"percent": 100}, {"percent": 0}]}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"traffic.percent": {Required: true},
			},
		},
	}
	assert.NoError(t, validateRequiredFields(resource))
}

func TestValidateRequiredFields_ArrayWithMissingRequiredField(t *testing.T) {
	// One element is missing the required field
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Properties: json.RawMessage(`{"traffic": [{"percent": 100}, {"type": "LATEST"}]}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"traffic.percent": {Required: true},
			},
		},
	}
	err := validateRequiredFields(resource)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "traffic.percent")
}

func TestValidateRequiredFields_EmptyArray(t *testing.T) {
	// Empty array — no elements to validate, should pass
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Properties: json.RawMessage(`{"traffic": []}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"traffic.percent": {Required: true},
			},
		},
	}
	assert.NoError(t, validateRequiredFields(resource))
}

func TestValidateRequiredFields_ArrayParentAbsent(t *testing.T) {
	// Parent array doesn't exist — field is N/A
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Properties: json.RawMessage(`{}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"traffic.percent": {Required: true},
			},
		},
	}
	assert.NoError(t, validateRequiredFields(resource))
}

func TestValidateRequiredFields_DeeplyNestedArray(t *testing.T) {
	// template.containers.image — containers is an array inside template object
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Properties: json.RawMessage(`{"template": {"containers": [{"image": "nginx", "name": "app"}]}}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"template.containers.image": {Required: true},
			},
		},
	}
	assert.NoError(t, validateRequiredFields(resource))
}

func TestValidateRequiredFields_DeeplyNestedArray_Missing(t *testing.T) {
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Properties: json.RawMessage(`{"template": {"containers": [{"name": "app"}]}}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"template.containers.image": {Required: true},
			},
		},
	}
	err := validateRequiredFields(resource)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "template.containers.image")
}

func TestValidateRequiredFields_NilProperties(t *testing.T) {
	resource := pkgmodel.Resource{
		Label: "test",
		Type:  "Test::Resource",
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"name": {Required: true},
			},
		},
	}
	assert.NoError(t, validateRequiredFields(resource))
}

func TestValidateRequiredFields_K8S_Containers(t *testing.T) {
	// Simulates K8S Job: spec.template.spec.containers.name
	resource := pkgmodel.Resource{
		Label: "test-job",
		Type:  "K8S::Batch::Job",
		Properties: json.RawMessage(`{
			"spec": {
				"template": {
					"spec": {
						"containers": [
							{"name": "worker", "image": "busybox"}
						]
					}
				}
			}
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"spec.template.spec.containers.name": {Required: true},
			},
		},
	}
	assert.NoError(t, validateRequiredFields(resource))
}
