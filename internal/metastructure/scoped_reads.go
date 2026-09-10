// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

var errPlanningScopeExpanded = errors.New("planning scope expanded")

// planningDatastore records semantic reads. Full resource scans remain indexes:
// their callers report selected map entries through the optional observer.
type planningDatastore struct {
	datastore.Datastore
	labels, ids, targets, keys, suppliedIDs map[string]bool
	inventoryIDs                            map[string]bool
	readFailure                             error
	resolved                                [3]map[string]bool
}

func newPlanningDatastore(ds datastore.Datastore, forma *pkgmodel.Forma) *planningDatastore {
	p := &planningDatastore{inventoryIDs: map[string]bool{}, Datastore: ds, labels: map[string]bool{}, ids: map[string]bool{}, targets: map[string]bool{}, suppliedIDs: map[string]bool{}, keys: map[string]bool{}}
	for _, key := range []string{datastore.AdmissionStackMappingGuard, datastore.AdmissionTargetGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard, datastore.AdmissionTopologyGuard} {
		p.keys[key] = true
	}
	for _, s := range forma.Stacks {
		p.labels[s.Label] = true
	}
	for _, r := range forma.Resources {
		p.labels[r.Stack] = true
		if r.Ksuid != "" {
			p.ids[r.Ksuid] = true
			p.suppliedIDs[r.Ksuid] = true
		}
	}
	return p
}
func sortedScope(m map[string]bool) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}
func (p *planningDatastore) ObservePlanningStack(label string)           { p.labels[label] = true }
func (p *planningDatastore) ObservePlanningTargetInventory(label string) { p.targets[label] = true }
func (p *planningDatastore) ObservePlanningResource(id string, r *pkgmodel.Resource) {
	// New request-only resources have no database lookup predicate. In particular,
	// don't grow the durable scope with freshly minted IDs on each restart.
	if r == nil || r.Version != "" || p.suppliedIDs[id] || p.inventoryIDs[id] {
		if id != "" {
			p.ids[id] = true
		}
	}
	if r != nil {
		p.labels[r.Stack] = true
	}
}
func (p *planningDatastore) observeRows(rows []*pkgmodel.Resource) {
	for _, r := range rows {
		if r != nil {
			p.ids[r.Ksuid] = true
			p.labels[r.Stack] = true
		}
	}
}
func (p *planningDatastore) resolveKeys() ([]string, error) {
	scopes, ok := p.Datastore.(datastore.AdmissionScopeResolver)
	if !ok {
		return nil, fmt.Errorf("datastore lacks admission scopes")
	}
	predicates, ok := p.Datastore.(datastore.AdmissionPredicateResolver)
	if !ok {
		return nil, fmt.Errorf("datastore lacks admission predicates")
	}
	for i, input := range []struct {
		values  map[string]bool
		resolve func([]string) ([]string, error)
	}{{p.labels, scopes.ResolveAdmissionStackGuards}, {p.ids, predicates.ResolveAdmissionResourceIdentityGuards}, {p.targets, predicates.ResolveAdmissionTargetInventoryGuards}} {
		if len(input.values) == 0 {
			continue
		}
		// Identity rows are durable and immutable. Reuse only their opaque keys
		// within this plan; every attempt still samples every revision twice.
		if p.resolved[i] == nil {
			p.resolved[i] = map[string]bool{}
		}
		var pending []string
		for _, value := range sortedScope(input.values) {
			if !p.resolved[i][value] {
				pending = append(pending, value)
			}
		}
		if len(pending) == 0 {
			continue
		}
		keys, err := input.resolve(pending)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			p.keys[key] = true
		}
		for _, value := range pending {
			p.resolved[i][value] = true
		}
	}
	if len(p.keys) > datastore.MaxAdmissionGuards {
		return nil, fmt.Errorf("%w: planning scope exceeds %d guards", datastore.ErrInvalidAdmission, datastore.MaxAdmissionGuards)
	}
	return sortedScope(p.keys), nil
}
func (p *planningDatastore) certify(read func() error) ([]datastore.RevisionGuard, error) {
	admitter, ok := p.Datastore.(datastore.CommandAdmitter)
	if !ok {
		return nil, fmt.Errorf("datastore lacks guarded admission")
	}
	keys, err := p.resolveKeys()
	if err != nil {
		return nil, err
	}
	before, err := admitter.ReadAdmissionRevisions(keys)
	if err != nil {
		return nil, err
	}
	// Resolve current incarnations inside the sampled mapping/label interval.
	for _, label := range sortedScope(p.labels) {
		if _, err = p.GetStackByLabel(label); err != nil {
			return nil, err
		}
	}
	readErr := read()
	if p.readFailure != nil {
		readErr = errors.Join(readErr, p.readFailure)
	}
	after, err := admitter.ReadAdmissionRevisions(keys)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, after) {
		return nil, datastore.ErrStaleAdmission
	}
	expanded, err := p.resolveKeys()
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(keys, expanded) {
		return nil, errPlanningScopeExpanded
	}
	return before, readErr
}
func (p *planningDatastore) GetStackByLabel(label string) (*pkgmodel.Stack, error) {
	p.labels[label] = true
	r, e := p.Datastore.GetStackByLabel(label)
	p.recordFailure(e)
	if e == nil && r != nil && r.ID != "" {
		p.keys[datastore.AdmissionStackGuardKey(r.ID)] = true
	}
	return r, e
}
func (p *planningDatastore) LoadResourcesByStack(label string) ([]*pkgmodel.Resource, error) {
	p.labels[label] = true
	r, e := p.Datastore.LoadResourcesByStack(label)
	p.recordFailure(e)
	if e == nil {
		p.observeRows(r)
	}
	return r, e
}
func (p *planningDatastore) GetKSUIDByTriplet(stack, label, typ string) (string, error) {
	p.labels[stack] = true
	r, e := p.Datastore.GetKSUIDByTriplet(stack, label, typ)
	p.recordFailure(e)
	if e == nil && r != "" {
		p.ids[r] = true
	}
	return r, e
}
func (p *planningDatastore) BatchGetKSUIDsByTriplets(ts []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error) {
	for _, t := range ts {
		p.labels[t.Stack] = true
	}
	r, e := p.Datastore.BatchGetKSUIDsByTriplets(ts)
	if e == nil {
		// A failed create has eligible desired intent before its first inventory
		// row. Preserve that identity when a source roundtrip omits internal IDs.
		if r == nil {
			r = map[pkgmodel.TripletKey]string{}
		}
		snapshots := map[string][]datastore.ResourceSnapshot{}
		for _, triplet := range ts {
			if r[triplet] != "" {
				continue
			}
			baseline, loaded := snapshots[triplet.Stack]
			if !loaded {
				baseline, e = p.GetResourcesAtLastReconcile(triplet.Stack)
				if e != nil {
					break
				}
				snapshots[triplet.Stack] = baseline
			}
			for _, prior := range baseline {
				if prior.Label == triplet.Label && prior.Type == triplet.Type {
					if existing := r[triplet]; existing != "" && existing != prior.KSUID {
						e = fmt.Errorf("ambiguous desired resource identity for %s/%s", triplet.Stack, triplet.Label)
						break
					}
					r[triplet] = prior.KSUID
				}
			}
			if e != nil {
				break
			}
		}
	}
	p.recordFailure(e)
	if e == nil {
		for _, id := range r {
			p.ids[id] = true
		}
	}
	return r, e
}
func (p *planningDatastore) LoadResourceById(id string) (*pkgmodel.Resource, error) {
	r, e := p.Datastore.LoadResourceById(id)
	p.recordFailure(e)
	if e == nil {
		p.ids[id] = true
		p.ObservePlanningResource(id, r)
	}
	return r, e
}
func (p *planningDatastore) LoadLatestResourceByKsuid(id string) (*pkgmodel.Resource, error) {
	r, e := p.Datastore.LoadLatestResourceByKsuid(id)
	p.recordFailure(e)
	if e == nil {
		p.ids[id] = true
		p.ObservePlanningResource(id, r)
	}
	return r, e
}
func (p *planningDatastore) LoadResource(uri pkgmodel.FormaeURI) (*pkgmodel.Resource, error) {
	r, e := p.Datastore.LoadResource(uri)
	p.recordFailure(e)
	if e == nil {
		p.ids[uri.KSUID()] = true
		p.ObservePlanningResource(uri.KSUID(), r)
	}
	return r, e
}
func (p *planningDatastore) FindResourcesDependingOn(id string) ([]*pkgmodel.Resource, error) {
	r, e := p.Datastore.FindResourcesDependingOn(id)
	p.recordFailure(e)
	if e == nil {
		p.observeRows(r)
	}
	return r, e
}
func (p *planningDatastore) FindResourcesDependingOnMany(ids []string) (map[string][]*pkgmodel.Resource, error) {
	r, e := p.Datastore.FindResourcesDependingOnMany(ids)
	p.recordFailure(e)
	if e == nil {
		for _, rows := range r {
			p.observeRows(rows)
		}
	}
	return r, e
}
func (p *planningDatastore) FindResourcesReferencingGenerator(id string) ([]*pkgmodel.Resource, error) {
	r, e := p.Datastore.FindResourcesReferencingGenerator(id)
	p.recordFailure(e)
	if e == nil {
		p.observeRows(r)
	}
	return r, e
}
func (p *planningDatastore) GetResourcesAtLastReconcile(label string) ([]datastore.ResourceSnapshot, error) {
	p.labels[label] = true
	return p.Datastore.GetResourcesAtLastReconcile(label)
}
func (p *planningDatastore) GetResourceModificationsSinceLastReconcile(label string) ([]datastore.ResourceModification, error) {
	p.labels[label] = true
	return p.Datastore.GetResourceModificationsSinceLastReconcile(label)
}
func (p *planningDatastore) GetPropertiesAtLastWrite(id string) (json.RawMessage, error) {
	p.ids[id] = true
	return p.Datastore.GetPropertiesAtLastWrite(id)
}
func (p *planningDatastore) GetOwnedMembers(id string) (pkgmodel.OwnedMembers, error) {
	p.ids[id] = true
	return p.Datastore.GetOwnedMembers(id)
}
func (p *planningDatastore) GetResourceObservation(id string) (*datastore.ResourceObservation, error) {
	reader, ok := p.Datastore.(datastore.ResourceObservationReader)
	if !ok {
		return nil, fmt.Errorf("datastore lacks resource observation reader")
	}
	r, e := reader.GetResourceObservation(id)
	if e == nil {
		p.ids[id] = true
		if r != nil {
			p.labels[r.Stack] = true
			if r.StackID != "" {
				p.keys[datastore.AdmissionStackGuardKey(r.StackID)] = true
			}
			p.ObservePlanningResource(id, r.Resource)
		}
	}
	return r, e
}

