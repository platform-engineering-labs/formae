// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/masterminds/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const (
	deadlineTestNamespace = "test"
	// deadlineSlack absorbs the time between deriving a call context and
	// observing its deadline inside the test.
	deadlineSlack = time.Second
)

// recordingPlugin is a FullResourcePlugin double that records the context handed
// to each operation and optionally fails every call with a canned error.
type recordingPlugin struct {
	mu   sync.Mutex
	ctxs map[resource.Operation]context.Context
	err  error
}

func newRecordingPlugin() *recordingPlugin {
	return &recordingPlugin{ctxs: make(map[resource.Operation]context.Context)}
}

func (p *recordingPlugin) record(operation resource.Operation, ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ctxs[operation] = ctx
}

// contextFor returns the context the plugin received for an operation.
func (p *recordingPlugin) contextFor(t *testing.T, operation resource.Operation) context.Context {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, ok := p.ctxs[operation]
	require.True(t, ok, "plugin was never called for operation %s", operation)
	return ctx
}

func (p *recordingPlugin) RateLimit() pkgmodel.RateLimitConfig {
	return pkgmodel.RateLimitConfig{Scope: pkgmodel.RateLimitScopeNamespace, MaxRequestsPerSecondForNamespace: 10}
}

func (p *recordingPlugin) DiscoveryFilters() []pkgmodel.MatchFilter { return nil }

func (p *recordingPlugin) LabelConfig() pkgmodel.LabelConfig {
	return pkgmodel.LabelConfig{DefaultQuery: "$.Name"}
}

func (p *recordingPlugin) Name() string      { return "recording-plugin" }
func (p *recordingPlugin) Namespace() string { return deadlineTestNamespace }
func (p *recordingPlugin) Version() *semver.Version {
	return semver.MustParse("1.0.0")
}
func (p *recordingPlugin) SupportedResources() []ResourceDescriptor { return nil }
func (p *recordingPlugin) SchemaForResourceType(string) (pkgmodel.Schema, error) {
	return pkgmodel.Schema{}, nil
}

func (p *recordingPlugin) Create(ctx context.Context, _ *resource.CreateRequest) (*resource.CreateResult, error) {
	p.record(resource.OperationCreate, ctx)
	if p.err != nil {
		return nil, p.err
	}
	return &resource.CreateResult{ProgressResult: inProgress(resource.OperationCreate)}, nil
}

func (p *recordingPlugin) Read(ctx context.Context, _ *resource.ReadRequest) (*resource.ReadResult, error) {
	p.record(resource.OperationRead, ctx)
	if p.err != nil {
		return nil, p.err
	}
	return &resource.ReadResult{Properties: `{"Name":"resource"}`}, nil
}

func (p *recordingPlugin) Update(ctx context.Context, _ *resource.UpdateRequest) (*resource.UpdateResult, error) {
	p.record(resource.OperationUpdate, ctx)
	if p.err != nil {
		return nil, p.err
	}
	return &resource.UpdateResult{ProgressResult: inProgress(resource.OperationUpdate)}, nil
}

func (p *recordingPlugin) Delete(ctx context.Context, _ *resource.DeleteRequest) (*resource.DeleteResult, error) {
	p.record(resource.OperationDelete, ctx)
	if p.err != nil {
		return nil, p.err
	}
	return &resource.DeleteResult{ProgressResult: inProgress(resource.OperationDelete)}, nil
}

func (p *recordingPlugin) Status(ctx context.Context, _ *resource.StatusRequest) (*resource.StatusResult, error) {
	p.record(resource.OperationCheckStatus, ctx)
	if p.err != nil {
		return nil, p.err
	}
	return &resource.StatusResult{ProgressResult: inProgress(resource.OperationCheckStatus)}, nil
}

func (p *recordingPlugin) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	p.record(resource.OperationList, ctx)
	if p.err != nil {
		return nil, p.err
	}
	return &resource.ListResult{NativeIDs: []string{"resource-1"}}, nil
}

