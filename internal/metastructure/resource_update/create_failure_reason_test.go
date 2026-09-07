// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// errSpawnRefused short-circuits a state function before the plugin request is
// sent, the shape a dispatch failure takes when the coordinator never returns an
// operator.
var errSpawnRefused = errors.New("plugin coordinator refused to spawn an operator")

// spawnFailingProcess fails the coordinator's spawn request, so a state function
// that finished preparing its plugin request cannot dispatch it.
type spawnFailingProcess struct {
	*stubUpdaterProcess
	log *capturingLog
}

func (p *spawnFailingProcess) Log() gen.Log { return p.log }

func (p *spawnFailingProcess) Call(_ any, _ any) (any, error) { return nil, errSpawnRefused }

func newSpawnFailingProcess() *spawnFailingProcess {
	return &spawnFailingProcess{stubUpdaterProcess: &stubUpdaterProcess{}, log: &capturingLog{}}
}

// capturedCreate returns the plugin.CreateResource create() assembled, failing
// the test when the state function never reached the plugin call.
func (p *operationCapturingProcess) capturedCreate(t *testing.T) plugin.CreateResource {
	t.Helper()
	require.NotNil(t, p.operation, "create() never reached the plugin call")
	op, ok := p.operation.(plugin.CreateResource)
	require.True(t, ok, "expected a CreateResource operation, got %T", p.operation)
	return op
}

// createForOpaqueSecret builds the ResourceUpdateData for creating a secret
// resource whose opaque property carries the given leaf. A create has no prior
// state: the desired properties are the whole input to the plugin request.
func createForOpaqueSecret(secretLeaf string) ResourceUpdateData {
	ru := &ResourceUpdate{
		Operation: OperationCreate,
		DesiredState: pkgmodel.Resource{
			Label: "identity-key", Type: "AWS::SecretsManager::Secret", Stack: "default",
			Schema:     secretSchema(),
			Properties: json.RawMessage(`{"Name":"n","Description":"d","SecretString":` + secretLeaf + `}`),
		},
		ResourceTarget: pkgmodel.Target{Label: "us-east-1", Namespace: "aws", Config: json.RawMessage(`{}`)},
	}
	return ResourceUpdateData{resourceUpdate: ru, commandID: "cmd-1", originalResourceKsuidURI: ru.DesiredState.URI()}
}

// A create that fails while preparing the plugin request never records plugin
// progress, so without an explicit reason the operator sees an empty
// ErrorMessage. The site must record one, and the recorded text must be the
// fixed text for its category — an equality check, because anything appended to
// it would be error detail that can name a property path.
func TestCreate_PrePluginFailure_RecordsRedactedFailureReason(t *testing.T) {
	const plaintext = "the-real-secret"
	digest := pkgmodel.ComputeValueHash(plaintext)

	cases := map[string]struct {
		build  func() ResourceUpdateData
		reason string
	}{
		// A stored hash in the desired properties is exactly what the guarded
		// conversion refuses to send to a provider as the live value.
		"stored hash in the desired properties": {
			build:  func() ResourceUpdateData { return createForOpaqueSecret(hashedLeaf(digest)) },
			reason: failureReasonUnrecoverableOpaqueValueOnCreate,
		},
		// Any other preparation failure — here a document the conversion cannot
		// decode — falls back to the generic reason.
		"undecodable desired document": {
			build: func() ResourceUpdateData {
				data := createForOpaqueSecret(`{"$visibility":"Opaque","$value":"` + plaintext + `"}`)
				data.resourceUpdate.DesiredState.Properties = json.RawMessage(`{"Broken":{"$ref":`)
				return data
			},
			reason: failureReasonPluginRequestPreparationOnCreate,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			data := tc.build()
			proc := newOperationCapturingProcess()
			state, _, _, err := create(StateCreating, data, proc)
			require.NoError(t, err)
			require.Equal(t, StateFinishedWithError, state, "preparing the plugin request must fail")
			require.Contains(t, strings.Join(proc.log.all(), "\n"), "failed to convert resource properties for plugin",
				"the intended failure site must be the one that fired")

			message := data.resourceUpdate.MostRecentFailureMessage()
			require.NotEmpty(t, message, "a pre-plugin failure must still surface a reason")
			assert.Equal(t, tc.reason, message, "the reason must be the fixed text for its category, with no error detail appended")
			assert.NotContains(t, message, plaintext, "the reason must not carry the secret")
			assert.NotContains(t, message, digest, "the reason must not carry the stored digest")
			assert.NotRegexp(t, anySHA256, message)
		})
	}
}

