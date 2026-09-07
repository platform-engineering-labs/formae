// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// setOnceConsumerLookup reads the live resources that reference a resource
// through a $ref envelope. It is the one edge the credential graph is built
// from beyond the command's own documents, and it is a one-method interface
// rather than the whole datastore so that the walk can be exercised over a
// graph a test writes down.
type setOnceConsumerLookup interface {
	FindResourcesDependingOn(ksuid string) ([]*pkgmodel.Resource, error)
}

// graphOccurrence identifies one place a credential arrives: a resource, and
// the dotted property path of the envelope it arrives in.
type graphOccurrence struct {
	Ksuid string
	Path  string
}

// graphResource is one resource in a generator's reachable graph, with every
// property document this admission can read for it.
//
// There are up to two, and both matter. The desired document is what the
// author wrote; the stored one is what resolution will honour, and it is
// where a SetOnce strategy usually lives, because a consumer's envelope
// INHERITS the strategy of the value it resolved from and keeps it from then
// on. Reading only the desired document would miss every consumer that
// acquired its strategy that way, which is the whole population this refusal
// exists for.
type graphResource struct {
	Ksuid     string
	Stack     string
	Label     string
	Type      string
	Documents []json.RawMessage
	// GenBindings and Refs are the envelopes the walk follows, derived once as
	// each document is indexed. The walk reaches a fixpoint over the whole
	// resource set, so deriving them per round would re-traverse every
	// document as many times as the graph is deep.
	GenBindings []pkgmodel.GenObject
	Refs        []refOccurrence
}

// refuseSetOnceGeneratorFields rejects a command that would draw a
// generator's value into a field whose value strategy is SetOnce.
//
// A generator exists to move a credential; a SetOnce field is one whose value
// is written once and never updated. Put in the same graph they contradict
// each other, and the contradiction is silent: the generator advances, the
// SetOnce field keeps what it has, no operation fails, and no later apply can
// level the two, because formae keeps a digest of a drawn value rather than
// the value. The only outcome that converges is to refuse the command and
// name the fields.
//
// The scope is the generator's REACHABLE GRAPH, per generator. Refusing a
// SetOnce on the destination the generator writes to directly is necessary
// and not sufficient: a destination that accepts the value can carry it on to
// a consumer that will not, and the credential stops there just as surely. So
// the walk starts at the $gen envelopes naming the generator and follows $ref
// edges outwards, checking the strategy of every envelope it arrives at.
//
// It is NOT stack-wide, and that boundary is load-bearing. A deployment can
// hold setOnce credentials throughout; reading the check over the stacks the
// command touches would turn replacing one hand-managed seed with a generator
// into a migration of every credential beside it. Nothing outside the
// generator's graph is looked at, and the datastore is not even asked about
// it.
//
// An edge is followed only when the consumer reads the property the credential
// actually arrives at. A resource holding a setOnce reference to the SECRET's
// ARN is not a consumer of the credential and must not be refused, so the
// reference's source property has to name the arrival path, or an ancestor of
// it.
//
// It fires on the generators that will DRAW, not on every generator the forma
// declares, and it reuses the draws and the live destination index that
// co-planning and the unreachable-destination refusal already computed. The
// harm is a value that moves at the generator and stops at the field; an apply
// that draws nothing moves nothing. That also keeps a deployment that carries
// the contradiction from failing every unrelated apply: the refusal lands on
// the apply that would actually mis-propagate.
//
// Like the unreachable-destination refusal beside it, it DOES fire under
// simulation. There is no confirmation that makes a credential that stops
// halfway correct, and rendering a plan that cannot be applied is worse than
// a refusal naming what to edit. And like it, it is an admission check for a
// NEW command only: a resumed command rebuilds its changeset from what a
// crashed one still owes and never comes through here.
func refuseSetOnceGeneratorFields(
	draws []generator_update.GeneratorUpdate,
	liveDestinations map[string][]*pkgmodel.Resource,
	resourceUpdates []resource_update.ResourceUpdate,
	consumers setOnceConsumerLookup,
) error {
	if len(draws) == 0 {
		return nil
	}

	graph := newSetOnceGraph(resourceUpdates, liveDestinations, consumers)

	var offending []apimodel.SetOnceGeneratorField
	for i := range draws {
		generator := draws[i].Generator
		if generator == nil || generator.GetID() == "" {
			continue
		}
		reached, err := graph.reachableFrom(generator.GetID())
		if err != nil {
			return fmt.Errorf("failed to walk the resources reached by generator %q in stack %q: %w",
				generator.GetLabel(), draws[i].StackLabel, err)
		}
		for occurrence := range reached {
			resource := graph.byKsuid[occurrence.Ksuid]
			if resource == nil || !setOnceAt(resource, occurrence.Path) {
				continue
			}
			offending = append(offending, apimodel.SetOnceGeneratorField{
				GeneratorLabel: generator.GetLabel(),
				GeneratorStack: draws[i].StackLabel,
				Stack:          resource.Stack,
				Label:          resource.Label,
				Type:           resource.Type,
				Field:          occurrence.Path,
			})
		}
	}
	if len(offending) == 0 {
		return nil
	}

	// Sorted, because the graph is walked over maps and the datastore orders
	// nothing it answers with, and an operator reading this list has to go and
	// find every entry on it.
	slices.SortFunc(offending, func(a, b apimodel.SetOnceGeneratorField) int {
		return cmp.Or(
			strings.Compare(a.GeneratorStack, b.GeneratorStack),
			strings.Compare(a.GeneratorLabel, b.GeneratorLabel),
			strings.Compare(a.Stack, b.Stack),
			strings.Compare(a.Label, b.Label),
			strings.Compare(a.Type, b.Type),
			strings.Compare(a.Field, b.Field),
		)
	})
	return apimodel.FormaGeneratorBoundToSetOnceFieldError{Fields: offending}
}

