// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package forma_command

import (
	"encoding/json"
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A missing boundary means legacy intent is unknown, not that setup was empty.
// Only guarded initial admission sets Committed; generic saves never do setup.
type SetupBoundary struct {
	Version   int  `json:"Version"`
	Committed bool `json:"Committed"`
}
type setupMetadata struct {
	Draws           []generator_update.GeneratorUpdate `json:"Draws,omitempty"`
	DrawIntentKnown bool                               `json:"DrawIntentKnown,omitempty"`
	Resolution      *pkgmodel.DriftReview              `json:"Resolution,omitempty"`
	SetupBoundary
	OnlyMetadata bool                               `json:"OnlyMetadata,omitempty"`
	Generators   []generator_update.GeneratorUpdate `json:"Generators"`
	Stacks       []stack_update.StackUpdate         `json:"Stacks,omitempty"`
	Policies     []policy_update.PolicyUpdate       `json:"Policies,omitempty"`
}

func (fc *FormaCommand) MarshalSetupMetadata(atomicAdmission bool) (json.RawMessage, error) {
	if fc.Setup == nil && fc.GeneratorUpdates == nil {
		return nil, nil
	}
	m := setupMetadata{Generators: fc.GeneratorUpdates, Resolution: fc.Resolution, Draws: fc.DrawGeneratorUpdates, DrawIntentKnown: fc.DrawIntentKnown || fc.DrawGeneratorUpdates != nil}
	if fc.Setup != nil {
		m.SetupBoundary = *fc.Setup
		// Only initial guarded admission may assert a new atomic boundary. SQL
		// writers preserve an already-committed envelope during lifecycle saves.
		if !atomicAdmission {
			m.Committed = false
		}
	}
	if m.Committed {
		m.OnlyMetadata = !fc.HasExecutableChanges()
		m.Stacks = fc.StackUpdates
		m.Policies = fc.PolicyUpdates
	}
	return json.Marshal(m)
}
func (fc *FormaCommand) UnmarshalSetupMetadata(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var m setupMetadata
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if m.Version > 1 {
		return fmt.Errorf("unsupported command setup format %d", m.Version)
	}
	if m.Version != 0 {
		fc.Setup = &m.SetupBoundary
	}
	fc.GeneratorUpdates = m.Generators
	fc.Resolution = m.Resolution
	fc.DrawGeneratorUpdates = m.Draws
	fc.DrawIntentKnown = m.DrawIntentKnown
	if m.Committed {
		fc.StackUpdates = m.Stacks
		fc.PolicyUpdates = m.Policies
	}
	return nil
}
func (fc *FormaCommand) CheckSetupRecovery() error {
	if fc.Setup == nil || fc.Setup.Version != 1 {
		return fmt.Errorf("command %s has unknown legacy setup intent; drain work before upgrading", fc.ID)
	}
	for _, u := range fc.StackUpdates {
		if u.State != stack_update.StackUpdateStateSuccess {
			return fmt.Errorf("command %s has incomplete stack setup", fc.ID)
		}
	}
	for _, u := range fc.PolicyUpdates {
		if u.State != policy_update.PolicyUpdateStateSuccess {
			return fmt.Errorf("command %s has incomplete policy setup", fc.ID)
		}
	}
	for _, u := range fc.GeneratorUpdates {
		if u.State != generator_update.GeneratorUpdateStateSuccess {
			return fmt.Errorf("command %s has incomplete generator setup", fc.ID)
		}
	}
	return nil
}
