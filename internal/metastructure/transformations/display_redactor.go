// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package transformations

import (
	"encoding/json"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Display redaction for command projections that leave the agent as
// presentation data: simulate responses, command status, conflict listings,
// and everything the CLI or an API consumer renders from them. Plaintext
// lives only in memory and the in-flight plugin request; digests live only
// at rest. Neither belongs in an API payload.
//
// Opacity is decided the same two ways persistence decides it, so the two
// cannot disagree: the schema-declared opaque field names (union of the
// prior and desired schemas with the agent-side known-opaque table, walked
// by the shared OpaqueWalk) and inline opaque/hashed envelopes. Pre-persist
// documents (a simulate response) carry declared plaintext; post-persist
// documents carry digests. Both redact to the same marker shape, which
// renderers already recognize as opaque by its $visibility.

// RedactPropertiesForDisplay returns props with every opaque value withheld.
// Reference and envelope metadata survive so renderers can still name the
// field and its source; value-bearing keys are replaced or dropped. Input
// that cannot be decoded fails closed to nil: presentation data may be
// withheld entirely, never leaked.
func RedactPropertiesForDisplay(props json.RawMessage, opaqueFields map[string]bool) json.RawMessage {
	if len(props) == 0 {
		return props
	}
	var decoded map[string]any
	if err := json.Unmarshal(props, &decoded); err != nil {
		return nil
	}
	walk := displayWalk(opaqueFields)
	walk.WalkProperties(decoded)
	// The log redactor additionally reaches opaque envelopes framed inside
	// $embed templates, which the structural walk cannot see into.
	out, err := json.Marshal(pkgmodel.RedactOpaqueForLog(decoded))
	if err != nil {
		return nil
	}
	return out
}

// RedactPatchDocumentForDisplay returns patchDoc with every opaque op value
// withheld, deciding opacity exactly as the persist-time patch hashing does:
// a path reading that is itself an opaque name means the value IS the
// secret; otherwise the value is walked for named fields and inline
// envelopes, which is what reaches a secret inside a whole-container op.
// Cascade marker values are constructed with their value already withheld
// for opaque sources and pass through untouched. Fails closed to nil on
// undecodable input.
func RedactPatchDocumentForDisplay(patchDoc json.RawMessage, opaqueFields map[string]bool) json.RawMessage {
	if len(patchDoc) == 0 {
		return patchDoc
	}
	var ops []map[string]any
	if err := json.Unmarshal(patchDoc, &ops); err != nil {
		return nil
	}
	walk := displayWalk(opaqueFields)
	for i, op := range ops {
		value, hasValue := op["value"]
		if !hasValue {
			continue
		}
		if m, ok := value.(map[string]any); ok && m["$cascade-resolvable"] == true {
			continue
		}
		path, _ := op["path"].(string)
		pointer, err := decodeJSONPointer(path)
		if err != nil {
			ops[i]["value"] = redactPatchValueConservatively(opaqueFields, value)
			continue
		}
		candidates, _ := candidatePrefixes(pointer.Segments)
		ops[i]["value"] = pkgmodel.RedactOpaqueForLog(redactPatchValue(walk, candidates, value))
	}
	out, err := json.Marshal(ops)
	if err != nil {
		return nil
	}
	return out
}

func redactPatchValue(walk *OpaqueWalk, candidates []prefix, value any) any {
	for _, p := range candidates {
		if walk.Opaque[p.name()] {
			return displayRedactValue(value)
		}
	}
	if claimed, ok := claimOpaqueEnvelope(value); ok {
		return claimed
	}
	return walk.walkValueAt(value, candidates)
}

// redactPatchValueConservatively processes an op whose path could not be
// decoded, mirroring the persist transformer's conservative mode: containers
// are walked with every name testable at any depth; a bare scalar under any
// declared opacity is withheld outright.
func redactPatchValueConservatively(opaqueFields map[string]bool, value any) any {
	switch value.(type) {
	case map[string]any, []any:
		w := displayWalk(opaqueFields)
		w.MatchAtAnyDepth = true
		return pkgmodel.RedactOpaqueForLog(w.walkValueAt(value, []prefix{{}}))
	}
	if len(opaqueFields) == 0 {
		return value
	}
	return displayRedactValue(value)
}

func displayWalk(opaqueFields map[string]bool) *OpaqueWalk {
	return &OpaqueWalk{
		Opaque: opaqueFields,
		Match:  func(v any) (any, bool) { return displayRedactValue(v), true },
		OnMiss: claimOpaqueEnvelope,
	}
}

// claimOpaqueEnvelope claims a map that is an inline opaque or hashed
// envelope. Cascade markers are not envelopes and are never claimed.
func claimOpaqueEnvelope(v any) (any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	if m["$cascade-resolvable"] == true {
		return nil, false
	}
	if m["$visibility"] == pkgmodel.VisibilityOpaque || m["$hashed"] == true {
		return displayRedactValue(m), true
	}
	return nil, false
}

// displayEnvelopeMetadataKeys are the envelope keys that carry structure or
// reference identity rather than value material; only they survive display
// redaction. Everything else — $value, $applied, $resolvedFrom, and any key
// of a map-shaped secret — is value material and is withheld.
//
// $gen, $generator, and $output name a generator reference the same way $ref/
// $label/$property name a resource reference: which generator produced the
// value, and which of its outputs. A $gen is always opaque, but a plan must
// still be able to say which generator moved.
var displayEnvelopeMetadataKeys = map[string]bool{
	"$ref": true, "$res": true, "$label": true, "$type": true, "$stack": true,
	"$property": true, "$json": true, "$strategy": true, "$hashed": true,
	"$visibility": true, "$embed": true,
	"$gen": true, "$generator": true, "$output": true,
}

func displayRedactValue(v any) any {
	m, ok := v.(map[string]any)
	if !ok || !isEnvelopeShapedForDisplay(m) {
		return map[string]any{"$visibility": pkgmodel.VisibilityOpaque, "$value": pkgmodel.RedactedForLog}
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		if displayEnvelopeMetadataKeys[k] {
			out[k] = val
		}
	}
	if _, hadValue := m["$value"]; hadValue {
		out["$value"] = pkgmodel.RedactedForLog
	}
	out["$visibility"] = pkgmodel.VisibilityOpaque
	return out
}

func isEnvelopeShapedForDisplay(m map[string]any) bool {
	for _, k := range []string{"$ref", "$res", "$gen", "$value", "$visibility", "$hashed"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}