func inProgress(operation resource.Operation) *resource.ProgressResult {
	return &resource.ProgressResult{
		Operation:       operation,
		OperationStatus: resource.OperationStatusInProgress,
		NativeID:        "resource-1",
		RequestID:       "request-1",
	}
}

// stubOperatorLog swallows all log output for plugin operator tests.
type stubOperatorLog struct{ gen.Log }

func (stubOperatorLog) Trace(string, ...any)   {}
func (stubOperatorLog) Debug(string, ...any)   {}
func (stubOperatorLog) Info(string, ...any)    {}
func (stubOperatorLog) Warning(string, ...any) {}
func (stubOperatorLog) Error(string, ...any)   {}
func (stubOperatorLog) Panic(string, ...any)   {}

// stubOperatorNode exposes a node-level environment so tests can exercise the
// operator's node fallback.
type stubOperatorNode struct {
	gen.Node
	env map[gen.Env]any
}

func (n stubOperatorNode) Name() gen.Atom { return gen.Atom("test-node") }

func (n stubOperatorNode) Env(name gen.Env) (any, bool) {
	v, ok := n.env[name]
	return v, ok
}

// stubOperatorProcess is a hand-rolled gen.Process double for PluginOperator
// tests. It serves the process environment, records every proc.Send message and
// every proc.SendAfter reschedule.
type stubOperatorProcess struct {
	gen.Process

	behavior gen.ProcessBehavior
	env      map[gen.Env]any
	node     stubOperatorNode

	mu         sync.Mutex
	sends      []any
	sendsAfter []any
}

func (p *stubOperatorProcess) Log() gen.Log                  { return stubOperatorLog{} }
func (p *stubOperatorProcess) Node() gen.Node                { return p.node }
func (p *stubOperatorProcess) PID() gen.PID                  { return gen.PID{Node: "test-node", ID: 1} }
func (p *stubOperatorProcess) Behavior() gen.ProcessBehavior { return p.behavior }
func (p *stubOperatorProcess) Mailbox() gen.ProcessMailbox   { return gen.ProcessMailbox{} }
func (p *stubOperatorProcess) Env(name gen.Env) (any, bool) {
	v, ok := p.env[name]
	return v, ok
}

func (p *stubOperatorProcess) Send(_ any, message any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sends = append(p.sends, message)
	return nil
}

func (p *stubOperatorProcess) SendAfter(_ any, message any, _ time.Duration) (gen.CancelFunc, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sendsAfter = append(p.sendsAfter, message)
	return func() bool { return true }, nil
}

// sentProgress returns all TrackedProgress messages sent via proc.Send, in order.
func (p *stubOperatorProcess) sentProgress() []TrackedProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []TrackedProgress
	for _, s := range p.sends {
		if progress, ok := s.(TrackedProgress); ok {
			out = append(out, progress)
		}
	}
	return out
}

// scheduled returns all messages the operator rescheduled via proc.SendAfter,
// in order.
func (p *stubOperatorProcess) scheduled() []any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]any(nil), p.sendsAfter...)
}

// sentListings returns all Listing messages sent via proc.Send, in order.
func (p *stubOperatorProcess) sentListings() []Listing {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Listing
	for _, s := range p.sends {
		if listing, ok := s.(Listing); ok {
			out = append(out, listing)
		}
	}
	return out
}

func newOperatorProcess(env map[gen.Env]any, nodeEnv map[gen.Env]any) *stubOperatorProcess {
	return &stubOperatorProcess{
		env:  env,
		node: stubOperatorNode{env: nodeEnv},
	}
}

// assertCallDeadline asserts the context bounds the call by budget: the deadline
// must not exceed budget from now, and must not be materially shorter either.
func assertCallDeadline(t *testing.T, ctx context.Context, budget time.Duration) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	require.True(t, ok, "a watched plugin call must receive a context with a deadline")
	remaining := time.Until(deadline)
	assert.LessOrEqual(t, remaining, budget, "call deadline must not exceed the supplied budget")
	assert.Greater(t, remaining, budget-deadlineSlack, "call deadline must span the supplied budget")
}

