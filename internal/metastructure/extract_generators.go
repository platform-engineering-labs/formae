// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ExtractGenerators projects every live generator into the inventory shape.
//
// A generator has no standalone form — it is always owned by exactly one stack
// — so the walk is over stacks, mirroring how ExtractStacks reaches per-stack
// policies rather than how ExtractPolicies reaches standalone ones.
//
// Two of the six things the inventory shows are derived rather than stored.
// The cadence comes out of the generator's own declared spec; the instant of
// the last rotation comes out of command history, via the same
// GetGeneratorsWithRotation query the rotation scheduler runs, so the tab and
// the scheduler can never disagree about when a credential last turned over.
// Destinations comes from FindResourcesReferencingGenerator, which is the
// authority on which resources bind a property to the generator.
//
// A per-generator lookup failing is logged and skipped rather than failing the
// whole listing: an inventory that renders nothing because one generator's
// destinations could not be read is worse than one that renders the rest.
func (m *Metastructure) ExtractGenerators() ([]apimodel.GeneratorInventoryItem, error) {
	stacks, err := m.Datastore.ListAllStacks()
	if err != nil {
		return nil, err
	}

	rotationByID, err := m.rotationByGeneratorID()
	if err != nil {
		return nil, err
	}

	var items []apimodel.GeneratorInventoryItem
	for _, stack := range stacks {
		generators, err := m.Datastore.LoadGeneratorsByStack(stack.Label)
		if err != nil {
			slog.Warn("Failed to load generators for stack", "stack", stack.Label, "error", err)
			continue
		}
		for _, gen := range generators {
			item, ok := m.generatorInventoryItem(gen, stack.Label, rotationByID)
			if !ok {
				continue
			}
			items = append(items, item)
		}
	}

	return items, nil
}

// rotationByGeneratorID indexes the rotation query by generator KSUID. The
// query answers only for generators that declare a cadence, which is exactly
// the set that has a cadence history to report.
func (m *Metastructure) rotationByGeneratorID() (map[string]datastoreRotation, error) {
	infos, err := m.Datastore.GetGeneratorsWithRotation()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]datastoreRotation, len(infos))
	for _, info := range infos {
		byID[info.GeneratorID] = datastoreRotation{
			everySeconds: info.IntervalSeconds,
			lastRotated:  info.LastRotationAt,
		}
	}
	return byID, nil
}

// datastoreRotation is the pair of cadence facts the projection needs, kept
// local so the projection does not depend on the datastore row shape.
type datastoreRotation struct {
	everySeconds int
	lastRotated  time.Time
}

// generatorInventoryItem builds one item. ok is false when the generator's
// spec cannot be marshalled, which is the one case with nothing to render.
func (m *Metastructure) generatorInventoryItem(
	gen pkgmodel.Generator,
	stackLabel string,
	rotationByID map[string]datastoreRotation,
) (apimodel.GeneratorInventoryItem, bool) {
	configJSON, err := json.Marshal(gen)
	if err != nil {
		slog.Warn("Failed to marshal generator config",
			"label", gen.GetLabel(), "stack", stackLabel, "error", err)
		return apimodel.GeneratorInventoryItem{}, false
	}

	item := apimodel.GeneratorInventoryItem{
		Label:  gen.GetLabel(),
		Type:   gen.GetType(),
		Stack:  stackLabel,
		Config: configJSON,
	}

	// A generator loaded from the datastore carries no KSUID (PasswordGenerator.ID
	// is json:"-"), so the identity lookup is what supplies both the generation
	// it holds and the KSUID its destinations and its rotation history are keyed
	// by.
	identity, err := m.Datastore.GetGeneratorIdentity(gen.GetLabel(), stackLabel)
	if err != nil {
		slog.Warn("Failed to get generator identity",
			"label", gen.GetLabel(), "stack", stackLabel, "error", err)
		return item, true
	}
	item.GenerationID = identity.GenerationID

	if rotation, found := rotationByID[identity.ID]; found {
		item.EverySeconds = rotation.everySeconds
		item.LastRotatedAt = rotation.lastRotated
	}

	if identity.ID == "" {
		return item, true
	}

	destinations, err := m.Datastore.FindResourcesReferencingGenerator(identity.ID)
	if err != nil {
		slog.Warn("Failed to find resources referencing generator",
			"label", gen.GetLabel(), "stack", stackLabel, "error", err)
		return item, true
	}
	item.Destinations = generatorDestinations(destinations)

	return item, true
}

// generatorDestinations projects bound resources down to label and stack. A
// destination's Properties hold the generator's value under an opaque
// envelope; taking only these two fields is what keeps it out of the
// inventory.
//
// The order is stable (stack, then label) so the rendered list and its count
// do not shuffle between reads.
func generatorDestinations(resources []*pkgmodel.Resource) []apimodel.GeneratorDestination {
	if len(resources) == 0 {
		return nil
	}
	out := make([]apimodel.GeneratorDestination, 0, len(resources))
	for _, r := range resources {
		if r == nil {
			continue
		}
		out = append(out, apimodel.GeneratorDestination{
			ResourceLabel: r.Label,
			StackLabel:    r.Stack,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StackLabel != out[j].StackLabel {
			return out[i].StackLabel < out[j].StackLabel
		}
		return out[i].ResourceLabel < out[j].ResourceLabel
	})
	return out
}