// setOnceGraph holds the resources the walk knows about and grows as it
// discovers live consumers.
type setOnceGraph struct {
	resources []*graphResource
	byKsuid   map[string]*graphResource
	consumers setOnceConsumerLookup
	// expanded records the resources whose live consumers have been read, so
	// the datastore is asked once per resource however many occurrences of it
	// the graph holds.
	expanded map[string]bool
}

// newSetOnceGraph indexes what the command already carries: the desired and
// stored documents of every resource it plans, and the stored documents of the
// live destinations the drawing generators are indexed against.
//
// A delete is left out. It writes no property, GeneratorsNeedingDraw skips it
// for the same reason, and a resource being torn down cannot hold a credential
// that fails to rotate. A replace still contributes, through the create half
// the changeset splits it into.
func newSetOnceGraph(
	resourceUpdates []resource_update.ResourceUpdate,
	liveDestinations map[string][]*pkgmodel.Resource,
	consumers setOnceConsumerLookup,
) *setOnceGraph {
	graph := &setOnceGraph{
		byKsuid:   make(map[string]*graphResource),
		consumers: consumers,
		expanded:  make(map[string]bool),
	}

	for i := range resourceUpdates {
		update := &resourceUpdates[i]
		if update.Operation == resource_update.OperationDelete ||
			update.Operation == resource_update.OperationReaped {
			continue
		}
		identity := &update.DesiredState
		if identity.Ksuid == "" {
			identity = &update.PriorState
		}
		graph.add(identity, update.DesiredState.Properties, update.PriorState.Properties)
	}

	for _, destinations := range liveDestinations {
		for _, destination := range destinations {
			if destination == nil {
				continue
			}
			graph.add(destination, destination.Properties)
		}
	}

	return graph
}

func (g *setOnceGraph) add(identity *pkgmodel.Resource, documents ...json.RawMessage) {
	if identity.Ksuid == "" {
		return
	}
	resource, known := g.byKsuid[identity.Ksuid]
	if !known {
		resource = &graphResource{
			Ksuid: identity.Ksuid,
			Stack: identity.Stack,
			Label: identity.Label,
			Type:  identity.Type,
		}
		g.byKsuid[identity.Ksuid] = resource
		g.resources = append(g.resources, resource)
	}
	for _, document := range documents {
		if len(document) == 0 {
			continue
		}
		resource.Documents = append(resource.Documents, document)
		resource.GenBindings = append(resource.GenBindings, pkgmodel.FindGenObjectsFromProperties(document)...)
		resource.Refs = append(resource.Refs, findRefOccurrences(document)...)
	}
}

// reachableFrom returns every occurrence the generator's value reaches: the
// $gen envelopes naming it, and every $ref envelope that reads one of those
// occurrences, transitively.
//
// It is a fixpoint rather than a single pass because the resource set grows as
// live consumers are read, and a consumer discovered late can reference an
// occurrence reached early. Both sets only ever grow and both are bounded by
// the graph, so the loop terminates.
func (g *setOnceGraph) reachableFrom(generatorKsuid string) (map[graphOccurrence]bool, error) {
	reached := make(map[graphOccurrence]bool)
	for _, resource := range g.resources {
		for _, binding := range resource.GenBindings {
			if binding.Generator == generatorKsuid {
				reached[graphOccurrence{Ksuid: resource.Ksuid, Path: binding.Path}] = true
			}
		}
	}
	if len(reached) == 0 {
		return nil, nil
	}

	for {
		grew, err := g.readLiveConsumers(reached)
		if err != nil {
			return nil, err
		}
		if g.followRefs(reached) {
			grew = true
		}
		if !grew {
			return reached, nil
		}
	}
}

