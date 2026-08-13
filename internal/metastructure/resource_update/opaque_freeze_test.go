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

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// errPluginUnreachable short-circuits the state function once the operation has
// been built, so these tests observe exactly the plugin request update()
// assembled without driving progress handling.
var errPluginUnreachable = errors.New("plugin operator unreachable")

// operationCapturingProcess drives update() far enough to observe the
// plugin.UpdateResource it builds: Call answers the coordinator's spawn request,
// CallWithTimeout records the operation and then fails, ending the state
// function immediately after the request is assembled.
type operationCapturingProcess struct {
	*stubUpdaterProcess
	log       *capturingLog
	operation plugin.PluginOperation
}

func (p *operationCapturingProcess) Log() gen.Log { return p.log }

func (p *operationCapturingProcess) Call(_ any, _ any) (any, error) {
	return messages.SpawnPluginOperatorResult{PID: gen.PID{Node: "test-node", ID: 2}}, nil
}

func (p *operationCapturingProcess) CallWithTimeout(_ any, message any, _ int) (any, error) {
	p.operation = message.(plugin.PluginOperation)
	return nil, errPluginUnreachable
}

func newOperationCapturingProcess() *operationCapturingProcess {
	return &operationCapturingProcess{stubUpdaterProcess: &stubUpdaterProcess{}, log: &capturingLog{}}
}

// capturedUpdate returns the plugin.UpdateResource update() assembled, failing
// the test when the state function never reached the plugin call.
func (p *operationCapturingProcess) capturedUpdate(t *testing.T) plugin.UpdateResource {
	t.Helper()
	require.NotNil(t, p.operation, "update() never reached the plugin call")
	op, ok := p.operation.(plugin.UpdateResource)
	require.True(t, ok, "expected an UpdateResource operation, got %T", p.operation)
	return op
}

// secretSchema declares one schema-opaque secret alongside plain metadata, the
// shape of a provider secret resource.
func secretSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "SecretString"},
		Hints:      map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true, WriteOnly: true}},
	}
}

// hashedLeaf renders the stored form of an opaque value: the digest, marked
// hashed, exactly as setOnce substitutes it into the desired properties.
func hashedLeaf(digest string) string {
	return `{"$strategy":"SetOnce","$visibility":"Opaque","$value":"` + digest + `","$hashed":true}`
}

// updateForFrozenSecret builds the ResourceUpdateData for a resource whose
// opaque value is frozen at its stored hash on both sides, with an unrelated
// metadata change carried in the patch.
func updateForFrozenSecret(digest string) ResourceUpdateData {
	schema := secretSchema()
	stored := `{"Name":"n","Description":"old","SecretString":` + hashedLeaf(digest) + `}`
	desired := `{"Name":"n","Description":"new","SecretString":` + hashedLeaf(digest) + `}`

	ru := &ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label: "identity-key", Type: "AWS::SecretsManager::Secret", Stack: "default",
			Schema: schema, Properties: json.RawMessage(stored),
		},
		DesiredState: pkgmodel.Resource{
			Label: "identity-key", Type: "AWS::SecretsManager::Secret", Stack: "default",
			Schema: schema, Properties: json.RawMessage(desired),
			PatchDocument: json.RawMessage(`[{"op":"replace","path":"/Description","value":"new"}]`),
		},
		ResourceTarget: pkgmodel.Target{Label: "us-east-1", Namespace: "aws", Config: json.RawMessage(`{}`)},
	}
	return ResourceUpdateData{resourceUpdate: ru, commandID: "cmd-1", originalResourceKsuidURI: ru.DesiredState.URI()}
}

// A setOnce-frozen opaque value must not block an unrelated metadata update.
// setOnce substitutes the STORED value — a $hashed envelope — into the desired
// properties, and the guarded converter that builds DesiredProperties rightly
// refuses to send a digest as if it were the secret, so before the fix every
// update to any other property on the resource failed permanently.
func TestUpdate_SetOnceFrozenSecret_SiblingEdit_ReachesPlugin(t *testing.T) {
	digest := pkgmodel.ComputeValueHash("the-real-secret")
	data := updateForFrozenSecret(digest)
	proc := newOperationCapturingProcess()

	_, _, _, err := update(StateUpdating, data, proc)
	require.NoError(t, err)

	op := proc.capturedUpdate(t)
	assert.JSONEq(t, `[{"op":"replace","path":"/Description","value":"new"}]`, op.PatchDocument,
		"the patch must carry the metadata change and nothing else")

	desired := map[string]any{}
	require.NoError(t, json.Unmarshal(op.DesiredProperties, &desired))
	assert.Equal(t, map[string]any{"$opaque": "preserved"}, desired["SecretString"],
		"an unrecoverable stored value must reach the plugin as a present-but-unusable sentinel")
	assert.Equal(t, "new", desired["Description"], "the metadata change must reach the plugin")
	assert.NotContains(t, string(op.DesiredProperties), digest, "the stored digest must never leave the agent")

	assert.Contains(t, string(data.resourceUpdate.DesiredState.Properties), digest,
		"the durable desired state must keep the stored hash")
}

