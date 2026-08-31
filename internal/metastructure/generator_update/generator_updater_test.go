// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package generator_update

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stubGeneratorUpdaterLog swallows all log output.
type stubGeneratorUpdaterLog struct{ gen.Log }

func (stubGeneratorUpdaterLog) Trace(string, ...any)   {}
func (stubGeneratorUpdaterLog) Debug(string, ...any)   {}
func (stubGeneratorUpdaterLog) Info(string, ...any)    {}
func (stubGeneratorUpdaterLog) Warning(string, ...any) {}
func (stubGeneratorUpdaterLog) Error(string, ...any)   {}
func (stubGeneratorUpdaterLog) Panic(string, ...any)   {}

// stubGeneratorUpdaterProcess is a gen.Process double that records every Send
// so tests can inspect what the actor reported to its requester.
type stubGeneratorUpdaterProcess struct {
	gen.Process

	mu      sync.Mutex
	sends   []any
	sendErr error
}

func (p *stubGeneratorUpdaterProcess) Log() gen.Log { return stubGeneratorUpdaterLog{} }
func (p *stubGeneratorUpdaterProcess) PID() gen.PID { return gen.PID{Node: "test-node", ID: 1} }
func (p *stubGeneratorUpdaterProcess) Send(_ any, msg any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sendErr != nil {
		return p.sendErr
	}
	p.sends = append(p.sends, msg)
	return nil
}

func (p *stubGeneratorUpdaterProcess) sentFinishedMessages() []GeneratorUpdateFinished {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []GeneratorUpdateFinished
	for _, s := range p.sends {
		if m, ok := s.(GeneratorUpdateFinished); ok {
			out = append(out, m)
		}
	}
	return out
}

func (p *stubGeneratorUpdaterProcess) sentShutdowns() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, s := range p.sends {
		if _, ok := s.(Shutdown); ok {
			n++
		}
	}
	return n
}

// advanceCall records one AdvanceGeneration invocation.
type advanceCall struct {
	generatorID  string
	generationID string
	commandID    string
	drawnUnder   json.RawMessage
}

// recordingAdvancer is a generationAdvancer double.
type recordingAdvancer struct {
	calls []advanceCall
	err   error
}

func (r *recordingAdvancer) AdvanceGeneration(generatorID, generationID, commandID string, drawnUnder json.RawMessage) error {
	r.calls = append(r.calls, advanceCall{generatorID, generationID, commandID, drawnUnder})
	return r.err
}

// countingSource is a deterministic ByteSource: it hands out 0, 1, 2, ...
// wrapping at 256, so two independently constructed sources produce the same
// sequence and therefore the same drawn value.
func countingSource() pkgmodel.ByteSource {
	var n int
	return func(b []byte) (int, error) {
		for i := range b {
			b[i] = byte(n % 256)
			n++
		}
		return len(b), nil
	}
}

func testPasswordGenerator() *pkgmodel.PasswordGenerator {
	return &pkgmodel.PasswordGenerator{
		Label:     "db-password",
		Stack:     "app",
		ID:        "2abcDEFghiJKLmnoPQRstuVWxyz",
		Length:    24,
		Uppercase: true,
		Lowercase: true,
		Digits:    true,
	}
}

func drawingData(t *testing.T, op GeneratorOperation, advancer *recordingAdvancer) GeneratorUpdaterData {
	t.Helper()
	return GeneratorUpdaterData{
		generatorUpdate: GeneratorUpdate{
			Generator:  testPasswordGenerator(),
			Operation:  op,
			State:      GeneratorUpdateStateNotStarted,
			StackLabel: "app",
		},
		requestedBy: gen.PID{Node: "test-node", ID: 99},
		entropy:     countingSource(),
		datastore:   advancer,
	}
}

