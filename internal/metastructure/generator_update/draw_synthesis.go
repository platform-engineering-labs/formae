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

// GeneratorSpecLookup is the minimal datastore surface the draw synthesis
// needs: the live spec of a generator this command references but does not
// itself declare. Declared locally, like GeneratorDatastore, because
// internal/datastore imports this package.
type GeneratorSpecLookup interface {
	// GetGenerator returns the live generator with this label on this stack,
	// or nil when there is none.
	GetGenerator(label, stackLabel string) (pkgmodel.Generator, error)
}

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
// the value must be drawn under), and from the datastore otherwise.
// genKeyToKsuid is the (label, stack) -> KSUID map translation resolved for
// every $gen this command saw, which is the only route from an envelope's
// KSUID back to the label and stack a datastore load needs.
//
// A KSUID this command can map to no generator at all draws nothing and is
// logged. It is not an error: the destination bound to it still dispatches,
// and the provider boundary refuses it with a message naming the property,
// which is more use to an operator than rejecting the whole command with a
// bare KSUID. That is a different case from the one this synthesis exists to
// close, which is a generator we CAN resolve being left out of the graph.
func SynthesizeDrawGeneratorUpdates(
	resourceUpdates []resource_update.ResourceUpdate,
	generatorUpdates []GeneratorUpdate,
	genKeyToKsuid map[pkgmodel.GeneratorKey]string,
	ds GeneratorSpecLookup,
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

	keyByKsuid := make(map[string]pkgmodel.GeneratorKey, len(genKeyToKsuid))
	for key, ksuid := range genKeyToKsuid {
		keyByKsuid[ksuid] = key
	}

	draws := make([]GeneratorUpdate, 0, len(needed))
	for _, ksuid := range needed {
		if gu, ok := declared[ksuid]; ok {
			draws = append(draws, NewDrawGeneratorUpdate(gu.Generator, gu.StackLabel))
			continue
		}

		key, ok := keyByKsuid[ksuid]
		if !ok || ds == nil {
			slog.Warn("No generator to draw from for a bound destination; the destination will be refused at the provider boundary",
				"generator", ksuid)
			continue
		}

		stored, err := ds.GetGenerator(key.Label, key.Stack)
		if err != nil {
			return nil, fmt.Errorf("failed to load generator %q in stack %q: %w", key.Label, key.Stack, err)
		}
		if stored == nil {
			slog.Warn("Generator named by a bound destination no longer exists; the destination will be refused at the provider boundary",
				"generator", key.Label, "stack", key.Stack)
			continue
		}

		// A loaded generator carries no ID; stamp the KSUID the $gen
		// envelopes were translated to, which is the same one its row holds.
		stored.SetID(ksuid)
		draws = append(draws, NewDrawGeneratorUpdate(stored, key.Stack))
	}

	return draws, nil
}
