// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// Policy is the base interface for all policy types
type Policy interface {
	GetLabel() string
	GetType() string
	GetStackID() string
	SetStackID(id string)
}

// ExpiresAtLayout is the canonical stored form of an absolute TTL deadline:
// RFC3339 in UTC at whole-second precision, always suffixed 'Z'. The form is
// fixed width, so lexicographic order over it is chronological order — which
// lets the datastores compare deadlines as plain strings under a binary
// collation, with no timestamp cast.
const ExpiresAtLayout = "2006-01-02T15:04:05Z"

// TTLPolicy destroys a stack at a deadline expressed either relatively
// (TTLSeconds, counted from the stack's creation) or absolutely (ExpiresAt).
// Exactly one of the two is set; ParsePolicy enforces that.
type TTLPolicy struct {
	Type         string    `json:"Type"` // "ttl"
	Label        string    `json:"Label,omitempty"`
	TTLSeconds   int64     `json:"TTLSeconds"`
	ExpiresAt    time.Time `json:"ExpiresAt,omitzero"`
	OnDependents string    `json:"OnDependents"` // "abort" or "cascade"
	StackID      string    `json:"-"`            // Set during processing, not from PKL
}

func (p *TTLPolicy) GetLabel() string      { return p.Label }
func (p *TTLPolicy) GetType() string       { return "ttl" }
func (p *TTLPolicy) GetStackID() string    { return p.StackID }
func (p *TTLPolicy) SetStackID(id string)  { p.StackID = id }
func (p *TTLPolicy) SetLabel(label string) { p.Label = label }

// IsAbsolute reports whether the policy carries an absolute deadline rather
// than a duration relative to stack creation.
func (p *TTLPolicy) IsAbsolute() bool { return !p.ExpiresAt.IsZero() }

// CanonicalExpiresAt renders the absolute deadline in the stored form, or ""
// for a relative policy. It normalises to UTC whole seconds itself rather than
// trusting the field, so a policy built in Go — not only one that came through
// ParsePolicy — still stores a value the fixed-width string comparison can
// order correctly.
func (p *TTLPolicy) CanonicalExpiresAt() string {
	if !p.IsAbsolute() {
		return ""
	}
	return p.ExpiresAt.UTC().Truncate(time.Second).Format(ExpiresAtLayout)
}

// CanonicalizeExpiresAt parses an absolute deadline in any RFC3339 spelling and
// returns it in the canonical stored form: UTC, truncated to the second.
// Sub-second precision is accepted and dropped.
func CanonicalizeExpiresAt(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid ExpiresAt %q: %w", value, err)
	}
	return t.UTC().Truncate(time.Second), nil
}

// AutoReconcilePolicy periodically reconciles a stack to its declared state
type AutoReconcilePolicy struct {
	Type            string    `json:"Type"` // "auto-reconcile"
	Label           string    `json:"Label,omitempty"`
	IntervalSeconds int64     `json:"IntervalSeconds"`
	LastReconcileAt time.Time `json:"LastReconcileAt,omitzero"` // Populated at query time, not from PKL
	StackID         string    `json:"-"`                        // Set during processing, not from PKL
}

func (p *AutoReconcilePolicy) GetLabel() string      { return p.Label }
func (p *AutoReconcilePolicy) GetType() string       { return "auto-reconcile" }
func (p *AutoReconcilePolicy) GetStackID() string    { return p.StackID }
func (p *AutoReconcilePolicy) SetStackID(id string)  { p.StackID = id }
func (p *AutoReconcilePolicy) SetLabel(label string) { p.Label = label }

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
		return parseTTLPolicy(raw)
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

// parseTTLPolicy decodes a TTL policy through a presence-bearing shadow struct
// and enforces the one-of. Pointers are required because neither field has a
// value that can stand for "absent": TTLSeconds 0 and negative are both legal
// relative deadlines, and the zero time is indistinguishable from an unset one.
func parseTTLPolicy(raw json.RawMessage) (Policy, error) {
	var shadow struct {
		Type         string  `json:"Type"`
		Label        string  `json:"Label"`
		TTLSeconds   *int64  `json:"TTLSeconds"`
		ExpiresAt    *string `json:"ExpiresAt"`
		OnDependents string  `json:"OnDependents"`
	}
	if err := json.Unmarshal(raw, &shadow); err != nil {
		return nil, fmt.Errorf("failed to parse TTL policy: %w", err)
	}

	if (shadow.TTLSeconds == nil) == (shadow.ExpiresAt == nil) {
		return nil, fmt.Errorf("TTL policy must set exactly one of TTLSeconds or ExpiresAt")
	}

	p := &TTLPolicy{
		Type:         "ttl",
		Label:        shadow.Label,
		OnDependents: shadow.OnDependents,
	}

	if shadow.TTLSeconds != nil {
		p.TTLSeconds = *shadow.TTLSeconds
		return p, nil
	}

	expiresAt, err := CanonicalizeExpiresAt(*shadow.ExpiresAt)
	if err != nil {
		return nil, err
	}
	p.ExpiresAt = expiresAt
	return p, nil
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
