// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

type Schema struct {
	Identifier       string
	Tags             string
	Fields           []string
	Nonprovisionable bool
	Hints            map[string]FieldHint
	Discoverable     bool
	Extractable      bool
}

type FieldHint struct {
	CreateOnly       bool
	Persist          bool
	WriteOnly        bool
	Required         bool
	RequiredOnCreate bool
}

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

func (s Schema) Metadata() []string {
	return filterFields(s, func(h FieldHint) bool { return h.Persist }, true)
}

func (s Schema) Required() []string {
	return filterFields(s, func(h FieldHint) bool { return h.Required }, true)
}

func (s Schema) RequiredOnCreate() []string {
	return filterFields(s, func(h FieldHint) bool { return h.RequiredOnCreate }, true)
}

func (s Schema) WriteOnly() []string {
	return filterFields(s, func(h FieldHint) bool { return h.WriteOnly }, true)
}