// The underlying error names the property that failed, and a property path is
// built from user-authored map keys. The log keeps that error; the recorded
// reason must carry none of it.
func TestCreate_PrePluginFailure_DoesNotLeakUserAuthoredInput(t *testing.T) {
	const plaintext = "the-real-secret"
	const marker = "CANARY"
	const hostileKey = "tenant.eu-west-1-" + marker
	digest := pkgmodel.ComputeValueHash(plaintext)

	// The same stored hash as the category case, relocated under a map key the
	// user chose.
	data := createForOpaqueSecret(hashedLeaf(digest))
	data.resourceUpdate.DesiredState.Schema = pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", hostileKey},
		Hints:      map[string]pkgmodel.FieldHint{hostileKey + ".SecretString": {Opaque: true, WriteOnly: true}},
	}
	data.resourceUpdate.DesiredState.Properties = json.RawMessage(
		`{"Name":"n","` + hostileKey + `":{"SecretString":` + hashedLeaf(digest) + `}}`)

	proc := newOperationCapturingProcess()
	state, _, _, err := create(StateCreating, data, proc)
	require.NoError(t, err)
	require.Equal(t, StateFinishedWithError, state, "preparing the plugin request must fail")

	logged := strings.Join(proc.log.all(), "\n")
	require.Contains(t, logged, "failed to convert resource properties for plugin",
		"the intended failure site must be the one that fired")
	require.Contains(t, logged, hostileKey, "the underlying error must genuinely carry the user-authored key")

	message := data.resourceUpdate.MostRecentFailureMessage()
	assert.Equal(t, failureReasonUnrecoverableOpaqueValueOnCreate, message)
	assert.NotContains(t, message, hostileKey, "the reason must not carry a user-authored property path")
	assert.NotContains(t, message, marker, "the reason must not carry any part of a user-authored key")
	assert.NotContains(t, message, plaintext, "the reason must not carry the secret")
	assert.NotContains(t, message, digest, "the reason must not carry the stored digest")
	assert.NotRegexp(t, anySHA256, message)
}

// Recording a reason must not change what fails: a create whose opaque value is
// live plaintext still reaches the plugin, carrying that plaintext.
func TestCreate_LivePlaintextSecret_ReachesPlugin(t *testing.T) {
	const plaintext = "brand-new-secret"
	data := createForOpaqueSecret(`{"$visibility":"Opaque","$value":"` + plaintext + `"}`)
	proc := newOperationCapturingProcess()

	_, _, _, err := create(StateCreating, data, proc)
	require.NoError(t, err)

	op := proc.capturedCreate(t)
	properties := map[string]any{}
	require.NoError(t, json.Unmarshal(op.Properties, &properties))
	assert.Equal(t, plaintext, properties["SecretString"], "a live plaintext secret must reach the provider as the value to write")
	assert.Equal(t, "d", properties["Description"], "the rest of the desired properties must reach the provider unchanged")
}

// A dispatch failure takes two shapes: the coordinator never returns an
// operator, so nothing was sent, and the call to an operator that was spawned
// does not complete, so the create may already be running at the provider. They
// are one category and must report one text, and that text is the one the second
// shape forces: it may not tell an operator the create never started.
func TestCreate_DispatchFailure_RecordsConservativeFailureReason(t *testing.T) {
	cases := map[string]struct {
		// shape returns the double, the log it captures, and a check on what the
		// double did with the plugin request before failing.
		shape func() (gen.Process, *capturingLog, func(*testing.T))
	}{
		"the coordinator never returned an operator": {
			shape: func() (gen.Process, *capturingLog, func(*testing.T)) {
				proc := newSpawnFailingProcess()
				return proc, proc.log, func(t *testing.T) {
					t.Helper()
					require.Contains(t, strings.Join(proc.log.all(), "\n"), errSpawnRefused.Error(),
						"this shape must fail before the create request is sent")
				}
			},
		},
		"the create was sent and the call did not complete": {
			shape: func() (gen.Process, *capturingLog, func(*testing.T)) {
				proc := newOperationCapturingProcess()
				return proc, proc.log, func(t *testing.T) {
					t.Helper()
					proc.capturedCreate(t)
				}
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			data := createForOpaqueSecret(`{"$visibility":"Opaque","$value":"brand-new-secret"}`)
			proc, log, checkRequest := tc.shape()

			state, _, _, err := create(StateCreating, data, proc)
			require.NoError(t, err)
			require.Equal(t, StateFinishedWithError, state, "dispatching the plugin request must fail")
			require.Contains(t, strings.Join(log.all(), "\n"), "failed to start create operation",
				"the intended failure site must be the one that fired")
			checkRequest(t)

			message := data.resourceUpdate.MostRecentFailureMessage()
			require.NotEmpty(t, message, "a dispatch failure must still surface a reason")
			assert.Equal(t, failureReasonPluginDispatchOnCreate, message,
				"both shapes must report the fixed dispatch text, with no error detail appended")
			assert.Contains(t, message, "may or may not have been created",
				"the reason must leave open that the provider already created the resource")
			assert.Contains(t, message, "check the provider before retrying",
				"the reason must tell an operator what to do before a retry")
		})
	}
}

