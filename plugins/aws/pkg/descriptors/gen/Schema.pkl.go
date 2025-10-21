// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Code generated from Pkl module `types`. DO NOT EDIT.
package gen

type Schema interface {
	GetNonprovisionable() bool

	GetIdentifier() string

	GetTags() *string

	GetHints() map[string]FieldHint

	GetFields() []string
}

var _ Schema = SchemaImpl{}

type SchemaImpl struct {
	// Property that dictates whether a resource is supported by the CC API
	Nonprovisionable bool `pkl:"Nonprovisionable"`

	// Property to store as the NativeID following create
	Identifier string `pkl:"Identifier"`

	// Property that contains tags
	Tags *string `pkl:"Tags"`

	// Properties that if changed result in a replace
	Hints map[string]FieldHint `pkl:"Hints"`

	Fields []string `pkl:"Fields"`
}

// Property that dictates whether a resource is supported by the CC API
func (rcv SchemaImpl) GetNonprovisionable() bool {
	return rcv.Nonprovisionable
}

// Property to store as the NativeId following create
func (rcv SchemaImpl) GetIdentifier() string {
	return rcv.Identifier
}

// Property that contains tags
func (rcv SchemaImpl) GetTags() *string {
	return rcv.Tags
}

// Properties that if changed result in a replace
func (rcv SchemaImpl) GetHints() map[string]FieldHint {
	return rcv.Hints
}

func (rcv SchemaImpl) GetFields() []string {
	return rcv.Fields
}
