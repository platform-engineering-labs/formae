// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import "encoding/json"

type Target struct {
	Label        string          `json:"Label" pkl:"Label"`
	Namespace    string          `json:"Namespace" pkl:"Namespace"`
	Config       json.RawMessage `json:"Config,omitempty" pkl:"Config,omitempty"`
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
