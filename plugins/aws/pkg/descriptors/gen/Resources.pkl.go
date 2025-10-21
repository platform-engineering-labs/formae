// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Code generated from Pkl module `types`. DO NOT EDIT.
package gen

type Resources interface {
	GetResources() []ResourceType
}

var _ Resources = ResourcesImpl{}

type ResourcesImpl struct {
	Resources []ResourceType `pkl:"resources"`
}

func (rcv ResourcesImpl) GetResources() []ResourceType {
	return rcv.Resources
}
