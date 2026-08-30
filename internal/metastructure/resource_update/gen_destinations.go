// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// IsGenDestinationStable reports whether the $gen occurrence at
// destinationPath was classified OccurrenceStable when this update was
// planned.
//
// This is the ONE reading of ProvenanceRecords for $gen destinations.
// Whether a generator draws at all, which destinations an executing draw
// writes to, and which destinations a resumed command still owes a value are
// three phrasings of the same question, and two independent readings of the
// same records would eventually disagree without anything observing it: a
// destination that got an edge but is frozen at delivery discards the value
// drawn for it, and one frozen but edge-less never gets a value at all.
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
// An untranslated $gen envelope carries no generator KSUID and is skipped.
// Translation rejects a $gen that resolves to no generator before planning,
// so an untranslated one here belongs to a path that never translated and
// has no identity to draw against.
func GeneratorsNeedingDraw(updates []ResourceUpdate) []string {
	var ksuids []string
	seen := make(map[string]bool)
	for i := range updates {
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
