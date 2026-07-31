// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package discovery

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
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

// recordingLog is a gen.Log test double that records every formatted message
// produced by any log method. Tests use it to assert that no log line on the
// resolve-failure path contains a plaintext secret or a raw "$value" envelope.
type recordingLog struct {
	gen.Log
	mu       sync.Mutex
	messages []string
}

func (r *recordingLog) record(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

func (r *recordingLog) Trace(format string, args ...any)   { r.record(format, args...) }
func (r *recordingLog) Debug(format string, args ...any)   { r.record(format, args...) }
func (r *recordingLog) Info(format string, args ...any)    { r.record(format, args...) }
func (r *recordingLog) Warning(format string, args ...any) { r.record(format, args...) }
func (r *recordingLog) Error(format string, args ...any)   { r.record(format, args...) }
func (r *recordingLog) Panic(format string, args ...any)   { r.record(format, args...) }

func (r *recordingLog) allMessages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.messages))
	copy(out, r.messages)
	return out
}

// recordingProcess wraps resolveStubProcess but routes Log() to a recordingLog
// so tests can assert on the content of log lines produced during resolution.
type recordingProcess struct {
	*resolveStubProcess
	log *recordingLog
}

func (p *recordingProcess) Log() gen.Log { return p.log }

// buildDiscoveryDataWithTarget returns a DiscoveryData ready for
// ensureTargetResolved tests: resolvedTargets/failedTargets are initialized and
// the given target is registered.
func buildDiscoveryDataWithTarget(label string, cfg json.RawMessage) DiscoveryData {
	t := pkgmodel.Target{Label: label, Namespace: "FakeAWS", Config: cfg}
	return DiscoveryData{
		targets:         map[string]pkgmodel.Target{label: t},
		resolvedTargets: make(map[string]bool),
		failedTargets:   make(map[string]bool),
	}
}

// TestEnsureTargetResolved_OpaqueRefResolvedOnce asserts that the first call
// for a target with an opaque $ref invokes resolveTargetConfigForList (one
// LoadResource call), stores the resolved config back into data.targets, and
// marks the target resolved. A second call with the same data hits the
// resolvedTargets cache and issues no further LoadResource calls.
func TestEnsureTargetResolved_OpaqueRefResolvedOnce(t *testing.T) {
	const label = "prod"
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

	cfg := buildOpaqueTargetConfig(ksuid, prop)
	data := buildDiscoveryDataWithTarget(label, cfg)

	// First call: should resolve the opaque $ref.
	data, ok := ensureTargetResolved(data, label, proc)
	require.True(t, ok, "first call must succeed")
	assert.True(t, data.resolvedTargets[label], "target must be marked resolved")
	assert.False(t, data.failedTargets[label], "target must not be marked failed")
	resolvedCfg := string(data.targets[label].Config)
	assert.Contains(t, resolvedCfg, plaintext, "resolved config must contain the plaintext")
	assert.NotContains(t, resolvedCfg, `"$ref"`, "resolved config must not contain $ref")
	loadCallsAfterFirst := len(proc.loadResourceCalls)
	assert.Equal(t, 1, loadCallsAfterFirst, "exactly one LoadResource call on first resolve")

	// Second call: must hit the cache, no additional LoadResource.
	data, ok = ensureTargetResolved(data, label, proc)
	require.True(t, ok, "second call must return true (cache hit)")
	assert.Equal(t, loadCallsAfterFirst, len(proc.loadResourceCalls),
		"second call must not issue another LoadResource (cache hit)")
}

// TestEnsureTargetResolved_FailedTargetNotRetried asserts that a target already
// in failedTargets returns false immediately without calling resolveTargetConfigForList.
func TestEnsureTargetResolved_FailedTargetNotRetried(t *testing.T) {
	const label = "broken"
	proc := &resolveStubProcess{}

	cfg := json.RawMessage(`{"Region":"us-east-1"}`)
	data := buildDiscoveryDataWithTarget(label, cfg)
	data.failedTargets[label] = true

	data, ok := ensureTargetResolved(data, label, proc)
	require.False(t, ok, "a target already in failedTargets must return false")
	assert.Empty(t, proc.loadResourceCalls,
		"no LoadResource must be issued for a pre-failed target")
}

// TestEnsureTargetResolved_ResolveFailureMarksTargetFailed asserts that when
// resolveTargetConfigForList returns an error the target is added to
// failedTargets and the call returns false.
func TestEnsureTargetResolved_ResolveFailureMarksTargetFailed(t *testing.T) {
	const label = "prod"
	const ksuid = "35R2vyf6mT5wEs0mTWT5bp1Lf0E"
	const prop = "SecretString"

	proc := &resolveStubProcess{
		loadErr: fmt.Errorf("datastore unavailable"),
	}

	cfg := buildOpaqueTargetConfig(ksuid, prop)
	data := buildDiscoveryDataWithTarget(label, cfg)

	data, ok := ensureTargetResolved(data, label, proc)
	require.False(t, ok, "a resolve failure must return false")
	assert.True(t, data.failedTargets[label], "target must be marked failed after a resolve error")
	assert.False(t, data.resolvedTargets[label], "target must not be marked resolved after a resolve error")
}