func deadlineTestData(plugin FullResourcePlugin, callTimeout time.Duration) PluginUpdateData {
	return PluginUpdateData{
		attempts:    1,
		callTimeout: callTimeout,
		context:     context.Background(),
		plugin:      plugin,
		config: pkgmodel.RetryConfig{
			MaxRetries:          3,
			RetryDelay:          time.Millisecond,
			StatusCheckInterval: 20 * time.Second,
		},
	}
}

func TestPluginOperatorInit_UsesCallTimeoutFromProcessEnv(t *testing.T) {
	operator := &PluginOperator{}
	proc := newOperatorProcess(map[gen.Env]any{
		"Plugin":            newRecordingPlugin(),
		"Context":           context.Background(),
		"RetryConfig":       pkgmodel.RetryConfig{MaxRetries: 3},
		"PluginCallTimeout": 45 * time.Second,
	}, nil)
	proc.behavior = operator

	require.NoError(t, operator.ProcessInit(proc))
	assert.Equal(t, 45*time.Second, operator.Data().callTimeout)
}

func TestPluginOperatorInit_FallsBackToNodeCallTimeout(t *testing.T) {
	operator := &PluginOperator{}
	proc := newOperatorProcess(map[gen.Env]any{
		"Plugin":      newRecordingPlugin(),
		"Context":     context.Background(),
		"RetryConfig": pkgmodel.RetryConfig{MaxRetries: 3},
	}, map[gen.Env]any{
		"PluginCallTimeout": 90 * time.Second,
	})
	proc.behavior = operator

	require.NoError(t, operator.ProcessInit(proc))
	assert.Equal(t, 90*time.Second, operator.Data().callTimeout)
}

func TestPluginOperatorInit_FallsBackToDefaultCallTimeout(t *testing.T) {
	tests := []struct {
		name string
		env  map[gen.Env]any
	}{
		{name: "absent", env: nil},
		{name: "zero", env: map[gen.Env]any{"PluginCallTimeout": time.Duration(0)}},
		{name: "negative", env: map[gen.Env]any{"PluginCallTimeout": -5 * time.Second}},
		{name: "wrong type", env: map[gen.Env]any{"PluginCallTimeout": "45s"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[gen.Env]any{
				"Plugin":      newRecordingPlugin(),
				"Context":     context.Background(),
				"RetryConfig": pkgmodel.RetryConfig{MaxRetries: 3},
			}
			for k, v := range tt.env {
				env[k] = v
			}

			operator := &PluginOperator{}
			proc := newOperatorProcess(env, nil)
			proc.behavior = operator

			require.NoError(t, operator.ProcessInit(proc))
			assert.Equal(t, defaultPluginCallTimeout, operator.Data().callTimeout)
		})
	}
}

