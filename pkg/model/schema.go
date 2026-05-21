// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import "encoding/json"

type Schema struct {
	Identifier   string               `json:"Identifier" pkl:"Identifier"`
	Fields       []string             `json:"Fields" pkl:"Fields"`
	Hints        map[string]FieldHint `json:"Hints" pkl:"Hints"`
	Discoverable bool                 `json:"Discoverable" pkl:"Discoverable"`
	Extractable  bool                 `json:"Extractable" pkl:"Extractable"`
	Portable     bool                 `json:"Portable" pkl:"Portable"`
}

type EdgeKind string

const (
	EdgeKindDefault           EdgeKind = "default"
	EdgeKindAttachesTo        EdgeKind = "attachesTo"
	EdgeKindRuntimeDependency EdgeKind = "runtimeDependency"
)

type FieldHint struct {
	CreateOnly         bool `json:"CreateOnly" pkl:"CreateOnly"`
	WriteOnly          bool `json:"WriteOnly" pkl:"WriteOnly"`
	Required           bool `json:"Required" pkl:"Required"`
	RequiredOnCreate   bool `json:"RequiredOnCreate" pkl:"RequiredOnCreate"`
	HasProviderDefault bool `json:"HasProviderDefault" pkl:"HasProviderDefault"`
	AttachesTo         bool     `json:"AttachesTo" pkl:"AttachesTo"` // DEPRECATED: kept for one release; engine derives EdgeKind from this when set.
	EdgeKind           EdgeKind `json:"EdgeKind" pkl:"EdgeKind"`    // NEW

	IndexField   string            `json:"IndexField" pkl:"IndexField"`
	UpdateMethod FieldUpdateMethod `json:"UpdateMethod" pkl:"UpdateMethod"`
}

// UnmarshalJSON normalizes the deprecated AttachesTo alias into EdgeKind so
// schemas published before EdgeKind landed continue to drive correct DAG edges.
func (fh *FieldHint) UnmarshalJSON(data []byte) error {
	type rawHint FieldHint
	var raw rawHint
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*fh = FieldHint(raw)
	if fh.EdgeKind == "" {
		fh.EdgeKind = EdgeKindDefault
		if fh.AttachesTo {
			fh.EdgeKind = EdgeKindAttachesTo
		}
	}
	return nil
}

type FieldUpdateMethod string

const FieldUpdateMethodArray FieldUpdateMethod = "Array"
const FieldUpdateMethodEntitySet FieldUpdateMethod = "EntitySet"
const FieldUpdateMethodSet FieldUpdateMethod = "Set"
const FieldUpdateMethodAtomic FieldUpdateMethod = "Atomic"
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

func (s Schema) WriteOnly() []string {
	return filterFields(s, func(h FieldHint) bool { return h.WriteOnly }, true)
}

func (s Schema) HasProviderDefault() []string {
	return filterFields(s, func(h FieldHint) bool { return h.HasProviderDefault }, true)
}
