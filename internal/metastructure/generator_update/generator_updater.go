// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package generator_update

import (
	"crypto/rand"
	"encoding/json"
	"fmt"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	StateNotStarted           = gen.Atom("not_started")
	StateDrawing              = gen.Atom("drawing")
	StateFinishedSuccessfully = gen.Atom("finished_successfully")
	StateFinishedWithError    = gen.Atom("finished_with_error")
)

// Operator-facing failure reasons. Like resource_update's
// updateRequestFailureReason/createRequestFailureReason, a draw failure is
// mapped to one of these fixed strings rather than surfacing the underlying
// error: this text is projected to the API as GeneratorUpdate.ErrorMessage,
// and the errors behind it name spec internals, database hosts and, in the
// worst case, could be made to echo input.
const (
	failureReasonDrawFailed = "cannot draw a value for this generator: its specification admits no value. Check its length and its character-class settings."

	failureReasonUnknownIdentity = "cannot draw a value for this generator: formae holds no identity for it, so it has nothing to record the new generation against."

	failureReasonSpecNotRecordable = "cannot draw a value for this generator: formae could not record the specification the value would be drawn under."

	failureReasonGenerationNotRecorded = "cannot draw a value for this generator: the value was drawn but its generation could not be recorded, so the value was discarded. Retry the apply."

	// Distinct from failureReasonDrawFailed on purpose: the generator's spec
	// is fine here, the actor could not hand the draw to itself. Telling the
	// operator to check the length and character classes would send them
	// looking for a fault that is not there.
	failureReasonDrawNotStarted = "cannot draw a value for this generator: formae could not start the draw. Retry the apply."
)

// generationAdvancer is the minimal datastore surface this actor needs: it
// records that a new generation was drawn for a generator. Declared locally
// rather than taking datastore.Datastore because internal/datastore imports
// this package (through forma_command), so importing it back would cycle.
type generationAdvancer interface {
	AdvanceGeneration(generatorID, generationID string, drawnUnder json.RawMessage) error
}

// GeneratorUpdater is an FSM actor that draws one generator's value, records
// the generation it was drawn under, and reports the value to its requester.
//
// The drawn value exists in exactly two places, both of them in memory and
// both of them for the lifetime of a single draw: GeneratorUpdaterData's
// drawnValue field, and the GeneratorUpdateFinished message this actor sends
// its requester before terminating. It is never written to the datastore,
// never placed on the GeneratorUpdate (which IS persisted, and is projected
// to the API), and never logged.
type GeneratorUpdater struct {
	statemachine.StateMachine[GeneratorUpdaterData]
}

// StartGeneratorUpdate is sent to the GeneratorUpdater to begin a draw.
//
// It carries no CommandID, unlike StartTargetUpdate: a TargetUpdater needs one
// to address the per-command ResolveCache actor and to stamp the persist
// message it sends the ResourcePersister, and this actor sends neither. The
// command is already in the actor's registered name (see
// actornames.GeneratorUpdater), which is the caller's to build.
type StartGeneratorUpdate struct {
	GeneratorUpdate GeneratorUpdate
}

// DrawValue is sent by the actor to itself to leave StateNotStarted before
// doing the draw, so the actor is observably in StateDrawing while it runs
// rather than still reporting StateNotStarted. Exported for the same reason
// Shutdown is: ergo dispatches handlers on the concrete message type.
type DrawValue struct{}

// GeneratorUpdateFinished is sent back to the requester when the draw
// completes. It is the ONLY carrier of the drawn value — the analogue of
// TargetUpdateFinished.ResolvedConfig. DrawnValue is populated on success and
// only on success; ErrorMessage is populated on failure and only on failure,
// always from the fixed set of reasons above.
type GeneratorUpdateFinished struct {
	NodeURI      pkgmodel.FormaeURI
	State        GeneratorUpdateState
	DrawnValue   string
	ErrorMessage string
}

// Shutdown is sent to terminate the GeneratorUpdater process.
type Shutdown struct{}

// GeneratorUpdaterData holds the FSM's internal state. It is process-local
// and is never persisted.
type GeneratorUpdaterData struct {
	generatorUpdate GeneratorUpdate
	requestedBy     gen.PID
	// entropy is the source Draw reads random bytes from: crypto/rand.Read in
	// production, a deterministic source in tests so a specific drawn value is
	// reproducible without relying on chance.
	entropy pkgmodel.ByteSource
	// datastore records the generation advance. Nothing else about a
	// generator is written from here.
	datastore generationAdvancer
	// drawnValue holds the live credential between the draw and the finished
	// signal. It is deliberately unexported and deliberately absent from
	// GeneratorUpdate: the update is marshalled into the command record, this
	// struct is not.
	drawnValue string
	// errorMessage is one of the fixed operator-facing reasons above, set
	// only on the failure path.
	errorMessage string
}