// TestEnsureTargetResolved_NoOpaqueRefPassthrough asserts that a target config
// with no opaque refs is passed through unchanged and is immediately cached as
// resolved without issuing any LoadResource or Read calls.
func TestEnsureTargetResolved_NoOpaqueRefPassthrough(t *testing.T) {
	const label = "plain"
	proc := &resolveStubProcess{}

	cfg := json.RawMessage(`{"Region":"us-east-1","AccountId":"123456789012"}`)
	data := buildDiscoveryDataWithTarget(label, cfg)

	data, ok := ensureTargetResolved(data, label, proc)
	require.True(t, ok, "a target with no opaque refs must succeed")
	assert.True(t, data.resolvedTargets[label])
	assert.Empty(t, proc.loadResourceCalls, "no LoadResource for a plain config")
	assert.Equal(t, string(cfg), string(data.targets[label].Config),
		"plain config must be stored unchanged")
}

// TestEnsureTargetResolved_ResolveFailureLogsNoSecret asserts that when a
// target's opaque reference cannot be resolved, none of the log lines produced
// on that failure path contain the plaintext secret value or a raw "$value"
// envelope field. This test runs at the ensureTargetResolved seam: it
// exercises the full call chain (ensureTargetResolved → resolveTargetConfigForList)
// with a recording log and a failing LoadResource stub, then scans every captured
// message for the sentinel values.
func TestEnsureTargetResolved_ResolveFailureLogsNoSecret(t *testing.T) {
	const label = "prod"
	const ksuid = "35R2vyf6mT5wEs0mTWT5bp1Lf0E"
	const prop = "SecretString"
	// plaintext is the value stored in the $value envelope; it must not leak
	// into any log line on the failure path.
	const plaintext = "super-secret-credential-value"

	inner := &resolveStubProcess{
		loadErr: fmt.Errorf("secret source unavailable"),
	}
	recLog := &recordingLog{}
	proc := &recordingProcess{resolveStubProcess: inner, log: recLog}

	// Build a config with the plaintext embedded in the $value envelope so any
	// accidental serialisation of the raw config would contain the sentinel.
	targetCfg := json.RawMessage(strings.ReplaceAll(
		string(buildOpaqueTargetConfig(ksuid, prop)),
		"old-value", plaintext,
	))
	data := buildDiscoveryDataWithTarget(label, targetCfg)

	data, ok := ensureTargetResolved(data, label, proc)

	require.False(t, ok, "resolve must fail when the source is unavailable")
	assert.True(t, data.failedTargets[label], "failed target must be recorded")

	for _, msg := range recLog.allMessages() {
		assert.NotContains(t, msg, plaintext,
			"log line must not contain the plaintext secret: %s", msg)
		assert.NotContains(t, msg, "$value",
			"log line must not include raw $ref envelope fields: %s", msg)
	}
}

// resolveFailureProcess is a gen.Process double that makes resolveTargetConfigForList
// fail (LoadResource returns an error) while also acting as a rate-limiter stub
// for the resumeScanning outer loop. It records every SpawnPluginOperator request
// so tests can assert that no ListResources is ever dispatched for the failed target.
type resolveFailureProcess struct {
	gen.Process
	log           *recordingLog
	loadErr       error
	spawnRequests []messages.SpawnPluginOperator
}

func (p *resolveFailureProcess) Log() gen.Log            { return p.log }
func (p *resolveFailureProcess) Node() gen.Node          { return stubNode{} }
func (p *resolveFailureProcess) PID() gen.PID            { return gen.PID{Node: "test-node", ID: 1} }
func (p *resolveFailureProcess) Send(_ any, _ any) error { return nil }

func (p *resolveFailureProcess) Call(_ any, message any) (any, error) {
	switch m := message.(type) {
	case changeset.RequestTokens:
		return changeset.TokensGranted{N: m.N}, nil
	case messages.LoadResource:
		if p.loadErr != nil {
			return nil, p.loadErr
		}
		return nil, fmt.Errorf("resolveFailureProcess: no load result configured")
	case messages.SpawnPluginOperator:
		p.spawnRequests = append(p.spawnRequests, m)
		return messages.SpawnPluginOperatorResult{}, nil
	default:
		return nil, fmt.Errorf("resolveFailureProcess: unexpected Call %T", message)
	}
}

