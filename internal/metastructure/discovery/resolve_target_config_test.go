// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package discovery

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// resolveStubProcess is a gen.Process test double for resolveTargetConfigForList tests.
// It records LoadResource calls and configures plugin Read outcomes.
type resolveStubProcess struct {
	gen.Process

	loadResourceCalls []pkgmodel.FormaeURI
	loadResult        messages.LoadResourceResult
	loadErr           error

	// readAttempts counts how many times the plugin read is attempted
	readAttempts int
	// readResponses is an ordered list of responses to return per attempt;
	// the last one is repeated if exhausted.
	readResponses []readResponse
}

type readResponse struct {
	progress *plugin.TrackedProgress
	err      error
}

func (p *resolveStubProcess) Log() gen.Log            { return stubLog{} }
func (p *resolveStubProcess) Node() gen.Node          { return stubNode{} }
func (p *resolveStubProcess) PID() gen.PID            { return gen.PID{Node: "test-node", ID: 2} }
func (p *resolveStubProcess) Send(_ any, _ any) error { return nil }

func (p *resolveStubProcess) Call(_ any, message any) (any, error) {
	switch m := message.(type) {
	case messages.LoadResource:
		p.loadResourceCalls = append(p.loadResourceCalls, m.ResourceURI)
		if p.loadErr != nil {
			return nil, p.loadErr
		}
		return p.loadResult, nil
	case messages.SpawnPluginOperator:
		p.readAttempts++
		idx := p.readAttempts - 1
		if idx >= len(p.readResponses) {
			idx = len(p.readResponses) - 1
		}
		resp := p.readResponses[idx]
		if resp.err != nil {
			return nil, resp.err
		}
		return messages.SpawnPluginOperatorResult{
			PID: gen.PID{Node: "test-node", ID: 99},
		}, nil
	default:
		return nil, fmt.Errorf("resolveStubProcess: unexpected Call message %T", message)
	}
}

func (p *resolveStubProcess) CallWithTimeout(_ any, message any, _ int) (any, error) {
	// This is called for the actual plugin.ReadResource after spawn
	if _, ok := message.(plugin.ReadResource); ok {
		idx := p.readAttempts - 1
		if idx >= len(p.readResponses) {
			idx = len(p.readResponses) - 1
		}
		resp := p.readResponses[idx]
		if resp.err != nil {
			return nil, resp.err
		}
		return *resp.progress, nil
	}
	return nil, fmt.Errorf("resolveStubProcess: unexpected CallWithTimeout message %T", message)
}

