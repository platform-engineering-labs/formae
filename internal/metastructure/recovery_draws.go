// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func recoveryGeneratorDraws(command *forma_command.FormaCommand, pending []resource_update.ResourceUpdate, ds datastore.Datastore) ([]generator_update.GeneratorUpdate, error) {
	destinations := map[string]bool{}
	for _, u := range pending {
		if u.IsAcceptance() || u.Operation == resource_update.OperationDelete || u.Operation == resource_update.OperationReaped {
			continue
		}
		for _, g := range pkgmodel.FindGenObjectsFromProperties(u.DesiredState.Properties) {
			destinations[g.Generator] = true
		}
	}
	if !command.DrawIntentKnown {
		if len(destinations) > 0 {
			return nil, fmt.Errorf("command %s has unknown legacy generator draw intent; cannot safely recover", command.ID)
		}
		return nil, nil
	}
	var draws []generator_update.GeneratorUpdate
	for _, intent := range command.DrawGeneratorUpdates {
		if intent.Generator == nil || intent.Generator.GetID() == "" {
			return nil, fmt.Errorf("invalid persisted draw identity")
		}
		if !destinations[intent.Generator.GetID()] {
			continue
		}
		draws = append(draws, generator_update.NewDrawGeneratorUpdate(ownPlanningValue(intent.Generator), intent.StackLabel))
	}
	return draws, nil
}

func (m *Metastructure) prepareIncompleteChangeset(stored *forma_command.FormaCommand) (*changeset.Changeset, error) {
	command := ownPlanningValue(stored)
	if err := command.CheckSetupRecovery(); err != nil {
		return nil, err
	}
	var pending []resource_update.ResourceUpdate
	for _, u := range command.ResourceUpdates {
		switch u.State {
		case resource_update.ResourceUpdateStateSuccess, resource_update.ResourceUpdateStateFailed, resource_update.ResourceUpdateStateRejected, resource_update.ResourceUpdateStateCanceled:
			continue
		}
		u.UpdateState()
		if u.State == resource_update.ResourceUpdateStateInProgress {
			u.State = resource_update.ResourceUpdateStateNotStarted
		}
		if u.State == resource_update.ResourceUpdateStateNotStarted {
			pending = append(pending, u)
		}
	}
	var targets []target_update.TargetUpdate
	for _, u := range command.TargetUpdates {
		if u.State == target_update.TargetUpdateStateNotStarted || u.State == target_update.TargetUpdateStateInProgress {
			u.State = target_update.TargetUpdateStateNotStarted
			targets = append(targets, u)
		}
	}
	if len(pending) == 0 && len(targets) == 0 {
		return nil, nil
	}
	synth, err := target_update.SynthesizeResolveTargetUpdates(resource_update.ReferencedTargetLabels(pending), resource_update.SourceTargetByKsuid(pending), targets, m.Datastore)
	if err != nil {
		return nil, err
	}
	draws, err := recoveryGeneratorDraws(command, pending, m.Datastore)
	if err != nil {
		return nil, err
	}
	cs, err := changeset.NewChangeset(pending, append(targets, synth...), draws, command.ID, command.Command, command.Config.Mode)
	return &cs, err
}
