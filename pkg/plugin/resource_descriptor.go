// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

type ListParameter struct {
	ParentProperty string
	ListProperty   string
	QueryPath      string
}

type ResourceDescriptor struct {
	// The fully qualified resource type, e.g. "AWS::EC::VPC"
	Type string

	// Some reources have nested resources that can only be queried (with the LIST operation) in the context of their
	// parent resource.
	//
	// ParentResourceTypesWithMappingProperties maps these nested resource types to the slice of additional parameters
	// that the LIST operation requires to query this resource type. This is relevant only for nested resource types.
	ParentResourceTypesWithMappingProperties map[string][]ListParameter

	// Indicates whether the resource type should be automatically discovered.
	Discoverable bool

	// Indicates whether the resource type can be extracted to PKL.
	Extractable bool
}
