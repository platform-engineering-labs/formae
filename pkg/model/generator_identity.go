// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import "encoding/json"

// GeneratorIdentity is controller state for one generator: its stable KSUID
// and the generation it currently holds. Deliberately kept off Generator so
// it can never participate in desired-config equality.
//
// GenerationSpec's bytes are NOT canonical: Postgres and Aurora store it as
// JSONB, which normalizes key order and whitespace on write, so what comes
// back can differ byte-for-byte from what AdvanceGeneration was given. Parse
// it; never byte-compare or hash it against the spec that was drawn.
//
// Defined here (rather than internal/datastore) so the resource_update
// package — which must not import internal/datastore, to avoid a cycle
// (internal/datastore imports resource_update for ResourceUpdate) — can
// still express a generator lookup that returns it. internal/datastore
// aliases its GeneratorIdentity to this type.
//
// This is agent-internal controller state, not a shape a forma author ever
// authors or reads: it is not part of pkg/model's intended SDK surface for
// this published module, and exists here only to break the import cycle
// above. Do not extend it for anything a forma-authoring caller would need.
type GeneratorIdentity struct {
	ID             string          // the generator's stable KSUID
	GenerationID   string          // "" until a generation has been drawn
	GenerationSpec json.RawMessage // the spec that generation was drawn under; nil when GenerationID is ""
}
