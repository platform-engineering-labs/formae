// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Code generated from Pkl module `types`. DO NOT EDIT.
package gen

type FieldHint interface {
	GetCreateOnly() bool

	GetPersist() bool

	GetWriteOnly() bool

	GetRequired() bool

	GetRequiredOnCreate() bool
}

var _ FieldHint = FieldHintImpl{}

type FieldHintImpl struct {
	CreateOnly bool `pkl:"CreateOnly"`

	Persist bool `pkl:"Persist"`

	WriteOnly bool `pkl:"WriteOnly"`

	Required bool `pkl:"Required"`

	RequiredOnCreate bool `pkl:"RequiredOnCreate"`
}

func (rcv FieldHintImpl) GetCreateOnly() bool {
	return rcv.CreateOnly
}

func (rcv FieldHintImpl) GetPersist() bool {
	return rcv.Persist
}

func (rcv FieldHintImpl) GetWriteOnly() bool {
	return rcv.WriteOnly
}

func (rcv FieldHintImpl) GetRequired() bool {
	return rcv.Required
}

func (rcv FieldHintImpl) GetRequiredOnCreate() bool {
	return rcv.RequiredOnCreate
}