func TestWatchedOperationsBoundPluginCallByDeadline(t *testing.T) {
	const callTimeout = 90 * time.Second

	tests := []struct {
		name      string
		operation resource.Operation
		invoke    func(data PluginUpdateData, proc gen.Process)
	}{
		{
			name:      "read",
			operation: resource.OperationRead,
			invoke: func(data PluginUpdateData, proc gen.Process) {
				read(gen.PID{}, StateNotStarted, data, ReadResource{Namespace: deadlineTestNamespace, NativeID: "resource-1"}, proc)
			},
		},
		{
			name:      "create",
			operation: resource.OperationCreate,
			invoke: func(data PluginUpdateData, proc gen.Process) {
				create(gen.PID{}, StateNotStarted, data, CreateResource{Namespace: deadlineTestNamespace, ResourceType: "Test::Resource"}, proc)
			},
		},
		{
			name:      "update",
			operation: resource.OperationUpdate,
			invoke: func(data PluginUpdateData, proc gen.Process) {
				update(gen.PID{}, StateNotStarted, data, UpdateResource{Namespace: deadlineTestNamespace, NativeID: "resource-1"}, proc)
			},
		},
		{
			name:      "delete",
			operation: resource.OperationDelete,
			invoke: func(data PluginUpdateData, proc gen.Process) {
				delete(gen.PID{}, StateNotStarted, data, DeleteResource{Namespace: deadlineTestNamespace, NativeID: "resource-1"}, proc)
			},
		},
		{
			name:      "status",
			operation: resource.OperationCheckStatus,
			invoke: func(data PluginUpdateData, proc gen.Process) {
				status(gen.PID{}, StateWaitingForResource, data, PluginOperatorCheckStatus{Namespace: deadlineTestNamespace, RequestID: "request-1"}, proc)
			},
		},
		{
			name:      "resume",
			operation: resource.OperationCheckStatus,
			invoke: func(data PluginUpdateData, proc gen.Process) {
				resume(gen.PID{}, StateNotStarted, data, ResumeWaitingForResource{
					Namespace:         deadlineTestNamespace,
					ResourceOperation: resource.OperationCreate,
					Request:           PluginOperatorCheckStatus{Namespace: deadlineTestNamespace, RequestID: "request-1"},
					PreviousAttempts:  1,
				}, proc)
			},
		},
		{
			name:      "retried create",
			operation: resource.OperationCreate,
			invoke: func(data PluginUpdateData, proc gen.Process) {
				retry(gen.PID{}, StateRetrying, data, PluginOperatorRetry{
					ResourceOperation: resource.OperationCreate,
					Request:           CreateResource{Namespace: deadlineTestNamespace, ResourceType: "Test::Resource"},
				}, proc)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := newRecordingPlugin()
			proc := newOperatorProcess(nil, nil)

			tt.invoke(deadlineTestData(plugin, callTimeout), proc)

			assertCallDeadline(t, plugin.contextFor(t, tt.operation), callTimeout)
		})
	}
}

func TestList_PluginCallHasNoDeadline(t *testing.T) {
	plugin := newRecordingPlugin()
	proc := newOperatorProcess(nil, nil)

	state, _, _, err := list(gen.PID{}, StateNotStarted, deadlineTestData(plugin, 90*time.Second),
		ListResources{Namespace: deadlineTestNamespace, ResourceType: "Test::Resource"}, proc)

	require.NoError(t, err)
	assert.Equal(t, StateFinishedSuccessfully, state)
	require.Len(t, proc.sentListings(), 1)

	_, hasDeadline := plugin.contextFor(t, resource.OperationList).Deadline()
	assert.False(t, hasDeadline, "discovery owns its own paging budget and must not be bounded by the per-call deadline")
}

func TestReadOnlyOperationsClassifyDeadlineAsServiceTimeout(t *testing.T) {
	const callTimeout = 90 * time.Second

	tests := []struct {
		name          string
		wantState     gen.Atom
		wantOperation resource.Operation
		wantScheduled any
		invoke        func(data PluginUpdateData, proc gen.Process) (gen.Atom, TrackedProgress)
	}{
		{
			name:          "read",
			wantState:     StateRetrying,
			wantOperation: resource.OperationRead,
			// A read carries no provider side effect, so the ladder re-issues it.
			wantScheduled: PluginOperatorRetry{
				ResourceOperation: resource.OperationRead,
				Request:           ReadResource{Namespace: deadlineTestNamespace, NativeID: "resource-1"},
			},
			invoke: func(data PluginUpdateData, proc gen.Process) (gen.Atom, TrackedProgress) {
				state, _, progress, _, _ := read(gen.PID{}, StateNotStarted, data,
					ReadResource{Namespace: deadlineTestNamespace, NativeID: "resource-1"}, proc)
				return state, progress
			},
		},
		{
			name:          "resume",
			wantState:     StateWaitingForResource,
			wantOperation: resource.OperationCreate,
			wantScheduled: PluginOperatorCheckStatus{
				Namespace:         deadlineTestNamespace,
				RequestID:         "request-1",
				NativeID:          "resource-1",
				ResourceOperation: resource.OperationCreate,
			},
			invoke: func(data PluginUpdateData, proc gen.Process) (gen.Atom, TrackedProgress) {
				state, _, progress, _, _ := resume(gen.PID{}, StateNotStarted, data, ResumeWaitingForResource{
					Namespace:         deadlineTestNamespace,
					ResourceOperation: resource.OperationCreate,
					Request: PluginOperatorCheckStatus{
						Namespace:         deadlineTestNamespace,
						RequestID:         "request-1",
						NativeID:          "resource-1",
						ResourceOperation: resource.OperationCreate,
					},
					PreviousAttempts: 1,
				}, proc)
				return state, progress
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := newRecordingPlugin()
			plugin.err = fmt.Errorf("calling the cloud API: %w", context.DeadlineExceeded)
			proc := newOperatorProcess(nil, nil)

			state, progress := tt.invoke(deadlineTestData(plugin, callTimeout), proc)

			assert.Equal(t, tt.wantState, state)
			assert.Equal(t, tt.wantOperation, progress.Operation)
			assert.Equal(t, resource.OperationStatusFailure, progress.OperationStatus)
			assert.Equal(t, resource.OperationErrorCodeServiceTimeout, progress.ErrorCode)
			assert.False(t, progress.Failed(), "retries remain, so the operation must not be terminal")
			assert.Contains(t, progress.StatusMessage, callTimeout.String())

			scheduled := proc.scheduled()
			require.Len(t, scheduled, 1, "the retry ladder must reschedule the operation so every attempt heartbeats")
			assert.Equal(t, tt.wantScheduled, scheduled[0])
		})
	}
}