// TestGeneratorUpdater_SuccessCarriesDrawnValue asserts the finished signal
// carries the drawn value on success, and that the actor then shuts itself down.
func TestGeneratorUpdater_SuccessCarriesDrawnValue(t *testing.T) {
	data := drawingData(t, GeneratorOperationCreate, &recordingAdvancer{})
	data.drawnValue = "s3cret-value"
	data.generationID = "2mnoPQRstuVWxyzabcDEFghiJKL"

	proc := &stubGeneratorUpdaterProcess{}
	_, _, err := onGeneratorUpdaterStateChange(StateDrawing, StateFinishedSuccessfully, data, proc)
	require.NoError(t, err)

	finished := proc.sentFinishedMessages()
	require.Len(t, finished, 1, "exactly one GeneratorUpdateFinished must be sent on success")
	assert.Equal(t, GeneratorUpdateStateSuccess, finished[0].State)
	assert.Equal(t, "s3cret-value", finished[0].DrawnValue,
		"the drawn value must reach the requester through the finished signal")
	assert.Equal(t, "2mnoPQRstuVWxyzabcDEFghiJKL", finished[0].GenerationID,
		"the generation the value was drawn under travels with it, so destinations can be stamped with it")
	assert.Empty(t, finished[0].ErrorMessage)
	assert.Equal(t, 1, proc.sentShutdowns(), "the actor must terminate itself once it has reported")
}

// TestGeneratorUpdater_FailureOmitsDrawnValue asserts a failed draw reports no
// value at all, even if one is somehow present in the actor's data.
func TestGeneratorUpdater_FailureOmitsDrawnValue(t *testing.T) {
	data := drawingData(t, GeneratorOperationCreate, &recordingAdvancer{})
	data.drawnValue = "s3cret-value"
	data.generationID = "2mnoPQRstuVWxyzabcDEFghiJKL"
	data.errorMessage = failureReasonDrawFailed

	proc := &stubGeneratorUpdaterProcess{}
	_, _, err := onGeneratorUpdaterStateChange(StateDrawing, StateFinishedWithError, data, proc)
	require.NoError(t, err)

	finished := proc.sentFinishedMessages()
	require.Len(t, finished, 1, "exactly one GeneratorUpdateFinished must be sent on failure")
	assert.Equal(t, GeneratorUpdateStateFailed, finished[0].State)
	assert.Empty(t, finished[0].DrawnValue,
		"a failed draw must never propagate a value")
	assert.Empty(t, finished[0].GenerationID,
		"a failed draw attests no generation")
	assert.Equal(t, failureReasonDrawFailed, finished[0].ErrorMessage)
}

// TestGeneratorUpdater_DrawRecordsGenerationUnderTheSpec asserts a successful
// draw records a fresh generation against the generator's KSUID, under the
// marshalled spec, and that the recorded spec is not the drawn value.
func TestGeneratorUpdater_DrawRecordsGenerationUnderTheSpec(t *testing.T) {
	advancer := &recordingAdvancer{}
	data := drawingData(t, GeneratorOperationCreate, advancer)

	proc := &stubGeneratorUpdaterProcess{}
	state, out, _, err := handleDrawValue(gen.PID{}, StateDrawing, data, DrawValue{}, proc)
	require.NoError(t, err)
	require.Equal(t, StateFinishedSuccessfully, state)

	expected, err := pkgmodel.Draw(testPasswordGenerator(), countingSource())
	require.NoError(t, err)
	assert.Equal(t, expected, out.drawnValue, "the draw must consume the injected entropy source")
	assert.Len(t, out.drawnValue, 24)

	require.Len(t, advancer.calls, 1, "a successful draw records exactly one generation")
	call := advancer.calls[0]
	assert.Equal(t, testPasswordGenerator().ID, call.generatorID)
	assert.Len(t, call.generationID, 27, "the generation identity must be a fresh KSUID")
	assert.Equal(t, call.generationID, out.generationID,
		"the generation reported with the value is the one recorded for it, never one re-read afterwards")

	var spec pkgmodel.PasswordGenerator
	require.NoError(t, json.Unmarshal(call.drawnUnder, &spec),
		"drawnUnder must be the marshalled generator spec")
	assert.Equal(t, 24, spec.Length)
	assert.NotContains(t, string(call.drawnUnder), out.drawnValue,
		"the drawn value must never be written to the datastore")
}

