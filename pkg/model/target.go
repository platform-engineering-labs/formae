// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"time"
)

// Target health state values stored in the health_state column.
const (
	TargetHealthStateUnknown     = "unknown"
	TargetHealthStateReachable   = "reachable"
	TargetHealthStateUnreachable = "unreachable"
	TargetHealthStateReaped      = "reaped"
)

// TargetHealthObservation carries the result of a single health sample for a target.
// It is produced by the classifier and delivered to the datastore via the async persister.
// IncarnationID may be empty, in which case the incarnation guard is skipped.
type TargetHealthObservation struct {
	// TargetLabel identifies the target this observation applies to.
	TargetLabel string
	// IncarnationID is the expected target_incarnation_id. When non-empty the
	// datastore rejects the write if the stored id does not match.
	IncarnationID string
	// State is the new health state (use the TargetHealthState* constants).
	State string
	// ObservedAt is the time the observation was taken (used as the monotonic guard).
	ObservedAt time.Time
	// LastSeenAt is set to ObservedAt for reachable observations; nil otherwise.
	LastSeenAt *time.Time
	// LastErrorCode is non-empty for unreachable observations.
	LastErrorCode string
}

// ConfigFieldHint describes mutability characteristics of a target config field.
// Mirrors FieldHint but scoped to target configuration.
type ConfigFieldHint struct {
	CreateOnly bool `json:"CreateOnly" pkl:"CreateOnly"`
}

// ConfigSchema describes the structure and mutability of a target's config fields.
type ConfigSchema struct {
	Hints map[string]ConfigFieldHint `json:"Hints,omitempty" pkl:"Hints,omitempty"`
}

// IsZero reports whether the schema has no hints, used by omitzero to suppress
// the field in JSON output for targets without schema annotations.
func (cs ConfigSchema) IsZero() bool {
	return len(cs.Hints) == 0
}

// TargetHealth holds persisted health-observation fields for a target.
// These are runtime state, not part of the declared/serialized target.
type TargetHealth struct {
	IncarnationID           string     `json:"-"`
	State                   string     `json:"-"`
	LastSeenAt              *time.Time `json:"-"`
	ObservedAt              *time.Time `json:"-"`
	FirstUnreachableAt      *time.Time `json:"-"`
	LastSampleAt            *time.Time `json:"-"`
	UnreachableAccumSeconds int64      `json:"-"`
	LastErrorCode           string     `json:"-"`
}

type Target struct {
	Label        string          `json:"Label" pkl:"Label"`
	Namespace    string          `json:"Namespace" pkl:"Namespace"`
	Config       json.RawMessage `json:"Config,omitempty" pkl:"Config,omitempty"`
	ConfigSchema ConfigSchema    `json:"ConfigSchema,omitzero" pkl:"ConfigSchema,omitempty"`
	Discoverable bool            `json:"Discoverable" pkl:"Discoverable"`
	Version      int             `json:"Version,omitempty"`
	Health       *TargetHealth   `json:"-"`
}

func NewTargetFromString(target string) Target {
	var t Target
	err := json.Unmarshal([]byte(target), &t)
	if err != nil {
		return Target{}
	}
	return t
}