func NewGeneratorUpdater() gen.ProcessBehavior {
	return &GeneratorUpdater{}
}

func (g *GeneratorUpdater) Init(args ...any) (statemachine.StateMachineSpec[GeneratorUpdaterData], error) {
	data := GeneratorUpdaterData{
		requestedBy: args[0].(gen.PID),
		entropy:     rand.Read,
	}

	// The datastore is mandatory, unlike TargetUpdater's optional re-read
	// handle: without it a drawn generation cannot be recorded, and a value
	// handed out under a generation nobody stored is exactly the state the
	// next apply cannot reason about.
	ds, ok := g.Env("Datastore")
	if !ok {
		g.Log().Error("GeneratorUpdater: missing 'Datastore' environment variable")
		return statemachine.StateMachineSpec[GeneratorUpdaterData]{}, fmt.Errorf("generatorUpdater: missing 'Datastore' environment variable")
	}
	advancer, ok := ds.(generationAdvancer)
	if !ok {
		g.Log().Error("GeneratorUpdater: 'Datastore' does not record generations")
		return statemachine.StateMachineSpec[GeneratorUpdaterData]{}, fmt.Errorf("generatorUpdater: 'Datastore' does not implement AdvanceGeneration")
	}
	data.datastore = advancer

	g.Log().Debug("GeneratorUpdater %s initialized", g.Name())

	return statemachine.NewStateMachineSpec(StateNotStarted,
		statemachine.WithData(data),
		statemachine.WithStateEnterCallback(onGeneratorUpdaterStateChange),
		// Not started — waiting for StartGeneratorUpdate.
		statemachine.WithStateMessageHandler(StateNotStarted, handleStartGeneratorUpdate),
		statemachine.WithStateMessageHandler(StateNotStarted, shutdownGeneratorUpdater),
		// Drawing.
		statemachine.WithStateMessageHandler(StateDrawing, handleDrawValue),
		statemachine.WithStateMessageHandler(StateDrawing, shutdownGeneratorUpdater),
		// Terminal states.
		statemachine.WithStateMessageHandler(StateFinishedSuccessfully, shutdownGeneratorUpdater),
		statemachine.WithStateMessageHandler(StateFinishedWithError, shutdownGeneratorUpdater),
	), nil
}

// handleStartGeneratorUpdate records the request and enters StateDrawing. The
// draw itself happens in handleDrawValue, off a message this actor sends
// itself, so the FSM reports the state it is actually in while drawing.
func handleStartGeneratorUpdate(from gen.PID, state gen.Atom, data GeneratorUpdaterData, message StartGeneratorUpdate, proc gen.Process) (gen.Atom, GeneratorUpdaterData, []statemachine.Action, error) {
	data.generatorUpdate = message.GeneratorUpdate

	if err := proc.Send(proc.PID(), DrawValue{}); err != nil {
		proc.Log().Error("GeneratorUpdater: failed to send draw message node=%s: %v", data.generatorUpdate.NodeURI(), err)
		data.errorMessage = failureReasonDrawNotStarted
		return StateFinishedWithError, data, nil, nil
	}

	return StateDrawing, data, nil, nil
}

