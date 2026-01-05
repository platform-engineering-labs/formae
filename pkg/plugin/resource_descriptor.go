// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import "github.com/platform-engineering-labs/formae/pkg/model"

type ListParameter struct {
	ParentProperty string `json:"ParentProperty" pkl:"ParentProperty"`
	ListProperty   string `json:"ListParameter" pkl:"ListParameter"`
	QueryPath      string `json:"QueryPath,omitempty" pkl:"QueryPath"`
}

type ResourceDescriptor struct {
	// The fully qualified resource type, e.g. "AWS::EC2::VPC"
	Type string `json:"Type" pkl:"Type"`

	// Some resources have nested resources that can only be queried (with the LIST operation) in the context of their
	// parent resource.
	//
	// ParentResourceTypesWithMappingProperties maps these nested resource types to the slice of additional parameters
	// that the LIST operation requires to query this resource type. This is relevant only for nested resource types.
	ParentResourceTypesWithMappingProperties map[string][]ListParameter `json:"ParentResourceTypesWithMappingProperties" pkl:"ParentResourceTypesWithMappingProperties"`

	Extractable  bool `json:"Extractable" pkl:"Extractable"`
	Discoverable bool `json:"Discoverable" pkl:"Discoverable"`
}

type ResourceTypeDescriptor struct {
	// The fully qualified resource type, e.g. "AWS::EC2::VPC"
	Type string `json:"Type" pkl:"Type"`

	// Schema contains the type-level metadata for this resource including field hints,
	// identifier pattern, and whether the resource is discoverable/extractable.
	// Schema is not a part of ResourceDescriptor due to the limitation of 64kb with ergo.
	Schema model.Schema `json:"Schema" pkl:"Schema"`

	// Duplicate required for easier parsing.
	ParentResourceTypesWithMappingProperties map[string][]ListParameter `json:"ParentResourceTypesWithMappingProperties" pkl:"ParentResourceTypesWithMappingProperties"`
}