func (p *planningDatastore) recordFailure(err error) {
	if err != nil && p.readFailure == nil {
		p.readFailure = err
	}
}
func (p *planningDatastore) LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error) {
	rows, err := p.Datastore.LoadAllResourcesByStack()
	p.recordFailure(err)
	// Only index identity. A callback at a semantic dereference adds guards.
	if err == nil {
		for _, resources := range rows {
			for _, r := range resources {
				if r != nil {
					p.inventoryIDs[r.Ksuid] = true
				}
			}
		}
	}
	return rows, err
}

func (p *planningDatastore) GetDesiredOwnership(label string) (map[string]pkgmodel.OwnedMembers, error) {
	p.labels[label] = true
	return datastore.ReadDesiredOwnership(p, label)
}

func (p *planningDatastore) GetDesiredInlinePoliciesForStack(id string) ([]pkgmodel.Policy, error) {
	reader, ok := p.Datastore.(datastore.DesiredMetadataReader)
	if !ok {
		return nil, fmt.Errorf("datastore lacks strict desired metadata reads")
	}
	result, err := reader.GetDesiredInlinePoliciesForStack(id)
	p.recordFailure(err)
	return result, err
}
func (p *planningDatastore) LoadDesiredGeneratorsByStack(label string) ([]pkgmodel.Generator, error) {
	p.labels[label] = true
	reader, ok := p.Datastore.(datastore.DesiredMetadataReader)
	if !ok {
		return nil, fmt.Errorf("datastore lacks strict desired metadata reads")
	}
	result, err := reader.LoadDesiredGeneratorsByStack(label)
	p.recordFailure(err)
	return result, err
}
