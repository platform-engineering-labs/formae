// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package generator_update

import (
	"encoding/json"
	"net/url"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// GeneratorOperation and GeneratorUpdateState reuse the generic operation and
// state vocabularies from types, the same way stack_update does — a
// generator's row lifecycle is Create/Update/Delete only, with no
// attach/detach and no standalone form, so there is nothing
// generator-specific to add.
type (
	GeneratorOperation   = types.OperationType
	GeneratorUpdateState = types.GeneratorUpdateState
)

const (
	GeneratorOperationCreate GeneratorOperation = types.OperationCreate
	GeneratorOperationUpdate GeneratorOperation = types.OperationUpdate
	GeneratorOperationDelete GeneratorOperation = types.OperationDelete
	// GeneratorOperationDraw is the synthetic op that draws a value. It is
	// the only generator operation the ExecutionDAG schedules: the three
	// above write the generator's row and are persisted before the changeset
	// starts.
	GeneratorOperationDraw GeneratorOperation = types.OperationDraw

	GeneratorUpdateStateNotStarted = types.GeneratorUpdateStateNotStarted
	GeneratorUpdateStateInProgress = types.GeneratorUpdateStateInProgress
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

// changeset.Update interface implementation for ExecutionDAG integration.
// The interface type itself is not referenced here (importing the changeset
// package from generator_update would be an import cycle: changeset imports
// forma_persister, which imports forma_command, which imports
// generator_update) — see changeset/changeset_test.go and
// changeset/generator_update_node_uri_test.go for the compile-time
// assertion and the cross-format collision test.

// NodeURI renders generator://<stack>/<label>/<operation>, with StackLabel
// and the generator's label each percent-encoded (url.PathEscape) before
// joining. This scheme distinguishes it, by construction, from the other two
// Update kinds that share the same ExecutionDAG.Nodes keyspace:
//   - a resource operation URI (changeset.createOperationURI) is the bare
//     "<ksuid>/<propertyPath>/<operation>" with no scheme at all: a KSUID,
//     a property path, and an OperationType are each drawn from character
//     sets that never contain "://", so a resource URI can never begin with
//     "generator://".
//   - a target operation URI (target_update.TargetUpdate.NodeURI) is
//     "target://<label>/<operation>" — a different literal scheme, so it
//     can never equal a "generator://" URI either.
//
// StackLabel and a generator's label are both free-form user-authored
// strings that may themselves contain "/" — nothing in this codebase
// validates against it. Joining two such fields with "/" as a plain
// delimiter would let two distinct (stack, label) pairs collapse onto the
// same string: StackLabel="s", label="a/b" and StackLabel="s/a", label="b"
// both naively join to "s/a/b". url.PathEscape rules this out: it escapes
// every literal "/" (and "%") within a segment to a %XX triplet, so the
// only unescaped "/" characters ExecutionDAG ever sees in a generator URI
// are the delimiters this function inserts itself, and PathEscape/
// PathUnescape are exact inverses, so two different raw segments can never
// produce the same escaped output.
//
// This is deliberately per-segment escaping rather than keying on the
// generator's own KSUID (which is how ResourceUpdate avoids the same class
// of collision, and would make it structurally impossible rather than
// merely escaped): GetID() is not reliably populated on every path a
// GeneratorUpdate is constructed on today. GenerateGeneratorUpdates's create
// and update branches call Generator.SetID from genKeyToKsuid before
// building the update, but its reconcile-driven delete branch builds the
// update directly from a generator LoadGeneratorsByStack returned — and
// PasswordGenerator.ID is `json:"-"`, so nothing in the persisted
// generator_data JSON round-trips it back on load. Every reconcile-driven
// GeneratorOperationDelete update therefore carries GetID() == "" today.
func (gu *GeneratorUpdate) NodeURI() pkgmodel.FormaeURI {
	label := ""
	if gu.Generator != nil {
		label = gu.Generator.GetLabel()
	}
	return pkgmodel.FormaeURI("generator://" + url.PathEscape(gu.StackLabel) + "/" + url.PathEscape(label) + "/" + string(gu.Operation))
}

// Resolvables returns nil: a generator draws a value locally, it never
// resolves a $ref against a resource or target property.
func (gu *GeneratorUpdate) Resolvables() []pkgmodel.FormaeURI { return nil }

// Namespace returns a fixed pseudo-namespace. A generator draw calls no
// provider plugin, so it has no provider namespace to report. Because
// IsRateLimited is false, this value never gates concurrency in
// ExecutionDAG.GetExecutableUpdates — it only keeps generator nodes in
// their own bucket, distinct from any provider's.
func (gu *GeneratorUpdate) Namespace() string { return "generator" }

// IsRateLimited returns false: drawing a password is local CPU work with no
// provider call, so it needs no rate-limit token. A future generator arm
// that calls out to a provider (e.g. fetching a secret from a vault) would
// need to revisit this.
func (gu *GeneratorUpdate) IsRateLimited() bool { return false }

func (gu *GeneratorUpdate) IsReady() bool   { return gu.State == GeneratorUpdateStateNotStarted }
func (gu *GeneratorUpdate) IsRunning() bool { return gu.State == GeneratorUpdateStateInProgress }
func (gu *GeneratorUpdate) IsSuccess() bool { return gu.State == GeneratorUpdateStateSuccess }
func (gu *GeneratorUpdate) IsFailed() bool  { return gu.State == GeneratorUpdateStateFailed }

func (gu *GeneratorUpdate) MarkInProgress() { gu.State = GeneratorUpdateStateInProgress }
func (gu *GeneratorUpdate) MarkFailed()     { gu.State = GeneratorUpdateStateFailed }

// PersistGeneratorUpdates is a message to persist generator updates.
type PersistGeneratorUpdates struct {
	GeneratorUpdates []GeneratorUpdate
	CommandID        string
	StackIDMap       map[string]string // StackLabel -> StackID mapping
}