// readLiveConsumers pulls the live consumers of every reached resource into
// the graph, so a consumer the command does not declare is walked like any
// other. Only resources the credential actually reaches are ever asked about.
func (g *setOnceGraph) readLiveConsumers(reached map[graphOccurrence]bool) (bool, error) {
	if g.consumers == nil {
		return false, nil
	}
	pending := make([]string, 0, len(reached))
	for occurrence := range reached {
		if !g.expanded[occurrence.Ksuid] {
			g.expanded[occurrence.Ksuid] = true
			pending = append(pending, occurrence.Ksuid)
		}
	}

	grew := false
	for _, ksuid := range pending {
		live, err := g.consumers.FindResourcesDependingOn(ksuid)
		if err != nil {
			return false, fmt.Errorf("failed to find the resources referencing resource %s: %w", ksuid, err)
		}
		for _, consumer := range live {
			if consumer == nil || consumer.Ksuid == "" {
				continue
			}
			if _, known := g.byKsuid[consumer.Ksuid]; known {
				continue
			}
			g.add(consumer, consumer.Properties)
			grew = true
		}
	}
	return grew, nil
}

// followRefs extends reached by one round of $ref edges, and reports whether
// it added anything.
func (g *setOnceGraph) followRefs(reached map[graphOccurrence]bool) bool {
	grew := false
	for _, resource := range g.resources {
		for _, reference := range resource.Refs {
			occurrence := graphOccurrence{Ksuid: resource.Ksuid, Path: reference.Path}
			if reached[occurrence] {
				continue
			}
			if !readsAReachedOccurrence(reached, reference) {
				continue
			}
			reached[occurrence] = true
			grew = true
		}
	}
	return grew
}

// refOccurrence is one $ref envelope: where it sits in the consuming document,
// and which resource and property it reads.
type refOccurrence struct {
	// Path is the consumer-side dotted path of the envelope.
	Path string
	// SourceKsuid and SourceProperty are the resource and property it reads.
	SourceKsuid    string
	SourceProperty string
}

// readsAReachedOccurrence reports whether a reference reads a property the
// credential arrives at. The reference must name the arrival path itself, or a
// container the arrival sits inside: a reference to some OTHER property of the
// same resource carries no credential, and refusing it would refuse formae's
// ordinary way of consuming a secret's identifier.
func readsAReachedOccurrence(reached map[graphOccurrence]bool, reference refOccurrence) bool {
	for occurrence := range reached {
		if occurrence.Ksuid != reference.SourceKsuid {
			continue
		}
		if occurrence.Path == reference.SourceProperty ||
			strings.HasPrefix(occurrence.Path, reference.SourceProperty+".") {
			return true
		}
	}
	return false
}

// findRefOccurrences lists every translated $ref envelope in a property
// document with the path it sits at.
//
// Only the translated shape is read. Admission translates $res triplets to
// $ref KSUID URIs before any of this runs, and the stored documents the live
// graph is read from hold the translated shape too; the paths that skip
// translation (synchronize, discovery, destroy) never compute a draw, so this
// is never called for them.
func findRefOccurrences(document json.RawMessage) []refOccurrence {
	var found []refOccurrence
	collectRefOccurrences("", gjson.ParseBytes(document), &found)
	return found
}

func collectRefOccurrences(path string, value gjson.Result, found *[]refOccurrence) {
	if value.IsObject() {
		if ref := value.Get("$ref"); ref.Exists() {
			uri := pkgmodel.FormaeURI(ref.String())
			if ksuid := uri.KSUID(); ksuid != "" {
				*found = append(*found, refOccurrence{
					Path:           path,
					SourceKsuid:    ksuid,
					SourceProperty: uri.PropertyPath(),
				})
			}
			return
		}
	} else if !value.IsArray() {
		return
	}
	value.ForEach(func(key, child gjson.Result) bool {
		childPath := key.String()
		if path != "" {
			childPath = path + "." + childPath
		}
		collectRefOccurrences(childPath, child, found)
		return true
	})
}

// setOnceAt reports whether any document of the resource declares a SetOnce
// value strategy at this path. Either document refuses: the desired one is
// what the author wrote, and the stored one is what resolution reads when it
// decides whether the field accepts a new value.
func setOnceAt(resource *graphResource, path string) bool {
	for _, document := range resource.Documents {
		if gjson.GetBytes(document, path).Get("$strategy").String() == pkgmodel.StrategySetOnce {
			return true
		}
	}
	return false
}
