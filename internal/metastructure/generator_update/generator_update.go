// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package generator_update

import (
	"encoding/json"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// GeneratorOperation and GeneratorUpdateState reuse the generic operation and
// state vocabularies from types, the same way stack_update does — a
// generator's lifecycle is Create/Update/Delete only, with no attach/detach
// and no standalone form, so there is nothing generator-specific to add.
type (
	GeneratorOperation   = types.OperationType
	GeneratorUpdateState = types.GeneratorUpdateState
)

const (
	GeneratorOperationCreate GeneratorOperation = types.OperationCreate
	GeneratorOperationUpdate GeneratorOperation = types.OperationUpdate
	GeneratorOperationDelete GeneratorOperation = types.OperationDelete

	GeneratorUpdateStateNotStarted = types.GeneratorUpdateStateNotStarted
	GeneratorUpdateStateSuccess    = types.GeneratorUpdateStateSuccess
	GeneratorUpdateStateFailed     = types.GeneratorUpdateStateFailed
)

// GeneratorUpdate represents a single generator change: a create, an update
// (spec change and/or rename), or a delete.
type GeneratorUpdate struct {
	Generator         pkgmodel.Generator   `json:"-"`
	ExistingGenerator pkgmodel.Generator   `json:"-"`
	Operation         GeneratorOperation   `json:"Operation"`
	State             GeneratorUpdateState `json:"State"`
	StackLabel        string               `json:"StackLabel"`
	StartTs           time.Time            `json:"StartTs"`
	ModifiedTs        time.Time            `json:"ModifiedTs"`
	Version           string               `json:"Version"`
	ErrorMessage      string               `json:"ErrorMessage,omitempty"`
}

// generatorUpdateJSON is a helper struct for JSON marshaling/unmarshaling,
// mirroring policyUpdateJSON: Generator is an interface, so it round-trips
// through a discriminated json.RawMessage rather than being marshaled
// directly.
type generatorUpdateJSON struct {
	Generator         json.RawMessage      `json:"Generator,omitempty"`
	ExistingGenerator json.RawMessage      `json:"ExistingGenerator,omitempty"`
	Operation         GeneratorOperation   `json:"Operation"`
	State             GeneratorUpdateState `json:"State"`
	StackLabel        string               `json:"StackLabel"`
	StartTs           time.Time            `json:"StartTs"`
	ModifiedTs        time.Time            `json:"ModifiedTs"`
	Version           string               `json:"Version"`
	ErrorMessage      string               `json:"ErrorMessage,omitempty"`
}

// MarshalJSON implements custom JSON marshaling for GeneratorUpdate.
func (gu GeneratorUpdate) MarshalJSON() ([]byte, error) {
	var generatorJSON, existingGeneratorJSON json.RawMessage
	var err error

	if gu.Generator != nil {
		generatorJSON, err = json.Marshal(gu.Generator)
		if err != nil {
			return nil, err
		}
	}

	if gu.ExistingGenerator != nil {
		existingGeneratorJSON, err = json.Marshal(gu.ExistingGenerator)
		if err != nil {
			return nil, err
		}
	}

	return json.Marshal(generatorUpdateJSON{
		Generator:         generatorJSON,
		ExistingGenerator: existingGeneratorJSON,
		Operation:         gu.Operation,
		State:             gu.State,
		StackLabel:        gu.StackLabel,
		StartTs:           gu.StartTs,
		ModifiedTs:        gu.ModifiedTs,
		Version:           gu.Version,
		ErrorMessage:      gu.ErrorMessage,
	})
}

// UnmarshalJSON implements custom JSON unmarshaling for GeneratorUpdate.
func (gu *GeneratorUpdate) UnmarshalJSON(data []byte) error {
	var helper generatorUpdateJSON
	if err := json.Unmarshal(data, &helper); err != nil {
		return err
	}

	gu.Operation = helper.Operation
	gu.State = helper.State
	gu.StackLabel = helper.StackLabel
	gu.StartTs = helper.StartTs
	gu.ModifiedTs = helper.ModifiedTs
	gu.Version = helper.Version
	gu.ErrorMessage = helper.ErrorMessage

	if len(helper.Generator) > 0 {
		generator, err := pkgmodel.ParseGenerator(helper.Generator)
		if err != nil {
			return err
		}
		gu.Generator = generator
	}

	if len(helper.ExistingGenerator) > 0 {
		existingGenerator, err := pkgmodel.ParseGenerator(helper.ExistingGenerator)
		if err != nil {
			return err
		}
		gu.ExistingGenerator = existingGenerator
	}

	return nil
}

// PersistGeneratorUpdates is a message to persist generator updates.
type PersistGeneratorUpdates struct {
	GeneratorUpdates []GeneratorUpdate
	CommandID        string
	StackIDMap       map[string]string // StackLabel -> StackID mapping
}
