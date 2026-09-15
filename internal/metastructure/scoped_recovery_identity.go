// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"fmt"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Target recovery re-adopts the reaped physical resource. A fresh client request
// cannot rely on IDs once written into an earlier caller-owned Forma. Only exact
// target and stack incarnation evidence may supply that missing identity.
func pinReapedRecoveryIdentities(ds datastore.Datastore, forma *pkgmodel.Forma) error {
	recovering := map[string]*pkgmodel.Target{}
	for _, declared := range forma.Targets {
		current, err := ds.LoadTarget(declared.Label)
		if err != nil {
			return err
		}
		if current == nil || current.Health == nil || current.Health.State != pkgmodel.TargetHealthStateReaped {
			continue
		}
		schema := declared.ConfigSchema
		if len(schema.Hints) == 0 {
			schema = current.ConfigSchema
		}
		if declared.Namespace != current.Namespace || target_update.ClassifyConfigChange(current.Config, declared.Config, schema) == target_update.ConfigImmutableChange {
			return fmt.Errorf("cannot recover resource identity for reaped target %q with changed target identity/configuration", declared.Label)
		}
		recovering[declared.Label] = current
		resolver.ObservePlanningTargetInventory(ds, declared.Label)
	}
	if len(recovering) == 0 {
		return nil
	}
	rows, err := ds.LoadReapedResources()
	if err != nil {
		return err
	}
	reader, ok := ds.(datastore.ResourceObservationReader)
	if !ok {
		return fmt.Errorf("target recovery requires physical resource observations")
	}
	for i := range forma.Resources {
		declared := &forma.Resources[i]
		target := recovering[declared.Target]
		if target == nil || declared.Ksuid != "" {
			continue
		}
		var selected *pkgmodel.Resource
		for _, row := range rows {
			if row == nil || !row.Managed || row.Target != declared.Target || row.Stack != declared.Stack || !strings.EqualFold(row.Type, declared.Type) || (row.Label != declared.Label && (declared.Alias == "" || row.Label != declared.Alias)) {
				continue
			}
			if selected != nil {
				return fmt.Errorf("ambiguous reaped resource identity for %s/%s/%s", declared.Stack, declared.Type, declared.Label)
			}
			selected = row
		}
		if selected == nil {
			continue
		}
		observation, err := reader.GetResourceObservation(selected.Ksuid)
		if err != nil {
			return err
		}
		stack, err := ds.GetStackByLabel(declared.Stack)
		if err != nil {
			return err
		}
		if observation == nil || observation.Operation != "reaped" || observation.Target != declared.Target || observation.Stack != declared.Stack || observation.TargetIncarnationID == "" || target.Health.IncarnationID == "" || observation.TargetIncarnationID != target.Health.IncarnationID || observation.StackID == "" || stack == nil || observation.StackID != stack.ID {
			return fmt.Errorf("reaped resource %q has uncertain or different target/stack incarnation; automatic recovery identity refused", selected.Ksuid)
		}
		declared.Ksuid = observation.KSUID
	}
	return nil
}