// buildOpaqueTargetConfig creates a target config with one opaque $ref pointing
// to the given KSUID and property path.
func buildOpaqueTargetConfig(ksuid, prop string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
		"Region": "us-east-1",
		"ApiKey": {
			"$ref": "formae://%s#/%s",
			"$value": "old-value",
			"$visibility": "Opaque"
		}
	}`, ksuid, prop))
}

// buildSourceResource creates a minimal Resource that the stub LoadResource returns.
func buildSourceResource(ksuid string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Ksuid:    ksuid,
		Label:    "my-secret",
		Type:     "FakeAWS::SecretsManager::Secret",
		Stack:    "default",
		Target:   "us-east-1",
		NativeID: "arn:aws:secretsmanager:us-east-1:123456789012:secret:my-secret",
	}
}

// TestResolveTargetConfigForList_OpaqueRefResolved asserts that when a target
// config carries one opaque $ref, the returned config has the $ref replaced
// by the resolved plaintext and contains no "$ref" substring.
func TestResolveTargetConfigForList_OpaqueRefResolved(t *testing.T) {
	const ksuid = "35R2vyf6mT5wEs0mTWT5bp1Lf0E"
	const prop = "SecretString"
	const plaintext = "s3cr3t"

	srcResource := buildSourceResource(ksuid)
	srcTarget := pkgmodel.Target{
		Label:     "us-east-1",
		Namespace: "FakeAWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
	}

	proc := &resolveStubProcess{
		loadResult: messages.LoadResourceResult{
			Resource: srcResource,
			Target:   srcTarget,
		},
		readResponses: []readResponse{
			{
				progress: &plugin.TrackedProgress{
					ProgressResult: resource.ProgressResult{
						OperationStatus:    resource.OperationStatusSuccess,
						ResourceProperties: json.RawMessage(fmt.Sprintf(`{"%s":"%s"}`, prop, plaintext)),
					},
				},
			},
		},
	}

	targetCfg := buildOpaqueTargetConfig(ksuid, prop)
	target := pkgmodel.Target{
		Label:  "prod",
		Config: targetCfg,
	}

	result, err := resolveTargetConfigForList(proc, target)

	require.NoError(t, err)
	assert.NotContains(t, string(result), `"$ref"`,
		"resolved config must not contain any $ref markers")
	assert.Contains(t, string(result), plaintext,
		"resolved config must contain the plaintext value")

	// Exactly one LoadResource call was issued (for the opaque URI)
	require.Len(t, proc.loadResourceCalls, 1)
	sourceURI := pkgmodel.FormaeURI(fmt.Sprintf("formae://%s#/%s", ksuid, prop))
	assert.Equal(t, sourceURI.Stripped(),
		proc.loadResourceCalls[0],
		"LoadResource must be called with the stripped URI (no property path)")
}

// TestResolveTargetConfigForList_NoOpaqueRefs asserts that a target config
// with no opaque refs is returned byte-identical and no LoadResource or Read
// is issued.
func TestResolveTargetConfigForList_NoOpaqueRefs(t *testing.T) {
	proc := &resolveStubProcess{}

	cfg := json.RawMessage(`{"Region":"us-east-1","AccountId":"123456789012"}`)
	target := pkgmodel.Target{
		Label:  "prod",
		Config: cfg,
	}

	result, err := resolveTargetConfigForList(proc, target)

	require.NoError(t, err)
	assert.Equal(t, string(cfg), string(result),
		"config without opaque refs must be returned byte-identical")
	assert.Empty(t, proc.loadResourceCalls,
		"no LoadResource must be issued when there are no opaque refs")
	assert.Zero(t, proc.readAttempts,
		"no Read must be issued when there are no opaque refs")
}

// TestResolveTargetConfigForList_NonRecoverableReadFailure asserts that a
// non-recoverable plugin Read failure returns an error that names the target
// and reference but does NOT contain the plaintext or any raw $value.
func TestResolveTargetConfigForList_NonRecoverableReadFailure(t *testing.T) {
	const ksuid = "35R2vyf6mT5wEs0mTWT5bp1Lf0E"
	const prop = "SecretString"
	const plaintext = "top-secret-value"

	srcResource := buildSourceResource(ksuid)
	srcTarget := pkgmodel.Target{
		Label:     "us-east-1",
		Namespace: "FakeAWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
	}

	proc := &resolveStubProcess{
		loadResult: messages.LoadResourceResult{
			Resource: srcResource,
			Target:   srcTarget,
		},
		readResponses: []readResponse{
			{
				progress: &plugin.TrackedProgress{
					ProgressResult: resource.ProgressResult{
						OperationStatus: resource.OperationStatusFailure,
						// A non-recoverable error code
						ErrorCode: resource.OperationErrorCodeNotFound,
					},
				},
			},
		},
	}

	targetCfg := buildOpaqueTargetConfig(ksuid, prop)
	// Embed plaintext into the config $value so we can check it's not leaked
	targetCfg = json.RawMessage(strings.ReplaceAll(string(targetCfg), "old-value", plaintext))
	target := pkgmodel.Target{
		Label:  "prod",
		Config: targetCfg,
	}

	result, err := resolveTargetConfigForList(proc, target)

	require.Error(t, err, "non-recoverable read failure must return an error")
	assert.Nil(t, result, "result must be nil on failure")

	// Error must name the target or reference for operator action
	errMsg := err.Error()
	assert.True(t,
		strings.Contains(errMsg, "prod") || strings.Contains(errMsg, ksuid),
		"error must name the target label or reference URI, got: %s", errMsg)

	// Error must NOT contain the plaintext or the raw $value
	assert.NotContains(t, errMsg, plaintext,
		"error must not leak the plaintext secret value")
	assert.NotContains(t, errMsg, "$value",
		"error must not include raw $ref envelope fields")
}

// TestResolveTargetConfigForList_RecoverableFailureThenSuccess asserts that a
// recoverable failure followed by success within the attempt budget resolves
// successfully, and that exceeding the budget returns an error.
func TestResolveTargetConfigForList_RecoverableFailureThenSuccess(t *testing.T) {
	const ksuid = "35R2vyf6mT5wEs0mTWT5bp1Lf0E"
	const prop = "SecretString"
	const plaintext = "s3cr3t"

	srcResource := buildSourceResource(ksuid)
	srcTarget := pkgmodel.Target{
		Label:     "us-east-1",
		Namespace: "FakeAWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
	}

	targetCfg := buildOpaqueTargetConfig(ksuid, prop)
	target := pkgmodel.Target{
		Label:  "prod",
		Config: targetCfg,
	}

	t.Run("recoverable then success", func(t *testing.T) {
		proc := &resolveStubProcess{
			loadResult: messages.LoadResourceResult{
				Resource: srcResource,
				Target:   srcTarget,
			},
			readResponses: []readResponse{
				{
					progress: &plugin.TrackedProgress{
						ProgressResult: resource.ProgressResult{
							OperationStatus: resource.OperationStatusFailure,
							ErrorCode:       resource.OperationErrorCodeServiceTimeout, // recoverable
						},
					},
				},
				{
					progress: &plugin.TrackedProgress{
						ProgressResult: resource.ProgressResult{
							OperationStatus:    resource.OperationStatusSuccess,
							ResourceProperties: json.RawMessage(fmt.Sprintf(`{"%s":"%s"}`, prop, plaintext)),
						},
					},
				},
			},
		}

		result, err := resolveTargetConfigForList(proc, target)

		require.NoError(t, err, "retry after recoverable failure must succeed")
		assert.Contains(t, string(result), plaintext,
			"resolved config must contain the plaintext after retry")
		assert.GreaterOrEqual(t, proc.readAttempts, 2,
			"at least two read attempts must be made: one failure, one success")
	})

	t.Run("exhausts budget", func(t *testing.T) {
		// Build a process where every attempt is a recoverable failure
		recoverableResp := readResponse{
			progress: &plugin.TrackedProgress{
				ProgressResult: resource.ProgressResult{
					OperationStatus: resource.OperationStatusFailure,
					ErrorCode:       resource.OperationErrorCodeServiceTimeout,
				},
			},
		}
		responses := make([]readResponse, maxDiscoveryResolveAttempts+1)
		for i := range responses {
			responses[i] = recoverableResp
		}
		proc := &resolveStubProcess{
			loadResult: messages.LoadResourceResult{
				Resource: srcResource,
				Target:   srcTarget,
			},
			readResponses: responses,
		}

		result, err := resolveTargetConfigForList(proc, target)

		require.Error(t, err, "exhausting the retry budget must return an error")
		assert.Nil(t, result, "result must be nil when budget is exhausted")
		assert.Equal(t, maxDiscoveryResolveAttempts, proc.readAttempts,
			"must exhaust the full attempt budget on repeated recoverable failures")
	})
}

// TestResolveTargetConfigForList_ConvertFailureDoesNotLeakEnvelope asserts that
// when the final ConvertToPluginFormat step fails (e.g. a $hashed field remains
// in the working config after all $ref URIs have been resolved), the function
// returns an error and a nil result — never the still-wrapped envelope
// containing $value.
//
// The trigger: the target config carries both an opaque $ref (which is
// successfully resolved) AND a separately hashed field that has no $ref so it
// is not in the opaque-URI list. ConvertToPluginFormat rejects the $hashed
// marker, exercising the error surface introduced by this fix.
func TestResolveTargetConfigForList_ConvertFailureDoesNotLeakEnvelope(t *testing.T) {
	const ksuid = "35R2vyf6mT5wEs0mTWT5bp1Lf0E"
	const prop = "SecretString"
	const plaintext = "s3cr3t"
	// hashedPlaintext is the value stored hashed in the target config; it must
	// not appear in any error message.
	const hashedPlaintext = "hashed-secret-value"

	srcResource := buildSourceResource(ksuid)
	srcTarget := pkgmodel.Target{
		Label:     "us-east-1",
		Namespace: "FakeAWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
	}

	proc := &resolveStubProcess{
		loadResult: messages.LoadResourceResult{
			Resource: srcResource,
			Target:   srcTarget,
		},
		readResponses: []readResponse{
			{
				progress: &plugin.TrackedProgress{
					ProgressResult: resource.ProgressResult{
						OperationStatus:    resource.OperationStatusSuccess,
						ResourceProperties: json.RawMessage(fmt.Sprintf(`{"%s":"%s"}`, prop, plaintext)),
					},
				},
			},
		},
	}

	// Target config has one resolvable $ref (SecretString) plus a hashed field
	// (StoredHash) that carries no $ref — ExtractOpaqueResolvableURIsFromJSON
	// will not include it in the URI list, so it survives to the final
	// ConvertToPluginFormat call, which rejects the $hashed marker.
	targetCfg := json.RawMessage(fmt.Sprintf(`{
		"Region": "us-east-1",
		"ApiKey": {
			"$ref": "formae://%s#/%s",
			"$value": "old-value",
			"$visibility": "Opaque"
		},
		"StoredHash": {
			"$value": %q,
			"$visibility": "Opaque",
			"$hashed": true
		}
	}`, ksuid, prop, hashedPlaintext))

	target := pkgmodel.Target{
		Label:  "prod",
		Config: targetCfg,
	}

	result, err := resolveTargetConfigForList(proc, target)

	require.Error(t, err, "a ConvertToPluginFormat failure must surface as an error")
	assert.Nil(t, result, "result must be nil — envelope config must not be returned")

	errMsg := err.Error()
	assert.NotContains(t, errMsg, plaintext,
		"error must not contain the plaintext resolved value")
	assert.NotContains(t, errMsg, hashedPlaintext,
		"error must not contain the hashed field's stored value")
	assert.NotContains(t, errMsg, "$value",
		"error must not include raw $ref envelope fields")
	assert.Contains(t, errMsg, "prod",
		"error must name the target label for operator action")
}

// Ensure resolveStubProcess satisfies gen.Process.
var _ gen.Process = (*resolveStubProcess)(nil)

// stubActorNamesSentinel verifies the expected actor name constant is accessible.
var _ = actornames.ResourcePersister
