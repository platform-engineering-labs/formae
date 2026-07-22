// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// Target health state values stored in the health_state column.
const (
	TargetHealthStateUnknown     = "unknown"
	TargetHealthStateReachable   = "reachable"
	TargetHealthStateUnreachable = "unreachable"
	TargetHealthStateReaped      = "reaped"
)

// TargetHealthStateReapPending is NEVER persisted to the health_state column.
// It is a purely computed display status for an 'unreachable' target that has
// already accrued at least its configured reap-after duration — i.e. it is
// due to be reaped (on an upcoming tick, or held back by the rate cap, an
// in-flight command, or dry-run mode). Surfaced to operators via stats so a
// candidate is visible before any tombstone.
const TargetHealthStateReapPending = "reap-pending"

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
	// Reaping holds the target's reaping behaviour. It is stored as a raw
	// JSON message (not the ReapingBehaviour interface) because Target is
	// embedded in structs that round-trip through json.Marshal/Unmarshal for
	// persistence, and encoding/json cannot unmarshal a present JSON object
	// into a nil interface field. Use ParseReaping to decode it.
	Reaping json.RawMessage `json:"Reaping,omitempty" pkl:"Reaping,omitempty"`
}

// ReapingBehaviour is the base interface for all target reaping variants.
type ReapingBehaviour interface {
	GetKind() string
}

// NeverReap means the target is never reaped, regardless of how long it
// remains unreachable.
type NeverReap struct {
	Kind string `json:"Kind"` // "never"
}

func (r *NeverReap) GetKind() string { return "never" }

// ReapAfter reaps the target once it has been continuously unreachable for
// MaxUnreachableSeconds.
type ReapAfter struct {
	Kind                  string `json:"Kind"` // "after"
	MaxUnreachableSeconds int64  `json:"MaxUnreachableSeconds"`
}

func (r *ReapAfter) GetKind() string { return "after" }

// ParseReaping parses a target's reaping behaviour from JSON. A nil or
// JSON-null raw message returns (nil, nil).
func ParseReaping(raw json.RawMessage) (ReapingBehaviour, error) {
	if raw == nil || string(raw) == "null" {
		return nil, nil
	}

	var header struct {
		Kind string `json:"Kind"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("failed to parse reaping kind: %w", err)
	}

	switch header.Kind {
	case "never":
		var r NeverReap
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("failed to parse never-reap: %w", err)
		}
		return &r, nil
	case "after":
		var r ReapAfter
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("failed to parse reap-after: %w", err)
		}
		return &r, nil
	default:
		return nil, fmt.Errorf("unknown reaping kind: %s", header.Kind)
	}
}

// DefaultReapMaxUnreachableSeconds is the global fallback reap-after duration
// (24h) applied when neither the target nor its plugin manifest declares a
// reaping behaviour.
const DefaultReapMaxUnreachableSeconds int64 = 86400

// ResolveReaping applies the reaping precedence: an explicit per-target
// behaviour wins, then the plugin manifest default, then the global default
// (ReapAfter of DefaultReapMaxUnreachableSeconds). Either argument may be nil.
func ResolveReaping(explicit, manifestDefault ReapingBehaviour) ReapingBehaviour {
	if explicit != nil {
		return explicit
	}
	if manifestDefault != nil {
		return manifestDefault
	}
	return &ReapAfter{Kind: "after", MaxUnreachableSeconds: DefaultReapMaxUnreachableSeconds}
}

// MarshalReaping serializes a reaping behaviour to its raw JSON representation,
// suitable for storing in Target.Reaping. A nil behaviour yields a nil message.
func MarshalReaping(b ReapingBehaviour) (json.RawMessage, error) {
	if b == nil {
		return nil, nil
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal reaping behaviour: %w", err)
	}
	return raw, nil
}

// ReapingToColumns decomposes a target's raw reaping message into the persisted
// column values (reap_kind, reap_max_unreachable_seconds). A nil/absent message
// falls back to the global default (after / DefaultReapMaxUnreachableSeconds).
func ReapingToColumns(raw json.RawMessage) (string, int64, error) {
	behaviour, err := ParseReaping(raw)
	if err != nil {
		return "", 0, err
	}
	switch b := behaviour.(type) {
	case nil:
		return "after", DefaultReapMaxUnreachableSeconds, nil
	case *NeverReap:
		return "never", DefaultReapMaxUnreachableSeconds, nil
	case *ReapAfter:
		return "after", b.MaxUnreachableSeconds, nil
	default:
		return "", 0, fmt.Errorf("unknown reaping behaviour: %T", behaviour)
	}
}

// ReapingRawFromColumns reconstructs a target's raw reaping message from the
// persisted column values. A "never" kind reconstructs a NeverReap regardless of
// the stored seconds; any other kind is treated as reap-after.
func ReapingRawFromColumns(kind string, maxUnreachableSeconds int64) json.RawMessage {
	if kind == "never" {
		return json.RawMessage(`{"Kind":"never"}`)
	}
	return json.RawMessage(fmt.Sprintf(`{"Kind":"after","MaxUnreachableSeconds":%d}`, maxUnreachableSeconds))
}

func NewTargetFromString(target string) Target {
	var t Target
	err := json.Unmarshal([]byte(target), &t)
	if err != nil {
		return Target{}
	}
	return t
}
