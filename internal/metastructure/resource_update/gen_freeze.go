// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// FreezeStableGeneratorBindings replaces, in a desired document about to be
// converted for a plugin Update, every $gen destination whose occurrence
// classified stable with opaquePreservedSentinel.
//
// A stable binding is one whose generation is provably the one the destination
// already carries. Nothing draws for it and nothing is delivered into it, so
// its envelope is still bare when the update is dispatched — and the envelope
// is not the value: GuardNoUnresolvedGenerators refuses to hand a provider a
// reference where a secret belongs. Without this substitution that refusal
// takes the whole document with it, so an edit to any unrelated property on a
// generator-bound resource can never be applied. This is the $gen counterpart
// of FreezeUnrecoverableOpaqueValues, and its rationale is the same: the guard
// protecting one value must not block every other property beside it.
//
// Substituting rather than deleting is deliberate, for the reason
// opaquePreservedSentinel documents: absence on DesiredProperties means "no
// value", and a plugin rebuilding a whole-document write from it could clear
// the secret it was meant to leave alone.
//
// Stability is read through IsGenDestinationStable, the one reading of
// ProvenanceRecords for $gen destinations. An absent or non-generator record
// is not stable and is left to the guard.
//
// An envelope that already carries a $value is left alone whatever its
// classification says. A $value is only ever there because a draw was
// delivered into it moments ago, and a draw reaches every destination of its
// generator in the changeset, stable ones included: stability decides whether
// the generator draws, not who a draw reaches. Freezing a delivered value
// would throw away the credential the draw was made for and send the provider
// a preserved-sentinel instead, leaving that destination on the old
// generation while its siblings moved to the new one.
//
// A path that does not address a $gen envelope is skipped rather than written
// over: destinations are addressed by a dot-joined path, so a map key
// containing a dot resolves elsewhere — onto nothing, or onto a neighbour.
// Skipping leaves the envelope for the guard to refuse, which fails the update
// closed instead of writing a sentinel over an unrelated property.
//
// Inputs are not mutated; a rewritten copy of desired is returned, so the
// caller keeps its durable record of the binding.
func FreezeStableGeneratorBindings(desired json.RawMessage, records []OccurrenceRecord) (json.RawMessage, error) {
	if len(desired) == 0 || len(records) == 0 {
		return desired, nil
	}

	out := string(desired)
	for _, occurrence := range pkgmodel.FindGenObjectsFromProperties(desired) {
		if !IsGenDestinationStable(records, occurrence.Path) {
			continue
		}
		node := gjson.Get(out, occurrence.Path)
		if !pkgmodel.IsGenObject(node) {
			continue
		}
		if node.Get("$value").Exists() {
			continue
		}
		updated, err := sjson.SetRaw(out, occurrence.Path, opaquePreservedSentinel)
		if err != nil {
			// The path can contain user-authored map keys, so it stays out of
			// the error: this text reaches an operator-visible failure reason.
			return nil, fmt.Errorf("failed to substitute a stable generator binding: %w", err)
		}
		out = updated
	}

	return json.RawMessage(out), nil
}