func TestStatus_ClassifiesDeadlineAsServiceTimeout(t *testing.T) {
	const callTimeout = 90 * time.Second

	plugin := newRecordingPlugin()
	plugin.err = fmt.Errorf("calling the cloud API: %w", context.DeadlineExceeded)
	proc := newOperatorProcess(nil, nil)

	state, _, _, err := status(gen.PID{}, StateWaitingForResource, deadlineTestData(plugin, callTimeout),
		PluginOperatorCheckStatus{
			Namespace:         deadlineTestNamespace,
			RequestID:         "request-1",
			NativeID:          "resource-1",
			ResourceOperation: resource.OperationCreate,
		}, proc)

	require.NoError(t, err)
	assert.Equal(t, StateWaitingForResource, state, "a status check that outran its deadline must not fail the operation")

	sent := proc.sentProgress()
	require.Len(t, sent, 1, "the resource updater must be told the status check outran its deadline")
	assert.Equal(t, resource.OperationCreate, sent[0].Operation)
	assert.Equal(t, resource.OperationStatusFailure, sent[0].OperationStatus)
	assert.Equal(t, resource.OperationErrorCodeServiceTimeout, sent[0].ErrorCode)
	assert.False(t, sent[0].Failed(), "retries remain, so the operation must not be terminal")
	assert.Contains(t, sent[0].StatusMessage, callTimeout.String())
}

