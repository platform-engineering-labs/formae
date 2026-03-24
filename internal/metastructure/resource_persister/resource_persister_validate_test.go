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

// --- Required fields inside Listing<SubResource> ---

func TestValidateRequiredFields_RequiredInsideRequiredListing(t *testing.T) {
	// rules is required, rules.verbs is required.
	// rules is present as an array with items that have verbs.
	// Should pass — the data is valid, validator just can't navigate arrays.
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
				"apiVersion":  {Required: true},
				"kind":        {Required: true},
				"rules":       {Required: true},
				"rules.verbs": {Required: true},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.NoError(t, err, "should not fail when required field is inside an array that is present")
}

func TestValidateRequiredFields_RequiredInsideOptionalListing_Absent(t *testing.T) {
	// traffic is optional, traffic.percent is required.
	// traffic is absent entirely — should pass because parent is optional.
	resource := pkgmodel.Resource{
		Label: "test-service",
		Type:  "GCP::Run::Service",
		Properties: json.RawMessage(`{
			"name": "my-service",
			"template": {"containers": [{"image": "nginx"}]}
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"name":            {Required: true},
				"traffic":         {Required: false},
				"traffic.percent": {Required: true},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.NoError(t, err, "should not fail when required field's optional parent is absent")
}

func TestValidateRequiredFields_RequiredInsideOptionalListing_Present(t *testing.T) {
	// traffic is optional, traffic.percent is required.
	// traffic is present as an array — should pass because we can't
	// navigate into array items via dot-path.
	resource := pkgmodel.Resource{
		Label: "test-service",
		Type:  "GCP::Run::Service",
		Properties: json.RawMessage(`{
			"name": "my-service",
			"traffic": [{"percent": 100, "type": "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"}]
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"name":            {Required: true},
				"traffic":         {Required: false},
				"traffic.percent": {Required: true},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.NoError(t, err, "should not fail when required field is inside a present optional array")
}

func TestValidateRequiredFields_RequiredInsideRequiredListing_ParentMissing(t *testing.T) {
	// rules is required, rules.verbs is required.
	// rules is completely absent — should fail because parent is required.
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
				"apiVersion":  {Required: true},
				"kind":        {Required: true},
				"rules":       {Required: true},
				"rules.verbs": {Required: true},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.Error(t, err, "should fail when required parent array is absent")
	assert.Contains(t, err.Error(), "rules")
}

// --- Deeply nested: Listing inside SubResource inside Listing ---

func TestValidateRequiredFields_DeeplyNested_RequiredInsideListingInsideSubResource(t *testing.T) {
	// spec.template.spec.containers is a Listing, containers.name is required.
	// All data present — should pass.
	resource := pkgmodel.Resource{
		Label: "test-deployment",
		Type:  "K8S::Apps::Deployment",
		Properties: json.RawMessage(`{
			"apiVersion": "apps/v1",
			"kind": "Deployment",
			"spec": {
				"template": {
					"spec": {
						"containers": [{"name": "app", "image": "nginx"}]
					}
				}
			}
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"apiVersion":                            {Required: true},
				"kind":                                  {Required: true},
				"spec":                                  {Required: false},
				"spec.template":                         {Required: true},
				"spec.template.spec":                    {Required: false},
				"spec.template.spec.containers":         {Required: true},
				"spec.template.spec.containers.name":    {Required: true},
				"spec.template.spec.containers.image":   {Required: false},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.NoError(t, err, "should not fail for deeply nested required field inside Listing")
}

func TestValidateRequiredFields_DeeplyNested_ListingInsideListing(t *testing.T) {
	// containers is a Listing, containers.ports is a Listing,
	// containers.ports.containerPort is required.
	resource := pkgmodel.Resource{
		Label: "test-deployment",
		Type:  "K8S::Apps::Deployment",
		Properties: json.RawMessage(`{
			"spec": {
				"template": {
					"spec": {
						"containers": [
							{
								"name": "app",
								"ports": [{"containerPort": 80, "protocol": "TCP"}]
							}
						]
					}
				}
			}
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"spec.template.spec.containers":                        {Required: true},
				"spec.template.spec.containers.name":                   {Required: true},
				"spec.template.spec.containers.ports":                  {Required: false},
				"spec.template.spec.containers.ports.containerPort":    {Required: true},
				"spec.template.spec.containers.ports.protocol":         {Required: false},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.NoError(t, err, "should not fail for required field nested inside Listing inside Listing")
}

// --- SubResource (non-Listing) still validates ---

func TestValidateRequiredFields_RequiredInsideSubResource(t *testing.T) {
	// spec.template is a SubResource (not a Listing), spec.template.name is required.
	// template is present but name is missing — should fail.
	resource := pkgmodel.Resource{
		Label: "test-resource",
		Type:  "Test::Type",
		Properties: json.RawMessage(`{
			"spec": {"template": {"description": "hello"}}
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
	assert.Error(t, err, "should fail when required field in SubResource is missing")
	assert.Contains(t, err.Error(), "spec.template.name")
}

func TestValidateRequiredFields_RequiredInsideOptionalSubResource_Absent(t *testing.T) {
	// spec.strategy is optional SubResource, spec.strategy.type is required.
	// strategy is absent — should pass because parent is optional.
	resource := pkgmodel.Resource{
		Label: "test-deployment",
		Type:  "K8S::Apps::Deployment",
		Properties: json.RawMessage(`{
			"apiVersion": "apps/v1",
			"kind": "Deployment",
			"spec": {"replicas": 3}
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"apiVersion":         {Required: true},
				"kind":               {Required: true},
				"spec":               {Required: false},
				"spec.strategy":      {Required: false},
				"spec.strategy.type": {Required: true},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.NoError(t, err, "should not fail when required field's optional SubResource parent is absent")
}

// --- Scalar required fields still work ---

func TestValidateRequiredFields_ScalarMissing(t *testing.T) {
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

func TestValidateRequiredFields_AllPresent(t *testing.T) {
	resource := pkgmodel.Resource{
		Label: "test-resource",
		Type:  "Test::Type",
		Properties: json.RawMessage(`{
			"name": "my-resource",
			"region": "us-east-1"
		}`),
		Schema: pkgmodel.Schema{
			Hints: map[string]pkgmodel.FieldHint{
				"name":   {Required: true},
				"region": {Required: true},
			},
		},
	}

	err := validateRequiredFields(resource)
	assert.NoError(t, err, "should pass when all required fields are present")
}
