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

// IsGenDestinationStable reports whether the $gen occurrence at
// destinationPath was classified OccurrenceStable when this update was
// planned.
//
// This is the ONE reading of ProvenanceRecords for $gen destinations, and it
// answers ONE question: does anything still need a value from this generator?
// Whether a generator draws at all, and which destinations a resumed command
// still owes a value, are two phrasings of that question; two independent
// readings of the same records would eventually disagree without anything
// observing it.
//
// It does NOT decide who a draw reaches. Once a generator draws, every live
// non-delete destination of it in the changeset receives the value and is
// stamped with the new generation — see
// ResourceUpdate.ResolveGeneratorValue. Using stability to narrow delivery is
// what leaves two consumers of one credential holding different values, with
// each apply repairing one and breaking the other.
//
// An absent record is NOT stable, and neither is a record whose desired
// identity resolves against the resources table rather than the generators
// one. ProvenanceRecords is computed on the update path only (a create has
// none), and OccurrenceDeferredUpdate is deliberately the zero value of
// OccurrenceClass for the same reason: a missing classification must never
// suppress a write.
func IsGenDestinationStable(records []OccurrenceRecord, destinationPath string) bool {
	for i := range records {
		if records[i].DestinationPath != destinationPath {
			continue
		}
		if records[i].DesiredIdentity.Kind != OccurrenceKindGenerator {
			return false
		}
		return records[i].Class == OccurrenceStable
	}
	return false
}

// GeneratorsNeedingDraw returns the distinct generator KSUIDs that at least
// one $gen destination among these updates needs a freshly drawn value from,
// in first-seen order.
//
// The set is derived from the DESTINATIONS, never from a diff of the
// generator's own row. A generator whose spec is unchanged produces no
// GeneratorUpdate, but a resource newly bound to it — or one whose stored
// provenance no longer matches the generation the generator holds — still
// needs a value: without a draw its $gen envelope reaches the provider
// boundary undrawn and is rejected there, on this apply and on every one
// after it.
//
// A generator every one of whose destinations classified OccurrenceStable is
// absent from the result and must NOT draw: its held generation is provably
// the one those destinations already carry, so drawing would rotate a live
// credential because something unrelated in the same forma changed.
//
// An update that is tearing its destination down is skipped whatever its
// occurrence says. A destroy sets DesiredState to the stored resource, so a
// delete carries the stored $gen envelope and no ProvenanceRecords at all;
// counting it would draw a value nothing writes, advance the generation, and
// rotate the credential out from under every consumer that survives. Replace
// is unaffected: the changeset splits it into a delete and a create, and the
// create half still draws.
//
// Only DesiredState.Properties is scanned, unlike the resolver's
// answerGeneratorOutputs, which also scans ReadOnlyProperties. The divergence
// is deliberate and the two are answering different questions. The resolver
// answers every occurrence that might be CLASSIFIED, wherever it sits; this
// decides what must be DRAWN, and a draw is owed only to a property formae
// writes. Read-only properties are never dispatched to a provider — only
// DesiredState.Properties goes through convertResourceForPlugin, and
// ReadOnlyProperties is carried alongside for persistence and comparison —
// so a $gen there names a destination nothing writes. Drawing for it would
// advance the generation and rotate the credential for every consumer that
// does get written, which is the same harm the delete skip above avoids.
//
// An untranslated $gen envelope carries no generator KSUID and is skipped.
// Translation rejects a $gen that resolves to no generator before planning,
// so an untranslated one here belongs to a path that never translated and
// has no identity to draw against.
func GeneratorsNeedingDraw(updates []ResourceUpdate) []string {
	var ksuids []string
	seen := make(map[string]bool)
	for i := range updates {
		if updates[i].Operation == OperationDelete || updates[i].Operation == OperationReaped {
			continue
		}
		records := updates[i].ProvenanceRecords
		for _, gen := range pkgmodel.FindGenObjectsFromProperties(updates[i].DesiredState.Properties) {
			if gen.Generator == "" || seen[gen.Generator] {
				continue
			}
			if IsGenDestinationStable(records, gen.Path) {
				continue
			}
			seen[gen.Generator] = true
			ksuids = append(ksuids, gen.Generator)
		}
	}
	return ksuids
}

