// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package generator_update

import (
	"fmt"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// NewDrawGeneratorUpdate creates a synthetic GeneratorUpdate whose only
// purpose is to draw one generator's value into memory for the destinations
// bound to it. It is the generator analogue of
// target_update.NewResolveTargetUpdate: never persisted, no row written, no
// provider called. ExistingGenerator is nil because there is nothing to
// compare or persist.
//
// generator MUST carry its KSUID (Generator.GetID()): the draw records its
// generation against that identity, and it is what matches the draw to the
// $gen envelopes naming it. A generator loaded from the datastore does NOT
// carry one — PasswordGenerator.ID is json:"-", so the KSUID lives only in
// the generators table and never round-trips through generator_data — so a
// caller building one from a load must stamp it first.
func NewDrawGeneratorUpdate(generator pkgmodel.Generator, stackLabel string) GeneratorUpdate {
	now := util.TimeNow()
	return GeneratorUpdate{
		Generator:  generator,
		Operation:  GeneratorOperationDraw,
		State:      GeneratorUpdateStateNotStarted,
		StackLabel: stackLabel,
		StartTs:    now,
		ModifiedTs: now,
	}
}

// SynthesizeDrawGeneratorUpdates builds one synthetic draw op per generator
// that at least one $gen destination in resourceUpdates needs a value from.
//
// The node set is derived from the DESTINATIONS, not from a diff of the
// generator's own row. A generator whose spec is unchanged produces no
// Create/Update/Delete GeneratorUpdate, yet a resource newly bound to it —
// or one whose stored provenance no longer matches the generation the
// generator holds — still needs a value drawn. Keying the draw off the row
// diff would leave such a destination dispatching its $gen envelope undrawn,
// which the provider boundary rejects on this apply and on every one after
// it, with nothing about the forma to change to get past it.
//
// The other half of the rule is the suppression that
// resource_update.GeneratorsNeedingDraw applies: a generator every one of
// whose destinations classified OccurrenceStable is absent from the result
// and draws nothing. That is what stops an unrelated edit elsewhere in the
// forma rotating a live credential.
//
// A generator's spec comes from this command's own generatorUpdates when it
// declares one (the desired spec, which is what the row will hold and what
// the value must be drawn under), and from lookup otherwise.
//
// lookup resolves a generator KSUID to the live generator holding that
// KSUID's spec, returning a nil generator when there is none. It is a closure
// rather than a datastore method because its two callers reach a generator by
// different routes and neither route is the other's: planning has the
// (label, stack) -> KSUID map translation just built, and resume has only the
// stacks its surviving destinations sit on. The generator it returns must
// carry its stack (GetStack), which is what the draw op is filed under; the
// KSUID is stamped here, since a loaded generator never carries one.
//
// A KSUID lookup resolves to no generator at all draws nothing and is
// logged. It is not an error: the destination bound to it still dispatches,
// and the provider boundary refuses it with a message naming the property,
// which is more use to an operator than rejecting the whole command with a
// bare KSUID. That is a different case from the one this synthesis exists to
// close, which is a generator we CAN resolve being left out of the graph.
func SynthesizeDrawGeneratorUpdates(
	resourceUpdates []resource_update.ResourceUpdate,
	generatorUpdates []GeneratorUpdate,
	lookup func(ksuid string) (pkgmodel.Generator, error),
) ([]GeneratorUpdate, error) {
	needed := resource_update.GeneratorsNeedingDraw(resourceUpdates)
	if len(needed) == 0 {
		return nil, nil
	}

	// This command's declared generators, by the KSUID GenerateGeneratorUpdates
	// stamped on them. A Delete is excluded deliberately: its row is removed
	// before the changeset starts, so its spec is not what a draw would be
	// recorded under.
	declared := make(map[string]*GeneratorUpdate, len(generatorUpdates))
	for i := range generatorUpdates {
		gu := &generatorUpdates[i]
		if gu.Operation != GeneratorOperationCreate && gu.Operation != GeneratorOperationUpdate {
			continue
		}
		if gu.Generator == nil || gu.Generator.GetID() == "" {
			continue
		}
		declared[gu.Generator.GetID()] = gu
	}

	draws := make([]GeneratorUpdate, 0, len(needed))
	for _, ksuid := range needed {
		if gu, ok := declared[ksuid]; ok {
			draws = append(draws, NewDrawGeneratorUpdate(gu.Generator, gu.StackLabel))
			continue
		}

		if lookup == nil {
			slog.Warn("No generator to draw from for a bound destination; the destination will be refused at the provider boundary",
				"generator", ksuid)
			continue
		}

		stored, err := lookup(ksuid)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve generator %s: %w", ksuid, err)
		}
		if stored == nil {
			slog.Warn("Generator named by a bound destination cannot be resolved; the destination will be refused at the provider boundary",
				"generator", ksuid)
			continue
		}

		// A loaded generator carries no ID; stamp the KSUID the $gen
		// envelopes were translated to, which is the same one its row holds.
		stored.SetID(ksuid)
		draws = append(draws, NewDrawGeneratorUpdate(stored, stored.GetStack()))
	}

	return draws, nil
}
