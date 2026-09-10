// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"errors"
	"fmt"
	"sort"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ExtractCommandDesiredDelta returns immutable command contributions for source
// catch-up. Values never come from today's actual inventory. References use the
// existing strict desired renderer, failing when identity cannot be represented.
func (m *Metastructure) ExtractCommandDesiredDelta(commandID string) (*apimodel.CommandDesiredDelta, error) {
	command, err := m.Datastore.GetFormaCommandByCommandID(commandID)
	if err != nil {
		return nil, err
	}
	if command == nil || command.Resolution == nil {
		return nil, resolutionError("resolution-unavailable", "command has no resolution receipt", "")
	}
	input := &pkgmodel.Forma{}
	for _, s := range command.Stacks {
		input.Stacks = append(input.Stacks, pkgmodel.Stack{Label: s.Label})
	}
	scope := newPlanningDatastore(m.Datastore, input)
	for attempt := 0; attempt < 16; attempt++ {
		var result *apimodel.CommandDesiredDelta
		_, err = scope.certify(func() error {
			current, e := scope.GetFormaCommandByCommandID(commandID)
			if e != nil {
				return e
			}
			if current == nil || current.Resolution == nil {
				return fmt.Errorf("resolution command disappeared")
			}
			if current.State != forma_command.CommandStateSuccess && current.State != forma_command.CommandStateFailed {
				return resolutionError("desired-intent-unavailable", "source catch-up requires a terminal Success or Failed command", "")
			}
			snippet := &pkgmodel.Forma{Extraction: &pkgmodel.ExtractionContext{}, Resources: []pkgmodel.Resource{}}
			result = &apimodel.CommandDesiredDelta{CommandID: commandID, State: string(current.State), Partial: true, Resolution: ownPlanningValue(current.Resolution), Forma: snippet}
			stacks := map[string]bool{}
			targets := map[string]bool{}
			// Replacement commands can carry a delete and a create for one
			// identity. The surviving desired declaration wins over its teardown.
			declarations := map[string]int{}
			for i, u := range current.ResourceUpdates {
				switch u.Operation {
				case resource_update.OperationCreate, resource_update.OperationUpdate, resource_update.OperationReplace:
					declarations[u.DesiredState.Ksuid] = i
				case resource_update.OperationAccept:
					if _, found := declarations[u.DesiredState.Ksuid]; !found {
						declarations[u.DesiredState.Ksuid] = i
					}
				}
			}
			deleted := map[string]bool{}
			for i, u := range current.ResourceUpdates {
				r := ownPlanningValue(u.DesiredState)
				if u.Operation == resource_update.OperationDelete || u.Operation == resource_update.OperationAcceptDelete {
					if _, survives := declarations[r.Ksuid]; survives || deleted[r.Ksuid] {
						continue
					}
					deleted[r.Ksuid] = true
					result.DeletedResources = append(result.DeletedResources, pkgmodel.DriftObservation{ResourceID: r.Ksuid, Stack: r.Stack, Type: r.Type, Label: r.Label, Kind: "delete", ObservedVersion: u.Version})
					continue
				}
				if u.Operation != resource_update.OperationCreate && u.Operation != resource_update.OperationUpdate && u.Operation != resource_update.OperationReplace && u.Operation != resource_update.OperationAccept {
					continue
				}
				if declarations[r.Ksuid] != i {
					continue
				}
				r.Properties, e = filterCoOwnedProperties(r.Properties, r.Schema, r.OwnedMembers)
				if e != nil {
					return e
				}
				r.OwnedMembers = nil
				r.ReadOnlyProperties = nil
				r.PatchDocument = nil
				r.Version = ""
				snippet.Resources = append(snippet.Resources, r)
				stacks[r.Stack] = true
				targets[r.Target] = true
			}
			for _, label := range sortedScope(stacks) {
				snippet.Stacks = append(snippet.Stacks, pkgmodel.Stack{Label: label})
			}
			// Target declarations must come from the command when available. Otherwise
			// omit them: this is a resource snippet that references an existing target,
			// and advertising today's credentials as reviewed would be misleading.
			for _, u := range current.ResourceUpdates {
				if targets[u.ResourceTarget.Label] {
					snippet.Targets = append(snippet.Targets, ownPlanningValue(u.ResourceTarget))
					delete(targets, u.ResourceTarget.Label)
				}
			}
			if e = translatePartialDesiredReferences(scope, snippet); e != nil {
				return e
			}
			sort.Slice(snippet.Resources, func(i, j int) bool { return snippet.Resources[i].Ksuid < snippet.Resources[j].Ksuid })
			sort.Slice(result.DeletedResources, func(i, j int) bool {
				return result.DeletedResources[i].ResourceID < result.DeletedResources[j].ResourceID
			})
			return nil
		})
		if errors.Is(err, errPlanningScopeExpanded) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	return nil, fmt.Errorf("%w: source delta scope did not stabilize", datastore.ErrStaleAdmission)
}