// TestResumeScanning_FailedResolveNeverDispatchesListResources asserts that
// when a target's opaque config cannot be resolved, resumeScanning skips the
// SpawnPluginOperator call entirely — no ListResources reaches the plugin —
// and the discovery cycle still completes (returns StateIdle) rather than
// hanging in StateDiscovering.
func TestResumeScanning_FailedResolveNeverDispatchesListResources(t *testing.T) {
	const namespace = "FakeAWS"
	const targetLabel = "us-east-1"
	const ksuid = "35R2vyf6mT5wEs0mTWT5bp1Lf0E"
	const prop = "SecretString"
	const plaintext = "top-secret-cred"

	// Target has an opaque $ref that cannot be resolved.
	opaqueConfig := json.RawMessage(strings.ReplaceAll(
		string(buildOpaqueTargetConfig(ksuid, prop)),
		"old-value", plaintext,
	))

	proc := &resolveFailureProcess{
		log:     &recordingLog{},
		loadErr: fmt.Errorf("secret manager unavailable"),
	}

	data := DiscoveryData{
		discoveryCfg: &pkgmodel.DiscoveryConfig{Enabled: true, Interval: 20 * time.Second},
		targets: map[string]pkgmodel.Target{
			targetLabel: {Label: targetLabel, Namespace: namespace, Config: opaqueConfig},
		},
		resourceDescriptors: map[string]plugin.ResourceDescriptor{
			"FakeAWS::S3::Bucket": {Type: "FakeAWS::S3::Bucket", Discoverable: true},
		},
		queuedListOperations: map[string][]ListOperation{
			namespace: {
				{ResourceType: "FakeAWS::S3::Bucket", TargetLabel: targetLabel},
			},
		},
		outstandingListOperations: map[string]ListOperation{},
		outstandingSyncCommands:   map[string]ListOperation{},
		summary:                   map[string]int{},
		resolvedTargets:           make(map[string]bool),
		failedTargets:             make(map[string]bool),
		typesWithChildrenQueued:   map[string]struct{}{},
		nativeIDsByCommand:        map[string][]string{},
	}

	nextState, resultData, _, err := resumeScanning(gen.PID{}, StateDiscovering, data, ResumeScanning{}, proc)

	require.NoError(t, err, "a resolve failure must not crash the actor")
	assert.Empty(t, proc.spawnRequests,
		"SpawnPluginOperator must not be called for a target whose config resolution failed")
	assert.True(t, resultData.failedTargets[targetLabel],
		"the target must be recorded in failedTargets")
	assert.Equal(t, StateIdle, nextState,
		"a cycle where all targets fail resolution must still return to Idle")

	// Redaction invariant: no log line must contain the plaintext or a raw "$value".
	for _, msg := range proc.log.allMessages() {
		assert.NotContains(t, msg, plaintext,
			"log line must not leak the plaintext credential: %s", msg)
		assert.NotContains(t, msg, "$value",
			"log line must not include raw $ref envelope fields: %s", msg)
	}
}

// TestOnStateChange_WarnsAboutFailedTargets asserts that when transitioning
// from StateDiscovering to StateIdle with at least one failed target, a warning
// log line is produced that names the failed target label(s) and their count.
// No config content is logged — only labels, which are safe to surface.
func TestOnStateChange_WarnsAboutFailedTargets(t *testing.T) {
	recLog := &recordingLog{}
	proc := &onStateChangeProc{log: recLog}

	data := DiscoveryData{
		timeStarted: time.Now().Add(-5 * time.Second),
		summary:     map[string]int{},
		failedTargets: map[string]bool{
			"prod-us-east-1": true,
			"staging-eu":     true,
		},
	}

	_, _, err := onStateChange(StateDiscovering, StateIdle, data, proc)
	require.NoError(t, err)

	msgs := recLog.allMessages()
	found := false
	for _, msg := range msgs {
		if strings.Contains(msg, "2") && strings.Contains(msg, "prod-us-east-1") && strings.Contains(msg, "staging-eu") {
			found = true
		}
	}
	assert.True(t, found,
		"completion warning must name the count and labels of all failed targets, got: %v", msgs)

	// Labels are safe to log but config contents must not appear.
	for _, msg := range msgs {
		assert.NotContains(t, msg, "$value",
			"completion log must not include raw $ref envelope fields: %s", msg)
	}
}

// onStateChangeProc is a minimal gen.Process stub whose Log() returns the
// recording logger used by TestOnStateChange_WarnsAboutFailedTargets.
type onStateChangeProc struct {
	gen.Process
	log *recordingLog
}

func (p *onStateChangeProc) Log() gen.Log   { return p.log }
func (p *onStateChangeProc) Node() gen.Node { return stubNode{} }
func (p *onStateChangeProc) PID() gen.PID   { return gen.PID{Node: "test-node", ID: 3} }
