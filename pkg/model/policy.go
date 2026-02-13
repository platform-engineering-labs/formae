// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"fmt"
)

// Policy is the base interface for all policy types
type Policy interface {
	GetLabel() string
	GetType() string
	GetStackID() string
	SetStackID(id string)
}

// TTLPolicy destroys a stack after the specified duration
type TTLPolicy struct {
	Type         string `json:"Type"`                   // "ttl"
	Label        string `json:"Label,omitempty"`
	TTLSeconds   int64  `json:"TTLSeconds"`
	OnDependents string `json:"OnDependents"`          // "abort" or "cascade"
	StackID      string `json:"-"`                     // Set during processing, not from PKL
}

func (p *TTLPolicy) GetLabel() string    { return p.Label }
func (p *TTLPolicy) GetType() string     { return "ttl" }
func (p *TTLPolicy) GetStackID() string  { return p.StackID }
func (p *TTLPolicy) SetStackID(id string) { p.StackID = id }

// AutoReconcilePolicy periodically reconciles a stack to its declared state
type AutoReconcilePolicy struct {
	Type            string `json:"Type"`            // "auto-reconcile"
	Label           string `json:"Label,omitempty"`
	IntervalSeconds int64  `json:"IntervalSeconds"`
	StackID         string `json:"-"`              // Set during processing, not from PKL
}

func (p *AutoReconcilePolicy) GetLabel() string     { return p.Label }
func (p *AutoReconcilePolicy) GetType() string      { return "auto-reconcile" }
func (p *AutoReconcilePolicy) GetStackID() string   { return p.StackID }
func (p *AutoReconcilePolicy) SetStackID(id string) { p.StackID = id }

// ParsePolicy parses a single policy from JSON
func ParsePolicy(raw json.RawMessage) (Policy, error) {
	var header struct {
		Type string `json:"Type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("failed to parse policy type: %w", err)
	}

	switch header.Type {
	case "ttl":
		var p TTLPolicy
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("failed to parse TTL policy: %w", err)
		}
		return &p, nil
	case "auto-reconcile":
		var p AutoReconcilePolicy
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("failed to parse auto-reconcile policy: %w", err)
		}
		return &p, nil
	default:
		return nil, fmt.Errorf("unknown policy type: %s", header.Type)
	}
}

// ParsePolicies parses multiple policies from JSON
func ParsePolicies(rawPolicies []json.RawMessage) ([]Policy, error) {
	policies := make([]Policy, 0, len(rawPolicies))
	for i, raw := range rawPolicies {
		policy, err := ParsePolicy(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to parse policy at index %d: %w", i, err)
		}
		policies = append(policies, policy)
	}
	return policies, nil
}
