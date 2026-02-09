// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import "encoding/json"

// Stack represents a logical grouping of resources with referential integrity.
type Stack struct {
	ID          string            `json:"ID,omitempty"`
	Label       string            `json:"Label"`
	Description string            `json:"Description"`
	Policies    []json.RawMessage `json:"Policies,omitempty"` // Inline policies from PKL
}