// TestFailedStatusCallNeverReissuesTheOriginalOperation covers a status check
// that carries the request that started it — the local-path shape, where the
// retry ladder would otherwise re-issue that request. A create that may already
// have reached the provider must never be sent again, so a status call that
// fails recoverably reschedules another status check instead.
func TestFailedStatusCallNeverReissuesTheOriginalOperation(t *testing.T) {
	const callTimeout = 90 * time.Second

	originalCreate := CreateResource{Namespace: deadlineTestNamespace, ResourceType: "Test::Resource"}
	check := PluginOperatorCheckStatus{
		Namespace:         deadlineTestNamespace,
		RequestID:         "request-1",
		NativeID:          "resource-1",
		ResourceType:      "Test::Resource",
		ResourceOperation: resource.OperationCreate,
		Request:           originalCreate,
	}

	tests := []struct {
		name   string
		err    error
		invoke func(data PluginUpdateData, proc gen.Process) gen.Atom
	}{
		{
			name: "status past its deadline",
			err:  fmt.Errorf("calling the cloud API: %w", context.DeadlineExceeded),
			invoke: func(data PluginUpdateData, proc gen.Process) gen.Atom {
				state, _, _, _ := status(gen.PID{}, StateWaitingForResource, data, check, proc)
				return state
			},
		},
		{
			name: "throttled status",
			err:  errors.New("ThrottlingException: Rate exceeded"),
			invoke: func(data PluginUpdateData, proc gen.Process) gen.Atom {
				state, _, _, _ := status(gen.PID{}, StateWaitingForResource, data, check, proc)
				return state
			},
		},
		{
			name: "resume past its deadline",
			err:  fmt.Errorf("calling the cloud API: %w", context.DeadlineExceeded),
			invoke: func(data PluginUpdateData, proc gen.Process) gen.Atom {
				state, _, _, _, _ := resume(gen.PID{}, StateNotStarted, data, ResumeWaitingForResource{
					Namespace:         deadlineTestNamespace,
					ResourceOperation: resource.OperationCreate,
					Request:           check,
					PreviousAttempts:  1,
				}, proc)
				return state
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := newRecordingPlugin()
			plugin.err = tt.err
			proc := newOperatorProcess(nil, nil)

			state := tt.invoke(deadlineTestData(plugin, callTimeout), proc)

			assert.Equal(t, StateWaitingForResource, state)

			scheduled := proc.scheduled()
			require.Len(t, scheduled, 1)
			rescheduledCheck, ok := scheduled[0].(PluginOperatorCheckStatus)
			require.True(t, ok, "a status check that outran its deadline must reschedule a status check, got %T", scheduled[0])
			assert.Nil(t, rescheduledCheck.Request, "the rescheduled check must not carry the request that would re-issue the operation")
			assert.Equal(t, check.RequestID, rescheduledCheck.RequestID)
			assert.Equal(t, check.NativeID, rescheduledCheck.NativeID)
			assert.Equal(t, check.ResourceType, rescheduledCheck.ResourceType)
			assert.Equal(t, check.ResourceOperation, rescheduledCheck.ResourceOperation)

			_, called := plugin.ctxs[resource.OperationCreate]
			assert.False(t, called, "the mutating operation must never be re-issued")
		})
	}
}

func TestMutatingOperationsClassifyDeadlineAsTerminal(t *testing.T) {
	const callTimeout = 90 * time.Second

	tests := []struct {
		name   string
		invoke func(data PluginUpdateData, proc gen.Process) (gen.Atom, TrackedProgress)
	}{
		{
			name: "create",
			invoke: func(data PluginUpdateData, proc gen.Process) (gen.Atom, TrackedProgress) {
				state, _, progress, _, _ := create(gen.PID{}, StateNotStarted, data,
					CreateResource{Namespace: deadlineTestNamespace, ResourceType: "Test::Resource"}, proc)
				return state, progress
			},
		},
		{
			name: "update",
			invoke: func(data PluginUpdateData, proc gen.Process) (gen.Atom, TrackedProgress) {
				state, _, progress, _, _ := update(gen.PID{}, StateNotStarted, data,
					UpdateResource{Namespace: deadlineTestNamespace, NativeID: "resource-1", DesiredProperties: json.RawMessage(`{}`)}, proc)
				return state, progress
			},
		},
		{
			name: "delete",
			invoke: func(data PluginUpdateData, proc gen.Process) (gen.Atom, TrackedProgress) {
				state, _, progress, _, _ := delete(gen.PID{}, StateNotStarted, data,
					DeleteResource{Namespace: deadlineTestNamespace, NativeID: "resource-1"}, proc)
				return state, progress
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := newRecordingPlugin()
			plugin.err = fmt.Errorf("calling the cloud API: %w", context.DeadlineExceeded)
			proc := newOperatorProcess(nil, nil)

			state, progress := tt.invoke(deadlineTestData(plugin, callTimeout), proc)

			assert.Equal(t, StateFinishedWithError, state, "a mutating call that may have reached the provider must not be repeated")
			assert.Equal(t, resource.OperationErrorCodeUnforeseenError, progress.ErrorCode)
			assert.True(t, progress.Failed())
			assert.Contains(t, progress.StatusMessage, callTimeout.String())
			assert.Contains(t, progress.StatusMessage, "calling the cloud API", "the plugin's own error text must stay diagnosable")
			assert.Empty(t, proc.sendsAfter, "no retry may be scheduled for a mutating call that outran its deadline")
		})
	}
}
