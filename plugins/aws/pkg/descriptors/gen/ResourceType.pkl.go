// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Code generated from Pkl module `types`. DO NOT EDIT.
package gen

type ResourceType interface {
	GetType() string

	GetSchema() Schema

	GetDiscoverable() bool

	GetExtractable() bool

	GetParentResourceTypesWithMappingProperties() *map[string][]ListProperty
}

var _ ResourceType = ResourceTypeImpl{}

type ResourceTypeImpl struct {
	Type string `pkl:"Type"`

	Schema Schema `pkl:"Schema"`

	Discoverable bool `pkl:"Discoverable"`

	Extractable bool `pkl:"Extractable"`

	ParentResourceTypesWithMappingProperties *map[string][]ListProperty `pkl:"ParentResourceTypesWithMappingProperties"`
}

func (rcv ResourceTypeImpl) GetType() string {
	return rcv.Type
}

func (rcv ResourceTypeImpl) GetSchema() Schema {
	return rcv.Schema
}

func (rcv ResourceTypeImpl) GetDiscoverable() bool {
	return rcv.Discoverable
}

func (rcv ResourceTypeImpl) GetExtractable() bool {
	return rcv.Extractable
}

func (rcv ResourceTypeImpl) GetParentResourceTypesWithMappingProperties() *map[string][]ListProperty {
	return rcv.ParentResourceTypesWithMappingProperties
}
