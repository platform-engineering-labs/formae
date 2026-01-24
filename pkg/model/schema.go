// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

type Schema struct {
	Identifier   string               `json:"Identifier" pkl:"Identifier"`
	Fields       []string             `json:"Fields" pkl:"Fields"`
	Hints        map[string]FieldHint `json:"Hints" pkl:"Hints"`
	Discoverable bool                 `json:"Discoverable" pkl:"Discoverable"`
	Extractable  bool                 `json:"Extractable" pkl:"Extractable"`
}

type FieldHint struct {
	CreateOnly       bool `json:"CreateOnly" pkl:"CreateOnly"`
	Required         bool `json:"Required" pkl:"Required"`
	RequiredOnCreate bool `json:"RequiredOnCreate" pkl:"RequiredOnCreate"`

	IndexField   string            `json:"IndexField" pkl:"IndexField"`
	UpdateMethod FieldUpdateMethod `json:"UpdateMethod" pkl:"UpdateMethod"`
}

type FieldUpdateMethod string

const FieldUpdateMethodArray FieldUpdateMethod = "Array"
const FieldUpdateMethodEntitySet FieldUpdateMethod = "EntitySet"
const FieldUpdateMethodSet FieldUpdateMethod = "Set"
const FieldUpdateMethodNone FieldUpdateMethod = ""

func filterFields[T bool](s Schema, selector func(FieldHint) T, value T) []string {
	var result []string

	for k, v := range s.Hints {
		if selector(v) == value {
			result = append(result, k)
		}
	}

	return result
}

func (s Schema) CreateOnly() []string {
	return filterFields(s, func(h FieldHint) bool { return h.CreateOnly }, true)
}

func (s Schema) Required() []string {
	return filterFields(s, func(h FieldHint) bool { return h.Required }, true)
}

func (s Schema) RequiredOnCreate() []string {
	return filterFields(s, func(h FieldHint) bool { return h.RequiredOnCreate }, true)
}