// CarryStableGeneratorBindingForward restores, on the desired document of an
// update, what a $gen destination whose occurrence classified stable already
// holds: the stored digest of its value, its $hashed marker, and — through
// the returned digest seeds — the provenance of the generation it was drawn
// from. It returns the rewritten desired document and the seeds to put on
// ResourceUpdate.ResolvedRootDigests.
//
// A stable destination draws nothing, so its desired envelope is bare: the
// forma declares a reference, never a value. That is fine for the write (the
// freeze substitutes a preserved-value sentinel on the copy the provider
// sees) but not for what lands at rest. The row is written from the desired
// document, so a bare envelope persists a destination with no value and no
// provenance, and the next apply reads that as a destination that moved:
// it plans, draws, and rotates a credential nothing asked to rotate. Carrying
// the two forward is what makes an unrelated edit on a generator-bound
// resource a no-op for the binding beside it.
//
// This is the $gen counterpart of what already happens for a literal opaque
// field, whose desired document carries the stored digest by construction
// because the author declared a value there.
//
// Stability is read through IsGenDestinationStable, and the gate is absolute:
// re-stamping an occurrence that is NOT stable would assert that a
// destination still holds a generation it may not hold, and would suppress
// the very rotation that classification exists to require. An occurrence with
// no stored value, or one stored unhashed, is left alone — there is nothing
// to carry and nothing to prove.
//
// Inputs are not mutated; a rewritten copy of desired is returned.
func CarryStableGeneratorBindingForward(
	desired, stored json.RawMessage,
	records []OccurrenceRecord,
) (json.RawMessage, map[string]string, error) {
	if len(desired) == 0 || len(stored) == 0 || len(records) == 0 {
		return desired, nil, nil
	}

	out := string(desired)
	var seeds map[string]string
	for _, occurrence := range pkgmodel.FindGenObjectsFromProperties(desired) {
		if !IsGenDestinationStable(records, occurrence.Path) {
			continue
		}
		// A destination is addressed by a dot-joined path, so a map key
		// containing a dot resolves elsewhere. Write only where the path
		// still addresses this envelope, exactly as the freeze does.
		if !pkgmodel.IsGenObject(gjson.Get(out, occurrence.Path)) {
			continue
		}
		storedEnvelope := gjson.GetBytes(stored, occurrence.Path)
		storedValue := storedEnvelope.Get("$value")
		if !storedValue.Exists() || !storedEnvelope.Get("$hashed").Bool() {
			continue
		}

		updated, err := sjson.Set(out, occurrence.Path+".$value", storedValue.Value())
		if err != nil {
			// The path can contain user-authored map keys, so it stays out of
			// the error: this text reaches an operator-visible failure reason.
			return nil, nil, fmt.Errorf("failed to carry a stable generator binding forward: %w", err)
		}
		// The digest is carried as a digest: without the marker the persist
		// transformer would hash it again and store a hash of a hash.
		updated, err = sjson.Set(updated, occurrence.Path+".$hashed", true)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to carry a stable generator binding forward: %w", err)
		}
		out = updated

		key := generatorSourceKey(occurrence.Generator, occurrence.Output)
		provenanceDigest := writtenProvenanceAt(records, occurrence.Path)
		if key == "" || provenanceDigest == "" {
			continue
		}
		if seeds == nil {
			seeds = make(map[string]string)
		}
		seeds[key] = provenanceDigest
	}

	return json.RawMessage(out), seeds, nil
}

// writtenProvenanceAt returns the generation digest the destination at this
// path was last written under, as the classifier recorded it. That value is
// exactly the $resolvedFrom the stored envelope carries, which is what the
// write-origin merge has to put back.
func writtenProvenanceAt(records []OccurrenceRecord, destinationPath string) string {
	for i := range records {
		if records[i].DestinationPath == destinationPath {
			return records[i].WrittenProvenance
		}
	}
	return ""
}
