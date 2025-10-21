// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Code generated from Pkl module `types`. DO NOT EDIT.
package gen

type ListProperty interface {
	GetParentProperty() string

	GetListParameter() string
}

var _ ListProperty = ListPropertyImpl{}

type ListPropertyImpl struct {
	ParentProperty string `pkl:"ParentProperty"`

	ListParameter string `pkl:"ListParameter"`
}

func (rcv ListPropertyImpl) GetParentProperty() string {
	return rcv.ParentProperty
}

func (rcv ListPropertyImpl) GetListParameter() string {
	return rcv.ListParameter
}