// The sentinel must survive the complete conversion — reference resolution,
// plugin-format unwrapping AND nested-empty-collection stripping — wherever the
// frozen leaf sits, and must not disturb its neighbours.
func TestUpdate_SetOnceFrozenSecret_NestedUnderMapAndArray(t *testing.T) {
	digest := pkgmodel.ComputeValueHash("nested-secret")
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Settings", "Entries"},
		Hints:  map[string]pkgmodel.FieldHint{"Settings.Password": {Opaque: true}, "Entries.Token": {Opaque: true}},
	}
	props := func(description string) string {
		return `{"Name":"n","Description":"` + description + `",` +
			`"Settings":{"Password":` + hashedLeaf(digest) + `,"Empty":{}},` +
			`"Entries":[{"Token":` + hashedLeaf(digest) + `,"Tags":[]}]}`
	}

	ru := &ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label: "r", Type: "AWS::SecretsManager::Secret", Stack: "default",
			Schema: schema, Properties: json.RawMessage(props("old")),
		},
		DesiredState: pkgmodel.Resource{
			Label: "r", Type: "AWS::SecretsManager::Secret", Stack: "default",
			Schema: schema, Properties: json.RawMessage(props("new")),
			PatchDocument: json.RawMessage(`[{"op":"replace","path":"/Description","value":"new"}]`),
		},
		ResourceTarget: pkgmodel.Target{Label: "us-east-1", Namespace: "aws", Config: json.RawMessage(`{}`)},
	}
	data := ResourceUpdateData{resourceUpdate: ru, commandID: "cmd-1", originalResourceKsuidURI: ru.DesiredState.URI()}
	proc := newOperationCapturingProcess()

	_, _, _, err := update(StateUpdating, data, proc)
	require.NoError(t, err)

	op := proc.capturedUpdate(t)
	var desired struct {
		Settings map[string]any   `json:"Settings"`
		Entries  []map[string]any `json:"Entries"`
	}
	require.NoError(t, json.Unmarshal(op.DesiredProperties, &desired))
	assert.Equal(t, map[string]any{"$opaque": "preserved"}, desired.Settings["Password"])
	require.Len(t, desired.Entries, 1)
	assert.Equal(t, map[string]any{"$opaque": "preserved"}, desired.Entries[0]["Token"])
	assert.NotContains(t, string(op.DesiredProperties), digest)
}

// FreezeUnrecoverableOpaqueValues acts only on values formae structurally cannot
// send. A genuine rotation supplies live plaintext, which must reach the plugin
// untouched so the provider actually writes the new secret.
func TestFreezeUnrecoverableOpaqueValues_RotationUntouched(t *testing.T) {
	schema := secretSchema()
	prior := json.RawMessage(`{"SecretString":` + hashedLeaf(pkgmodel.ComputeValueHash("old")) + `}`)
	desired := json.RawMessage(`{"SecretString":{"$visibility":"Opaque","$value":"brand-new"}}`)

	out, err := FreezeUnrecoverableOpaqueValues(prior, desired, schema, schema, "AWS::SecretsManager::Secret")
	require.NoError(t, err)
	assert.JSONEq(t, string(desired), string(out), "a live plaintext rotation must not be frozen")
}

// The case that separates this helper from SuppressUnchangedOpaqueValues: an
// UNCHANGED opaque value whose desired side is still live plaintext is
// recoverable, so it must be left exactly as it is.
func TestFreezeUnrecoverableOpaqueValues_UnchangedPlaintextUntouched(t *testing.T) {
	schema := secretSchema()
	prior := json.RawMessage(`{"SecretString":` + hashedLeaf(pkgmodel.ComputeValueHash("same")) + `}`)
	desired := json.RawMessage(`{"SecretString":{"$visibility":"Opaque","$value":"same"}}`)

	out, err := FreezeUnrecoverableOpaqueValues(prior, desired, schema, schema, "AWS::SecretsManager::Secret")
	require.NoError(t, err)
	assert.JSONEq(t, string(desired), string(out))
}

