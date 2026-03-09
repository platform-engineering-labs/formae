// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import "encoding/json"

// ConfigFieldHint describes mutability characteristics of a target config field.
type ConfigFieldHint struct {
	CreateOnly bool `json:"CreateOnly" pkl:"CreateOnly"`
}

// ConfigSchema describes the structure and mutability of a target's config fields.
type ConfigSchema struct {
	Hints map[string]ConfigFieldHint `json:"Hints,omitempty" pkl:"Hints,omitempty"`
}

type Target struct {
	Label        string          `json:"Label" pkl:"Label"`
	Namespace    string          `json:"Namespace" pkl:"Namespace"`
	Config       json.RawMessage `json:"Config,omitempty" pkl:"Config,omitempty"`
	ConfigSchema ConfigSchema    `json:"ConfigSchema,omitempty" pkl:"ConfigSchema,omitempty"`
	Discoverable bool            `json:"Discoverable" pkl:"Discoverable"`
	Version      int             `json:"Version,omitempty"`
}

func NewTargetFromString(target string) Target {
	var t Target
	err := json.Unmarshal([]byte(target), &t)
	if err != nil {
		return Target{}
	}
	return t
}