// TestGeneratorUpdater_RecordingFailureDiscardsTheValue asserts that when the
// generation cannot be recorded the drawn value is discarded rather than
// reported, and the failure carries a fixed operator-facing reason.
func TestGeneratorUpdater_RecordingFailureDiscardsTheValue(t *testing.T) {
	advancer := &recordingAdvancer{err: datastoreError("connection refused to db-host:5432")}
	data := drawingData(t, GeneratorOperationCreate, advancer)

	proc := &stubGeneratorUpdaterProcess{}
	state, out, _, err := handleDrawValue(gen.PID{}, StateDrawing, data, DrawValue{}, proc)
	require.NoError(t, err)

	assert.Equal(t, StateFinishedWithError, state)
	assert.Empty(t, out.drawnValue, "a value that could not be recorded must not be kept")
	assert.Equal(t, failureReasonGenerationNotRecorded, out.errorMessage)
	assert.NotContains(t, out.errorMessage, "db-host",
		"the operator-facing message must not carry raw error text")
}

// TestGeneratorUpdater_UndrawableSpecMapsToAFixedReason asserts a spec that
// admits no value fails with a fixed reason and never reaches the datastore.
func TestGeneratorUpdater_UndrawableSpecMapsToAFixedReason(t *testing.T) {
	advancer := &recordingAdvancer{}
	data := drawingData(t, GeneratorOperationCreate, advancer)
	data.generatorUpdate.Generator = &pkgmodel.PasswordGenerator{
		Label:  "db-password",
		Stack:  "app",
		ID:     "2abcDEFghiJKLmnoPQRstuVWxyz",
		Length: 0,
	}

	proc := &stubGeneratorUpdaterProcess{}
	state, out, _, err := handleDrawValue(gen.PID{}, StateDrawing, data, DrawValue{}, proc)
	require.NoError(t, err)

	assert.Equal(t, StateFinishedWithError, state)
	assert.Equal(t, failureReasonDrawFailed, out.errorMessage)
	assert.False(t, strings.Contains(out.errorMessage, "non-positive"),
		"the operator-facing message must not carry raw error text")
	assert.Empty(t, advancer.calls, "a failed draw must not advance the generation")
}

// TestGeneratorUpdater_DeleteDrawsNothing asserts a delete node finishes
// successfully without drawing a value or touching the datastore: the
// generator row was already removed before the changeset started.
func TestGeneratorUpdater_DeleteDrawsNothing(t *testing.T) {
	advancer := &recordingAdvancer{}
	data := drawingData(t, GeneratorOperationDelete, advancer)

	proc := &stubGeneratorUpdaterProcess{}
	state, out, _, err := handleDrawValue(gen.PID{}, StateDrawing, data, DrawValue{}, proc)
	require.NoError(t, err)

	assert.Equal(t, StateFinishedSuccessfully, state)
	assert.Empty(t, out.drawnValue)
	assert.Empty(t, advancer.calls)
}

// TestGeneratorUpdater_UnknownIdentityFailsBeforeDrawing asserts a generator
// with no KSUID fails before any value is drawn, since the generation it would
// be drawn under could not be recorded against anything.
func TestGeneratorUpdater_UnknownIdentityFailsBeforeDrawing(t *testing.T) {
	advancer := &recordingAdvancer{}
	data := drawingData(t, GeneratorOperationCreate, advancer)
	data.generatorUpdate.Generator.SetID("")

	proc := &stubGeneratorUpdaterProcess{}
	state, out, _, err := handleDrawValue(gen.PID{}, StateDrawing, data, DrawValue{}, proc)
	require.NoError(t, err)

	assert.Equal(t, StateFinishedWithError, state)
	assert.Equal(t, failureReasonUnknownIdentity, out.errorMessage)
	assert.Empty(t, out.drawnValue)
	assert.Empty(t, advancer.calls)
}

