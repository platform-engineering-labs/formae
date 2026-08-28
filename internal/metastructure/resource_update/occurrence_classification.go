// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// OccurrenceClass classifies one reference occurrence for planning: whether
// it is provably stable (suppressed), needs a definite deferred update, or
// converges once because its movement is unknown.
type OccurrenceClass int

const (
	// OccurrenceDeferredUpdate is deliberately the ZERO value: a missing or
	// forgotten classification must never suppress a write.
	OccurrenceDeferredUpdate OccurrenceClass = iota
	// OccurrenceStable: the source provably did not move since this
	// occurrence last resolved; no op is planned for it.
	OccurrenceStable
	// OccurrenceConvergeUnknown: movement cannot be decided (absent or
	// legacy provenance, unknown source digest); one converging update
	// installs current provenance.
	OccurrenceConvergeUnknown
)

// OccurrenceIdentity is the normalized identity of a reference occurrence's
// source: which resource, which property, and which extraction of it. The
// extraction selector is part of identity: pointing the same reference at a
// different key of the same document is a repoint.
type OccurrenceIdentity struct {
	Ksuid        string
	PropertyPath string
	JSONPath     string
}

// TripletLookup resolves a ($stack, $label, $type) triplet to a KSUID, the
// same translation the generator's reference rewrite performs. Returning
// false means the triplet cannot be resolved; identity normalization then
// fails closed.
type TripletLookup func(stack, label, typ string) (string, bool)

// NormalizeOccurrenceIdentity maps a reference envelope of either shape - a
// translated {"$ref": "formae://<ksuid>#/<path>"} or a structured
// {"$res":true,"$label":..,"$type":..,"$stack":..,"$property":..} - to one
// identity, so a $res envelope and its translated $ref form compare equal (a
// lifecycle rewrite is not a repoint). A shape that cannot normalize returns
// ok=false and MUST be treated as an identity mismatch by callers: failing
// closed defers, never suppresses.
func NormalizeOccurrenceIdentity(envelope gjson.Result, lookup TripletLookup) (OccurrenceIdentity, bool) {
	if !envelope.IsObject() {
		return OccurrenceIdentity{}, false
	}
	jsonPath := envelope.Get("$json").String()
	if ref := envelope.Get("$ref"); ref.Exists() {
		uri := pkgmodel.FormaeURI(ref.String())
		if uri.KSUID() == "" {
			return OccurrenceIdentity{}, false
		}
		return OccurrenceIdentity{Ksuid: uri.KSUID(), PropertyPath: uri.PropertyPath(), JSONPath: jsonPath}, true
	}
	if envelope.Get("$res").Bool() {
		if lookup == nil {
			return OccurrenceIdentity{}, false
		}
		ksuid, ok := lookup(envelope.Get("$stack").String(), envelope.Get("$label").String(), envelope.Get("$type").String())
		if !ok || ksuid == "" {
			return OccurrenceIdentity{}, false
		}
		return OccurrenceIdentity{Ksuid: ksuid, PropertyPath: envelope.Get("$property").String(), JSONPath: jsonPath}, true
	}
	return OccurrenceIdentity{}, false
}

// OccurrenceRecord is the per-occurrence provenance state: computed at
// planning, persisted immutably with the resource update, and read back by
// execution-time regeneration. It carries only digests, identities, and
// paths - never a value.
type OccurrenceRecord struct {
	// DestinationPath is the consumer-side dotted path of the occurrence
	// (unique per document; the resolver's walk convention).
	DestinationPath string `json:"DestinationPath"`
	// DesiredIdentity and StoredIdentity are the normalized source
	// identities of the desired and stored envelopes. A difference is a
	// repoint (or a selector change) and always plans.
	DesiredIdentity OccurrenceIdentity `json:"DesiredIdentity"`
	StoredIdentity  OccurrenceIdentity `json:"StoredIdentity"`
	// HasStoredWritten reports whether the stored envelope carries a written
	// $value at all: absent means FIRST DECLARATION, which normal diff
	// semantics decide (never the unknown fallback).
	HasStoredWritten bool `json:"HasStoredWritten"`
	// SourceRootDigest is the source's whole-value digest in the canonical
	// domain ("" = unknown). Root domain.
	SourceRootDigest string `json:"SourceRootDigest,omitempty"`
	// WrittenProvenance is the stored envelope's $resolvedFrom. Root domain.
	WrittenProvenance string `json:"WrittenProvenance,omitempty"`
	// WrittenDigest is the digest of the value actually written to this
	// occurrence (post-extraction for $json). Written domain.
	WrittenDigest string `json:"WrittenDigest,omitempty"`
	// Class is the planning-time classification.
	Class OccurrenceClass `json:"Class"`
}

// ClassifyOccurrence sets rec.Class by the ordered movement rules. leafDigest
// lazily computes the written-domain digest of the occurrence's freshly
// extracted $json leaf from the declared source value; it returns ok=false
// when no extraction is possible (bare reference, undeclared source, or
// extraction failure).
//
// Digest domains per rule: R4 compares root vs root; R5 compares written vs
// written. Crossing domains is a bug.
func ClassifyOccurrence(rec *OccurrenceRecord, opaque, destCreateOnly, force bool, leafDigest func() (string, bool)) {
	switch {
	// R0: nothing was ever written to this occurrence - a first declaration.
	// Normal diff semantics decide (including formae's normal createOnly
	// replacement); the unknown fallback below must never see it.
	case !rec.HasStoredWritten:
		rec.Class = OccurrenceDeferredUpdate
	// R1: this classifier governs opaque occurrences only.
	case !opaque:
		rec.Class = OccurrenceDeferredUpdate
	// R2: force is the sanctioned re-assert path and bypasses suppression.
	case force:
		rec.Class = OccurrenceDeferredUpdate
	// R3: a repoint or selector change always plans, digests notwithstanding.
	case rec.DesiredIdentity != rec.StoredIdentity:
		rec.Class = OccurrenceDeferredUpdate
	// R4 [root vs root]: provably unmoved.
	case provenance.Valid(rec.WrittenProvenance) && provenance.Valid(rec.SourceRootDigest) &&
		rec.WrittenProvenance == rec.SourceRootDigest:
		rec.Class = OccurrenceStable
	// R5 [written vs written]: the root moved; a $json occurrence whose
	// freshly extracted leaf equals what was last written is still stable.
	case provenance.Valid(rec.WrittenProvenance) && provenance.Valid(rec.SourceRootDigest):
		if leaf, ok := leafDigest(); ok && provenance.Valid(rec.WrittenDigest) && leaf == rec.WrittenDigest {
			rec.Class = OccurrenceStable
		} else {
			rec.Class = OccurrenceDeferredUpdate
		}
	// R6: movement unknown. A mutable destination converges once; a
	// createOnly destination never replaces on unknown (ruled).
	case destCreateOnly:
		rec.Class = OccurrenceStable
	default:
		rec.Class = OccurrenceConvergeUnknown
	}
}
