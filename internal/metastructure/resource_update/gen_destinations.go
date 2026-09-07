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
// This is the ONE reading of $gen destination stability, and it answers ONE
// question: does anything still need a value from this generator? Whether a
// generator draws at all, and which destinations a resumed command still owes
// a value, are two phrasings of that question; two independent readings of
// the same records would eventually disagree without anything observing it.
//
// Other code reads the same records for different questions:
// writtenProvenanceAt for the digest a destination was last written under,
// and ResourceUpdate.regeneratePatchDocument for every occurrence of any kind
// that stays suppressed through a regeneration. That last one tests Class
// alone, without the generator-kind check below, and still cannot disagree
// with this reading at a $gen destination: a $gen envelope normalizes to
// OccurrenceKindGenerator, and an occurrence whose desired envelope does not
// normalize is classified OccurrenceDeferredUpdate outright.
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

// ResourceKsuidsInCommand returns the KSUIDs of the resources this command
// holds a planned operation for.
//
// It answers ONE question — can this command reach this resource — and it is
// asked in two places: subtracted from the live destinations the datastore
// indexes for a drawing generator, what is left is what no delivery could get
// to; and a resource already in the set is never co-planned, because the
// changeset keeps one node per operation URI and a second update for one
// resource would be dropped on the floor with nothing terminalizing it.
//
// Reach is a property of the graph, not of the document. Delivery runs over
// the changeset's nodes, so ANY planned update reaches its resource, whatever
// its desired document now says about the generator. A destination the same
// command unbinds is written by that command like any other node; narrowing
// the set to the updates that still bind the generator would refuse an apply
// that both unbinds one destination and draws for another, which is perfectly
// deliverable.
//
// Both ends of an update are counted, so a destination the command is tearing
// down is reached rather than named unreachable. It is owed no value — a
// delete writes no property — but the command does reach it, and omitting it
// would make removing a consumer of a generator whose spec also moved
// impossible.
func ResourceKsuidsInCommand(updates []ResourceUpdate) map[string]bool {
	inCommand := make(map[string]bool, len(updates))
	for i := range updates {
		if ksuid := updates[i].DesiredState.Ksuid; ksuid != "" {
			inCommand[ksuid] = true
		}
		if ksuid := updates[i].PriorState.Ksuid; ksuid != "" {
			inCommand[ksuid] = true
		}
	}
	return inCommand
}

// CoPlannedForDraw marks a resource that this command must plan because a
// generator it binds to draws here, not because anything about the resource
// itself moved.
//
// A drawn value reaches only the destinations that are nodes in the changeset,
// and formae keeps a hash of the value rather than the value, so a destination
// left out of the command cannot be brought level on any later apply. Adding a
// second destination to a generator therefore has to carry the destinations
// already applied beside it into the same command: they take the new
// generation together or they diverge for good.
//
// It is a distinct type rather than one more bool so it cannot be transposed
// with the `force` argument beside it. The two are unrelated: force is the
// operator re-asserting declared state, and it deliberately does not touch a
// generator binding (ClassifyOccurrence R2), while this is the planner
// discharging an obligation a draw created.
type CoPlannedForDraw bool

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

// SuppressCarriedStableGeneratorBindings drops, from both sides of a patch
// regeneration, every $gen destination that classified stable and whose
// desired envelope holds a carried digest rather than a value.
//
// It is the $gen counterpart of what SuppressUnchangedOpaqueValues does for a
// literal opaque field, and it exists because that function cannot decide this
// case. It calls a leaf unchanged by comparing the digest of the desired value
// against the digest STORED on the prior side, and a provider whose Read
// returns the secret (a secret store's GetSecretValue) has by then replaced
// that stored digest with the live plaintext. Digest against plaintext never
// matches, so the field survives into the "after" side of the diff carrying
// the digest CarryStableGeneratorBindingForward put there, and the guarded
// conversion rightly refuses to hand a provider a digest where a secret
// belongs — taking the unrelated edit beside it down with it.
//
// Stability is the answer that comparison was reaching for, and a better one:
// it was decided at planning from the destination's provenance and persisted
// immutably on this row, so it holds whatever a Read has since merged into
// prior state. A stable destination provably still holds the generation it was
// drawn from, so it contributes nothing to the diff and belongs on neither
// side of it. Dropping it from BOTH is what leaves no op behind — dropping it
// from the desired side alone would read as a removal under reconcile
// semantics.
//
// Only a $hashed envelope is dropped. A destination a draw was delivered into
// moments ago carries a LIVE value and no marker, and stability never narrows
// delivery: that value has to reach the patch or the destination stays on the
// old generation while its siblings move. A bare envelope carries nothing to
// refuse and is left for the stable-occurrence substitution in patch
// generation, which already neutralises it.
//
// This drops from the diff inputs only. The update's own desired document is
// untouched, so the freeze at the provider boundary still substitutes its
// preserved sentinel, and the row still persists the digest and the generation
// it was drawn from.
//
// Inputs are not mutated; rewritten copies are returned.
func SuppressCarriedStableGeneratorBindings(
	existing, desired json.RawMessage,
	records []OccurrenceRecord,
) (json.RawMessage, json.RawMessage, error) {
	if len(existing) == 0 || len(desired) == 0 || len(records) == 0 {
		return existing, desired, nil
	}

	strippedExisting := string(existing)
	strippedDesired := string(desired)
	for _, occurrence := range pkgmodel.FindGenObjectsFromProperties(desired) {
		if !IsGenDestinationStable(records, occurrence.Path) {
			continue
		}
		// A destination is addressed by a dot-joined path, so a map key
		// containing a dot resolves elsewhere. Act only where the path still
		// addresses this envelope, exactly as the freeze does.
		node := gjson.Get(strippedDesired, occurrence.Path)
		if !pkgmodel.IsGenObject(node) || !node.Get("$hashed").Bool() {
			continue
		}

		var err error
		strippedDesired, err = sjson.Delete(strippedDesired, occurrence.Path)
		if err != nil {
			// The path can contain user-authored map keys, so it stays out of
			// the error: this text reaches an operator-visible failure reason.
			return nil, nil, fmt.Errorf("failed to drop a stable generator binding from the desired diff input: %w", err)
		}
		if gjson.Get(strippedExisting, occurrence.Path).Exists() {
			strippedExisting, err = sjson.Delete(strippedExisting, occurrence.Path)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to drop a stable generator binding from the existing diff input: %w", err)
			}
		}
	}

	return json.RawMessage(strippedExisting), json.RawMessage(strippedDesired), nil
}