// handleDrawValue draws the value, records the generation it was drawn under,
// and moves to a terminal state.
//
// The order is deliberate. Everything that can fail without a live credential
// in hand is done first: identity, then the marshalled spec. The draw itself
// comes next, and the generation is recorded BEFORE success is reported, so a
// value can never reach a destination under a generation the datastore does
// not know about. If the recording fails the drawn value is dropped on the
// floor here rather than reported.
//
// No log line in this function formats the generator's spec or its value —
// identity only.
func handleDrawValue(from gen.PID, state gen.Atom, data GeneratorUpdaterData, message DrawValue, proc gen.Process) (gen.Atom, GeneratorUpdaterData, []statemachine.Action, error) {
	nodeURI := data.generatorUpdate.NodeURI()

	// A delete has nothing to draw: the generator's row was already removed
	// by PersistGeneratorUpdates before the changeset started, so the node's
	// work is done. Drawing here would mint a credential for a generator that
	// no longer exists and then try to advance a tombstoned row's generation.
	if data.generatorUpdate.Operation == GeneratorOperationDelete {
		return StateFinishedSuccessfully, data, nil, nil
	}

	spec := data.generatorUpdate.Generator

	// The KSUID comes off the generator on the update, and is the same value
	// CreateGenerator/UpdateGenerator persisted and the same value any $gen
	// reference in this command was translated to. For a generator this
	// command declares, GenerateGeneratorUpdates stamped it from the
	// translation phase's genKeyToKsuid map; for one it only references,
	// SynthesizeDrawGeneratorUpdates stamps it after loading the generator —
	// a LOADED generator carries no ID of its own, since
	// PasswordGenerator.ID is `json:"-"` and the KSUID lives only in the
	// generators table.
	generatorID := ""
	if spec != nil {
		generatorID = spec.GetID()
	}
	if generatorID == "" {
		proc.Log().Error("GeneratorUpdater: no generator identity to record a generation against node=%s", nodeURI)
		data.errorMessage = failureReasonUnknownIdentity
		return StateFinishedWithError, data, nil, nil
	}

	// drawnUnder is the generator SPEC, never the value: it lands in a JSONB
	// column that nothing in the stack redacts.
	drawnUnder, err := json.Marshal(spec)
	if err != nil {
		proc.Log().Error("GeneratorUpdater: failed to marshal the generator spec node=%s", nodeURI)
		data.errorMessage = failureReasonSpecNotRecordable
		return StateFinishedWithError, data, nil, nil
	}

	// A fresh KSUID: this is the generation's identity, which a later apply
	// digests and compares against what each destination was stamped with. It
	// is not a digest of anything itself.
	generationID := util.NewID()

	value, err := pkgmodel.Draw(spec, data.entropy)
	if err != nil {
		// The error names spec internals; only the fixed reason is reported.
		proc.Log().Error("GeneratorUpdater: failed to draw a value node=%s", nodeURI)
		data.errorMessage = failureReasonDrawFailed
		return StateFinishedWithError, data, nil, nil
	}

	if err := data.datastore.AdvanceGeneration(generatorID, generationID, drawnUnder); err != nil {
		proc.Log().Error("GeneratorUpdater: failed to record the generation node=%s generation=%s", nodeURI, generationID)
		data.errorMessage = failureReasonGenerationNotRecorded
		return StateFinishedWithError, data, nil, nil
	}

	data.drawnValue = value
	return StateFinishedSuccessfully, data, nil, nil
}

// onGeneratorUpdaterStateChange reports the outcome to the requester on
// reaching a terminal state, then shuts the actor down. The drawn value is
// attached to the finished message on the success branch and nowhere else, so
// a failure — including one that happened after a value was already drawn —
// can never propagate one.
func onGeneratorUpdaterStateChange(oldState gen.Atom, newState gen.Atom, data GeneratorUpdaterData, proc gen.Process) (gen.Atom, GeneratorUpdaterData, error) {
	if newState != StateFinishedSuccessfully && newState != StateFinishedWithError {
		return newState, data, nil
	}

	finished := GeneratorUpdateFinished{
		NodeURI: data.generatorUpdate.NodeURI(),
		State:   GeneratorUpdateStateFailed,
	}
	if newState == StateFinishedSuccessfully {
		finished.State = GeneratorUpdateStateSuccess
		finished.DrawnValue = data.drawnValue
	} else {
		finished.ErrorMessage = data.errorMessage
	}

	proc.Log().Debug("GeneratorUpdater: sending GeneratorUpdateFinished to requester state=%s node=%s", newState, finished.NodeURI)
	if err := proc.Send(data.requestedBy, finished); err != nil {
		proc.Log().Error("GeneratorUpdater: failed to send GeneratorUpdateFinished to requester: %v", err)
	}

	// Send ourselves a shutdown message to terminate the process.
	if err := proc.Send(proc.PID(), Shutdown{}); err != nil {
		proc.Log().Error("GeneratorUpdater: failed to send shutdown message: %v", err)
	}

	return newState, data, nil
}

func shutdownGeneratorUpdater(from gen.PID, state gen.Atom, data GeneratorUpdaterData, shutdown Shutdown, proc gen.Process) (gen.Atom, GeneratorUpdaterData, []statemachine.Action, error) {
	return state, data, nil, gen.TerminateReasonNormal
}