// TestGeneratorUpdaterActorName_DistinguishesStacks asserts two generators
// sharing a label in different stacks get distinct actor names, so one command
// touching both spawns two actors rather than colliding on one.
func TestGeneratorUpdaterActorName_DistinguishesStacks(t *testing.T) {
	a := GeneratorUpdate{Generator: &pkgmodel.PasswordGenerator{Label: "db-password"}, Operation: GeneratorOperationCreate, StackLabel: "app"}
	b := GeneratorUpdate{Generator: &pkgmodel.PasswordGenerator{Label: "db-password"}, Operation: GeneratorOperationCreate, StackLabel: "batch"}

	assert.NotEqual(t,
		actornames.GeneratorUpdater(a.NodeURI(), "cmd-1"),
		actornames.GeneratorUpdater(b.NodeURI(), "cmd-1"))
}

// TestGeneratorUpdater_UndeliverableDrawTriggerIsNotASpecFailure asserts that
// failing to hand the draw to ourselves is reported as its own reason. The
// generator's spec is not at fault, so the operator must not be sent to check
// its length and character classes.
func TestGeneratorUpdater_UndeliverableDrawTriggerIsNotASpecFailure(t *testing.T) {
	data := drawingData(t, GeneratorOperationCreate, &recordingAdvancer{})
	proc := &stubGeneratorUpdaterProcess{sendErr: datastoreError("mailbox full")}

	state, out, _, err := handleStartGeneratorUpdate(gen.PID{}, StateNotStarted, data,
		StartGeneratorUpdate{GeneratorUpdate: data.generatorUpdate}, proc)
	require.NoError(t, err)

	assert.Equal(t, StateFinishedWithError, state)
	assert.Equal(t, failureReasonDrawNotStarted, out.errorMessage)
	assert.NotEqual(t, failureReasonDrawFailed, out.errorMessage)
	assert.Empty(t, out.drawnValue)
}

// TestGeneratorUpdater_EachDrawGetsAFreshGeneration asserts two successive
// draws record two different generation identities. The generation ID is the
// rotation's identity, and a later apply compares a digest of it against what
// each destination was stamped with, so a repeated value would make a fresh
// draw indistinguishable from the previous one.
func TestGeneratorUpdater_EachDrawGetsAFreshGeneration(t *testing.T) {
	advancer := &recordingAdvancer{}
	proc := &stubGeneratorUpdaterProcess{}

	for range 2 {
		state, _, _, err := handleDrawValue(gen.PID{}, StateDrawing, drawingData(t, GeneratorOperationCreate, advancer), DrawValue{}, proc)
		require.NoError(t, err)
		require.Equal(t, StateFinishedSuccessfully, state)
	}

	require.Len(t, advancer.calls, 2)
	assert.NotEqual(t, advancer.calls[0].generationID, advancer.calls[1].generationID,
		"every draw must record a fresh generation identity")
}

// TestGeneratorUpdater_FaultyEntropyFailsWithoutAValue asserts a byte source
// that cannot deliver — either by erroring or by returning fewer bytes than
// asked for without an error — fails the draw with the fixed reason, attaches
// no value, and never advances the generation.
func TestGeneratorUpdater_FaultyEntropyFailsWithoutAValue(t *testing.T) {
	tests := []struct {
		name   string
		source pkgmodel.ByteSource
	}{
		{
			name: "source returns an error",
			source: func([]byte) (int, error) {
				return 0, datastoreError("entropy pool exhausted at /dev/urandom")
			},
		},
		{
			name:   "source short-reads without an error",
			source: func([]byte) (int, error) { return 0, nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			advancer := &recordingAdvancer{}
			data := drawingData(t, GeneratorOperationCreate, advancer)
			data.entropy = tt.source

			proc := &stubGeneratorUpdaterProcess{}
			state, out, _, err := handleDrawValue(gen.PID{}, StateDrawing, data, DrawValue{}, proc)
			require.NoError(t, err)

			assert.Equal(t, StateFinishedWithError, state)
			assert.Equal(t, failureReasonDrawFailed, out.errorMessage)
			assert.NotContains(t, out.errorMessage, "urandom",
				"the operator-facing message must not carry raw error text")
			assert.Empty(t, out.drawnValue)
			assert.Empty(t, advancer.calls, "a failed draw must not advance the generation")
		})
	}
}

// datastoreError builds an error carrying text that must not reach an operator.
type datastoreError string

func (e datastoreError) Error() string { return string(e) }