// A reason belongs to the pass that recorded it. The pass that follows records
// its own, and a pass that succeeds records none, so an operator never reads a
// reason describing an attempt that is over.
func TestCreate_StaleFailureReason_DoesNotOutliveItsPass(t *testing.T) {
	const stale = "cannot create this resource: an earlier attempt could not build the provider request for it."

	t.Run("a failing pass reports its own reason", func(t *testing.T) {
		data := createForOpaqueSecret(`{"$visibility":"Opaque","$value":"brand-new-secret"}`)
		data.resourceUpdate.FailureReason = stale
		proc := newOperationCapturingProcess()

		state, _, _, err := create(StateCreating, data, proc)
		require.NoError(t, err)
		require.Equal(t, StateFinishedWithError, state, "dispatching the plugin request must fail")

		message := data.resourceUpdate.MostRecentFailureMessage()
		assert.Equal(t, failureReasonPluginDispatchOnCreate, message,
			"the failure an operator reads must be the one this pass hit")
		assert.NotEqual(t, stale, message, "a reason from an earlier attempt must not be reported")
	})

	t.Run("a succeeding pass reports no failure", func(t *testing.T) {
		data := createForOpaqueSecret(`{"$visibility":"Opaque","$value":"brand-new-secret"}`)
		data.resourceUpdate.FailureReason = stale

		data.resourceUpdate.MarkAsSuccess()

		assert.Empty(t, data.resourceUpdate.MostRecentFailureMessage(),
			"a create that succeeded must report no failure")
	})
}

// Every site that fails while preparing the plugin Create request records its
// reason through one mapping, so the two categories must be right at the
// mapping. Create is worded for creating a resource: its texts must not be the
// update ones, whose remedy of leaving the provider's current value in place
// does not exist on a create.
func TestCreateRequestFailureReason_Categories(t *testing.T) {
	wrapped := fmt.Errorf("converting properties: %w", fmt.Errorf("resolving references: %w", resolver.ErrHashedValueNotWritable))

	assert.Equal(t, failureReasonUnrecoverableOpaqueValueOnCreate, createRequestFailureReason(wrapped),
		"an unrecoverable stored opaque value must be reported as such however deeply it is wrapped")
	assert.Equal(t, failureReasonPluginRequestPreparationOnCreate, createRequestFailureReason(errors.New("malformed document")),
		"any other preparation failure falls back to the generic reason")

	assert.NotEqual(t, failureReasonUnrecoverableOpaqueValueOnUpdate, failureReasonUnrecoverableOpaqueValueOnCreate,
		"create must not report the update wording")
	assert.NotEqual(t, failureReasonPluginRequestPreparationOnUpdate, failureReasonPluginRequestPreparationOnCreate,
		"create must not report the update wording")
}

// A property bound to a generator whose value has not been drawn is its own
// category: the remedy is to declare the value rather than to re-supply a
// secret formae once held, so it must not fall back to the generic
// preparation reason or borrow the stored-hash wording.
func TestRequestFailureReason_UndrawnGeneratorValueIsItsOwnCategory(t *testing.T) {
	wrapped := fmt.Errorf("converting properties: %w", resolver.ErrUnresolvedGeneratorReferenceNotWritable)

	assert.Equal(t, failureReasonUndrawnGeneratorValueOnCreate, createRequestFailureReason(wrapped),
		"an undrawn generator value must be reported as such however deeply it is wrapped")
	assert.Equal(t, failureReasonUndrawnGeneratorValueOnUpdate, updateRequestFailureReason(wrapped))

	assert.NotEqual(t, failureReasonUndrawnGeneratorValueOnCreate, failureReasonUnrecoverableOpaqueValueOnCreate,
		"an undrawn value is not an unrecoverable stored hash")
	assert.NotEqual(t, failureReasonUndrawnGeneratorValueOnCreate, failureReasonUndrawnGeneratorValueOnUpdate,
		"create must not report the update wording")
}
