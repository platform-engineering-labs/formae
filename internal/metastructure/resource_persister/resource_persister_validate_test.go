// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_persister

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestValidateRequiredFields_NestedInListing(t *testing.T) {
	// When a required field path traverses through a Listing (array),
	// e.g., "rules.verbs" where "rules" is an array of PolicyRule objects,
	// validation should skip these paths since individual array items
	// can't be validated via dot-path traversal.
	resource := pkgmodel.Resource{
		Label: "test-clusterrole",
		Type:  "K8S::Rbac::ClusterRole",
		Properties: json.RawMessage(`{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind": "ClusterRole",
			"metadata": {"name": "test-cr"},
			"rules": [
				{"apiGroups": [""], "resources": ["pods"], "verbs": ["get", "list"]}
			]
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"apiVersion":   {Required: true},
				"kind":         {Required: true},
				"metadata":     {Required: true},
				"metadata.name": {Required: true},
				"rules":        {Required: true},
				"rules.verbs":  {Required: true},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.NoError(t, err, "should not fail when required field path traverses an array")
}

func TestValidateRequiredFields_NestedInListingMissing(t *testing.T) {
	// Top-level required field "rules" is missing — should fail.
	resource := pkgmodel.Resource{
		Label: "test-clusterrole",
		Type:  "K8S::Rbac::ClusterRole",
		Properties: json.RawMessage(`{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind": "ClusterRole",
			"metadata": {"name": "test-cr"}
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"apiVersion": {Required: true},
				"kind":       {Required: true},
				"rules":      {Required: true},
				"rules.verbs": {Required: true},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.Error(t, err, "should fail when top-level required array field is missing")
	assert.Contains(t, err.Error(), "rules")
}

func TestValidateRequiredFields_ScalarRequired(t *testing.T) {
	// Basic scalar required field validation still works.
	resource := pkgmodel.Resource{
		Label: "test-resource",
		Type:  "Test::Type",
		Properties: json.RawMessage(`{
			"name": "my-resource"
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"name":   {Required: true},
				"region": {Required: true},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.Error(t, err, "should fail when required scalar field is missing")
	assert.Contains(t, err.Error(), "region")
}

func TestValidateRequiredFields_NestedSubResourceRequired(t *testing.T) {
	// Nested required fields in SubResources (not arrays) should still validate.
	resource := pkgmodel.Resource{
		Label: "test-resource",
		Type:  "Test::Type",
		Properties: json.RawMessage(`{
			"spec": {"template": {}}
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"spec":               {Required: true},
				"spec.template":      {Required: true},
				"spec.template.name": {Required: true},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.Error(t, err, "should fail when nested required field in SubResource is missing")
	assert.Contains(t, err.Error(), "spec.template.name")
}