// A hashed value that does NOT match prior state means something upstream is
// broken. It must not be papered over: it stays put and still fails the guard.
func TestFreezeUnrecoverableOpaqueValues_HashedButNotEqualToPriorUntouched(t *testing.T) {
	schema := secretSchema()
	prior := json.RawMessage(`{"SecretString":` + hashedLeaf(pkgmodel.ComputeValueHash("a")) + `}`)
	desired := json.RawMessage(`{"SecretString":` + hashedLeaf(pkgmodel.ComputeValueHash("b")) + `}`)

	out, err := FreezeUnrecoverableOpaqueValues(prior, desired, schema, schema, "AWS::SecretsManager::Secret")
	require.NoError(t, err)
	assert.JSONEq(t, string(desired), string(out))
}

// Anything that does not match the exact envelope shape falls through to the
// guard rather than being guessed at.
func TestFreezeUnrecoverableOpaqueValues_MalformedEnvelopesUntouched(t *testing.T) {
	schema := secretSchema()
	digest := pkgmodel.ComputeValueHash("s")
	cases := map[string]struct{ prior, desired string }{
		"hashed marker is not a boolean": {
			prior:   `{"SecretString":` + hashedLeaf(digest) + `}`,
			desired: `{"SecretString":{"$visibility":"Opaque","$value":"` + digest + `","$hashed":"true"}}`,
		},
		"desired value is absent": {
			prior:   `{"SecretString":` + hashedLeaf(digest) + `}`,
			desired: `{"SecretString":{"$visibility":"Opaque","$hashed":true}}`,
		},
		"desired value is not a string": {
			prior:   `{"SecretString":` + hashedLeaf(digest) + `}`,
			desired: `{"SecretString":{"$visibility":"Opaque","$value":42,"$hashed":true}}`,
		},
		"prior is not an object": {
			prior:   `{"SecretString":"` + digest + `"}`,
			desired: `{"SecretString":` + hashedLeaf(digest) + `}`,
		},
		"prior is absent": {
			prior:   `{"Name":"n"}`,
			desired: `{"SecretString":` + hashedLeaf(digest) + `}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := FreezeUnrecoverableOpaqueValues(
				json.RawMessage(tc.prior), json.RawMessage(tc.desired), schema, schema, "AWS::SecretsManager::Secret")
			require.NoError(t, err)
			assert.JSONEq(t, tc.desired, string(out))
		})
	}
}

// $strategy is deliberately ignored when matching: the persist transformer
// canonicalises a missing strategy on an already-hashed envelope, so requiring
// it to match would reintroduce the permanent failure.
func TestFreezeUnrecoverableOpaqueValues_IgnoresStrategyMismatch(t *testing.T) {
	schema := secretSchema()
	digest := pkgmodel.ComputeValueHash("s")
	prior := json.RawMessage(`{"SecretString":{"$visibility":"Opaque","$value":"` + digest + `","$hashed":true}}`)
	desired := json.RawMessage(`{"SecretString":` + hashedLeaf(digest) + `}`)

	out, err := FreezeUnrecoverableOpaqueValues(prior, desired, schema, schema, "AWS::SecretsManager::Secret")
	require.NoError(t, err)
	assert.JSONEq(t, `{"SecretString":{"$opaque":"preserved"}}`, string(out))
}

// Opacity is resolved from the union of both schemas and both documents, so a
// hint dropped from the desired schema, or an inline marker carried only on the
// prior side, still classifies.
func TestFreezeUnrecoverableOpaqueValues_ClassificationSources(t *testing.T) {
	digest := pkgmodel.ComputeValueHash("s")
	frozen := hashedLeaf(digest)
	bare := pkgmodel.Schema{Fields: []string{"Secret"}}
	hinted := pkgmodel.Schema{Fields: []string{"Secret"}, Hints: map[string]pkgmodel.FieldHint{"Secret": {Opaque: true}}}

	t.Run("hint only on the prior schema", func(t *testing.T) {
		props := `{"Secret":{"$value":"` + digest + `","$hashed":true}}`
		out, err := FreezeUnrecoverableOpaqueValues(json.RawMessage(props), json.RawMessage(props), hinted, bare, "T")
		require.NoError(t, err)
		assert.JSONEq(t, `{"Secret":{"$opaque":"preserved"}}`, string(out))
	})

	t.Run("inline opacity only on the prior side", func(t *testing.T) {
		prior := `{"Secret":{"$visibility":"Opaque","$value":"` + digest + `","$hashed":true}}`
		desired := `{"Secret":{"$value":"` + digest + `","$hashed":true}}`
		out, err := FreezeUnrecoverableOpaqueValues(json.RawMessage(prior), json.RawMessage(desired), bare, bare, "T")
		require.NoError(t, err)
		assert.JSONEq(t, `{"Secret":{"$opaque":"preserved"}}`, string(out))
	})

	t.Run("nested hint name", func(t *testing.T) {
		schema := pkgmodel.Schema{Fields: []string{"Settings"}, Hints: map[string]pkgmodel.FieldHint{"Settings.Password": {Opaque: true}}}
		props := `{"Settings":{"Password":` + frozen + `,"Host":"db"}}`
		out, err := FreezeUnrecoverableOpaqueValues(json.RawMessage(props), json.RawMessage(props), schema, schema, "T")
		require.NoError(t, err)
		assert.JSONEq(t, `{"Settings":{"Password":{"$opaque":"preserved"},"Host":"db"}}`, string(out))
	})

	t.Run("hint inside an array of sub-resources", func(t *testing.T) {
		schema := pkgmodel.Schema{Fields: []string{"Users"}, Hints: map[string]pkgmodel.FieldHint{"Users.Password": {Opaque: true}}}
		props := `{"Users":[{"Name":"a","Password":` + frozen + `},{"Name":"b","Password":` + frozen + `}]}`
		out, err := FreezeUnrecoverableOpaqueValues(json.RawMessage(props), json.RawMessage(props), schema, schema, "T")
		require.NoError(t, err)
		assert.JSONEq(t,
			`{"Users":[{"Name":"a","Password":{"$opaque":"preserved"}},{"Name":"b","Password":{"$opaque":"preserved"}}]}`,
			string(out))
	})
}

// Path handling must address the leaf that actually matched, not a neighbour a
// path syntax happens to resolve to.
func TestFreezeUnrecoverableOpaqueValues_HostileKeys(t *testing.T) {
	digest := pkgmodel.ComputeValueHash("s")
	frozen := hashedLeaf(digest)

	t.Run("key containing a literal dot", func(t *testing.T) {
		schema := pkgmodel.Schema{Fields: []string{"a.b"}, Hints: map[string]pkgmodel.FieldHint{"a.b": {Opaque: true}}}
		props := `{"a.b":` + frozen + `,"a":{"b":"not-a-secret"}}`
		out, err := FreezeUnrecoverableOpaqueValues(json.RawMessage(props), json.RawMessage(props), schema, schema, "T")
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(out, &got))
		assert.Equal(t, map[string]any{"$opaque": "preserved"}, got["a.b"], "the literal dotted key must be frozen")
	})

	t.Run("object key 0 is not an array index", func(t *testing.T) {
		schema := pkgmodel.Schema{Fields: []string{"Slots"}, Hints: map[string]pkgmodel.FieldHint{"Slots.Secret": {Opaque: true}}}
		props := `{"Slots":{"0":{"Secret":` + frozen + `},"1":{"Secret":"plain"}}}`
		out, err := FreezeUnrecoverableOpaqueValues(json.RawMessage(props), json.RawMessage(props), schema, schema, "T")
		require.NoError(t, err)
		assert.JSONEq(t,
			`{"Slots":{"0":{"Secret":{"$opaque":"preserved"}},"1":{"Secret":"plain"}}}`, string(out))
	})
}

// Non-opaque properties are never touched, including one whose legitimate value
// is digest-shaped — otherwise a leak assertion could pass by accident.
func TestFreezeUnrecoverableOpaqueValues_NonOpaqueSiblingsUntouched(t *testing.T) {
	schema := secretSchema()
	digest := pkgmodel.ComputeValueHash("s")
	props := `{"Name":"n","Checksum":{"$value":"` + digest + `","$hashed":true},"SecretString":` + hashedLeaf(digest) + `}`

	out, err := FreezeUnrecoverableOpaqueValues(
		json.RawMessage(props), json.RawMessage(props), schema, schema, "AWS::SecretsManager::Secret")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, map[string]any{"$opaque": "preserved"}, got["SecretString"])
	assert.Equal(t, map[string]any{"$value": digest, "$hashed": true}, got["Checksum"],
		"a non-opaque property must be left alone even when its value is digest-shaped")
}

// An update that fails while preparing the plugin request never records plugin
// progress, so without an explicit reason the operator sees an empty
// ErrorMessage. Every one of those sites must record one, and the recorded text
// must not carry the secret, its digest, or a property path.
func TestUpdate_PrePluginFailure_RecordsRedactedFailureReason(t *testing.T) {
	const plaintext = "the-real-secret"
	digest := pkgmodel.ComputeValueHash(plaintext)

	// Each case reaches exactly one site: the logged line identifies which, so
	// a fixture that failed earlier than intended cannot pass for its site.
	cases := map[string]struct {
		build func() ResourceUpdateData
		logs  string
	}{
		// A desired hash that does not match prior state is exactly the input
		// the freeze deliberately declines to rewrite, so it still fails the
		// guard in the desired conversion.
		"desired conversion": {
			build: func() ResourceUpdateData {
				data := updateForFrozenSecret(digest)
				data.resourceUpdate.PriorState.Properties = json.RawMessage(
					`{"Name":"n","Description":"old","SecretString":` + hashedLeaf(pkgmodel.ComputeValueHash("other")) + `}`)
				return data
			},
			logs: "failed to convert resource properties for plugin",
		},
		// A recoverable desired value converts cleanly, so an undecodable prior
		// document fails in the prior conversion.
		"prior conversion": {
			build: func() ResourceUpdateData {
				data := recoverableUpdate(plaintext)
				data.resourceUpdate.PriorState.Properties = json.RawMessage(`{"Broken":{"$ref":`)
				return data
			},
			logs: "failed to convert existing resource properties for plugin",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			data := tc.build()
			proc := newOperationCapturingProcess()
			state, _, _, err := update(StateUpdating, data, proc)
			require.NoError(t, err)
			require.Equal(t, StateFinishedWithError, state, "preparing the plugin request must fail")
			require.Contains(t, strings.Join(proc.log.all(), "\n"), tc.logs, "the intended failure site must be the one that fired")

			message := data.resourceUpdate.MostRecentFailureMessage()
			require.NotEmpty(t, message, "a pre-plugin failure must still surface a reason")
			assert.NotContains(t, message, plaintext, "the reason must not carry the secret")
			assert.NotContains(t, message, digest, "the reason must not carry the stored digest")
			assert.NotContains(t, message, "SecretString", "the reason must not carry a property path")
			assert.NotRegexp(t, anySHA256, message)
			assert.False(t, strings.Contains(message, "/"), "the reason must not carry a property path")
		})
	}
}

// Every site that fails while preparing the plugin Update request records its
// reason through one mapping, so the two categories must be right at the
// mapping. This is also the only coverage available for the prior-opaque-strip
// site: its error branch is defensive, because an input it could not decode into
// an object already fails the conversion immediately before it.
func TestUpdateRequestFailureReason_Categories(t *testing.T) {
	wrapped := fmt.Errorf("converting properties: %w", resolver.ErrHashedValueNotWritable)

	assert.Equal(t, failureReasonUnrecoverableOpaqueValue, updateRequestFailureReason(wrapped),
		"an unrecoverable stored opaque value must be reported as such however deeply it is wrapped")
	assert.Equal(t, failureReasonPluginRequestPreparation, updateRequestFailureReason(errors.New("malformed document")),
		"any other preparation failure falls back to the generic reason")
}

// recoverableUpdate builds an update whose opaque value is live plaintext, so
// the desired conversion succeeds and a fault planted in prior state surfaces at
// a later preparation step.
func recoverableUpdate(plaintext string) ResourceUpdateData {
	data := updateForFrozenSecret(pkgmodel.ComputeValueHash(plaintext))
	data.resourceUpdate.DesiredState.Properties = json.RawMessage(
		`{"Name":"n","Description":"new","SecretString":{"$visibility":"Opaque","$value":"` + plaintext + `"}}`)
	return data
}

// A recorded reason must not outlive the attempt that produced it: a retried or
// resumed update that succeeds must not surface a stale failure.
func TestUpdate_FailureReasonDoesNotSurviveARetry(t *testing.T) {
	data := updateForFrozenSecret(pkgmodel.ComputeValueHash("s"))
	data.resourceUpdate.FailureReason = "a reason recorded by an earlier attempt"

	_, _, _, err := update(StateUpdating, data, newOperationCapturingProcess())
	require.NoError(t, err)
	assert.Empty(t, data.resourceUpdate.FailureReason, "a new attempt must clear the previous reason")

	data.resourceUpdate.FailureReason = "another stale reason"
	data.resourceUpdate.MarkAsSuccess()
	assert.Empty(t, data.resourceUpdate.MostRecentFailureMessage(), "a successful update must not report a failure")
}
