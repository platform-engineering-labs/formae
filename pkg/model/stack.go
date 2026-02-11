// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Stack represents a logical grouping of resources with referential integrity.
type Stack struct {
	ID          string            `json:"ID,omitempty"`
	Label       string            `json:"Label"`
	Description string            `json:"Description"`
	Policies    []json.RawMessage `json:"Policies,omitempty"` // Inline policies from PKL
}

// IsPolicyReference checks if a raw policy JSON is a reference ($ref) rather than inline
func IsPolicyReference(raw json.RawMessage) bool {
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return false
	}
	return strings.HasPrefix(ref.Ref, "policy://")
}

// ParsePolicyReference extracts the policy label from a reference
func ParsePolicyReference(raw json.RawMessage) (string, error) {
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return "", err
	}
	if !strings.HasPrefix(ref.Ref, "policy://") {
		return "", fmt.Errorf("not a policy reference: %s", ref.Ref)
	}
	return strings.TrimPrefix(ref.Ref, "policy://"), nil
}
