// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceUpdater_Initialization(t *testing.T) {
	sender := gen.PID{Node: "test", ID: 100}

	ds := newMockDatastore()
	env := map[gen.Env]any{
		"RetryConfig": pkgmodel.RetryConfig{
			StatusCheckInterval: 1 * time.Second,
		},
		"DiscoveryConfig": pkgmodel.DiscoveryConfig{},
		"Datastore":       ds,
	}

	updater, err := unit.Spawn(t, newResourceUpdater,
		unit.WithArgs(sender),
		unit.WithEnv(env))

	assert.NoError(t, err, "Failed to spawn resource updater")
	assert.NotNil(t, updater, "Resource updater should not be nil")
	assert.False(t, updater.IsTerminated(), "Resource updater should not be terminated after spawning")
}

// TestResolveValue_KeepsPatchDocumentInSync covers the ResolveValue
// contract: PatchDocument is derived from (PriorState, DesiredState,
// Schema), so whenever ResolveValue mutates DesiredState.Properties it
// must re-derive the patch. Mirrors the cascade-update scenario where a
// parent (e.g. ECS TaskDefinition) is being replaced and the dependent's
// $value tracks the parent's new identifier — the eventual plugin Update
// must see a patch reflecting that change.
func TestResolveValue_KeepsPatchDocumentInSync(t *testing.T) {
	parentKsuid := "3E3wKW8YqVCQEyfKjsGpbsoE8bl"
	parentURI := pkgmodel.FormaeURI("formae://" + parentKsuid + "#/TaskDefinitionArn")

	priorProps := json.RawMessage(`{
		"ServiceName": "formae-sdk-test-svc",
		"TaskDefinition": {
			"$ref": "formae://` + parentKsuid + `#/TaskDefinitionArn",
			"$value": "arn:aws:ecs:us-east-1:0:task-definition/test:1"
		},
		"DesiredCount": 0
	}`)

	schema := pkgmodel.Schema{
		Identifier: "ServiceName",
		Fields:     []string{"ServiceName", "TaskDefinition", "DesiredCount"},
		Hints: map[string]pkgmodel.FieldHint{
			"ServiceName":    {CreateOnly: true},
			"TaskDefinition": {CreateOnly: false},
			"DesiredCount":   {CreateOnly: false},
		},
	}

	ru := &ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label:      "svc",
			Properties: priorProps,
			Schema:     schema,
		},
		DesiredState: pkgmodel.Resource{
			Label: "svc",
			// DesiredState starts byte-identical to prior — typical for a
			// cascade-update emitted by the planner where the user didn't
			// directly modify the dependent.
			Properties: append(json.RawMessage(nil), priorProps...),
			Schema:     schema,
		},
	}

	err := ru.ResolveValue(parentURI, "arn:aws:ecs:us-east-1:0:task-definition/test:2")
	require.NoError(t, err)
	require.NotEmpty(t, ru.DesiredState.PatchDocument, "ResolveValue must populate PatchDocument when DesiredState changes")
	patchStr := string(ru.DesiredState.PatchDocument)
	assert.Contains(t, patchStr, "TaskDefinition", "patch must target the path whose $value changed")
	assert.Contains(t, patchStr, "task-definition/test:2", "patch must carry the post-resolution $value")
	assert.NotContains(t, patchStr, "task-definition/test:1", "patch must not carry the pre-resolution $value")
	assert.False(t, strings.Contains(patchStr, "DesiredCount"),
		"patch should only diff the changed field, not stable ones: %s", patchStr)
}

// TestResolveValue_NoChangeProducesEmptyPatch confirms that resolving a
// $ref to the same $value it already has yields an empty/no-op patch —
// no spurious provider calls.
func TestResolveValue_NoChangeProducesEmptyPatch(t *testing.T) {
	parentKsuid := "parent-ksuid"
	parentURI := pkgmodel.FormaeURI("formae://" + parentKsuid + "#/Arn")
	props := json.RawMessage(`{
		"ServiceName": "svc",
		"ParentRef": {"$ref": "formae://` + parentKsuid + `#/Arn", "$value": "v1"}
	}`)
	schema := pkgmodel.Schema{
		Identifier: "ServiceName",
		Fields:     []string{"ServiceName", "ParentRef"},
		Hints: map[string]pkgmodel.FieldHint{
			"ServiceName": {CreateOnly: true},
			"ParentRef":   {CreateOnly: false},
		},
	}
	ru := &ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Properties: props,
			Schema:     schema,
		},
		DesiredState: pkgmodel.Resource{
			Properties: append(json.RawMessage(nil), props...),
			Schema:     schema,
		},
	}
	err := ru.ResolveValue(parentURI, "v1")
	require.NoError(t, err)
	if len(ru.DesiredState.PatchDocument) > 0 {
		assert.Equal(t, "[]", string(ru.DesiredState.PatchDocument),
			"no $value change should produce no patch ops, got %s", string(ru.DesiredState.PatchDocument))
	}
}

// TestResolveValue_NonUpdateLeavesPatchDocumentUntouched ensures the
// patch-regen branch only fires for Updates. Creates and Deletes carry
// full state to the provider rather than a diff, so they don't need —
// and shouldn't pay for — a regenerated PatchDocument when resolvables
// are substituted along the way.
func TestResolveValue_NonUpdateLeavesPatchDocumentUntouched(t *testing.T) {
	parentURI := pkgmodel.FormaeURI("formae://parent-ksuid#/Arn")
	props := json.RawMessage(`{"Ref": {"$ref": "formae://parent-ksuid#/Arn", "$value": "v1"}}`)
	for _, op := range []OperationType{OperationCreate, OperationDelete, OperationReplace} {
		t.Run(string(op), func(t *testing.T) {
			ru := &ResourceUpdate{
				Operation:  op,
				PriorState: pkgmodel.Resource{Properties: props},
				DesiredState: pkgmodel.Resource{
					Properties: append(json.RawMessage(nil), props...),
					// Schema with Fields so the regen WOULD fire if the
					// Operation guard weren't in place.
					Schema: pkgmodel.Schema{
						Fields: []string{"Ref"},
						Hints:  map[string]pkgmodel.FieldHint{"Ref": {CreateOnly: false}},
					},
					PatchDocument: json.RawMessage(`["pre-existing-untouched"]`),
				},
			}
			err := ru.ResolveValue(parentURI, "v2")
			require.NoError(t, err)
			assert.Equal(t, `["pre-existing-untouched"]`, string(ru.DesiredState.PatchDocument),
				"non-Update operations must not re-derive PatchDocument")
		})
	}
}

// TestMostRecentFailureMessage_FallsBackToFailureReason covers the
// terminal-resolve-miss path: the resource fails before any plugin operation
// runs, so there is no progress-based failure message. Without a fallback the
// operator sees an empty ErrorMessage. MostRecentFailureMessage must surface
// the recorded FailureReason instead.
func TestMostRecentFailureMessage_FallsBackToFailureReason(t *testing.T) {
	ru := &ResourceUpdate{
		State:         ResourceUpdateStateFailed,
		FailureReason: `could not resolve reference "formae://abc#/Arn": source resource has no property "Arn"`,
		// No ProgressResult — the failure precedes any plugin call.
	}

	assert.Equal(t, ru.FailureReason, ru.MostRecentFailureMessage(),
		"a terminal resolve miss has no plugin progress, so the FailureReason must surface as the failure message")
}

// TestMostRecentFailureMessage_PrefersProgressMessage ensures the FailureReason
// is only a fallback: a genuine plugin failure message still takes precedence
// so we never mask a richer provider error with a generic resolve reason.
func TestMostRecentFailureMessage_PrefersProgressMessage(t *testing.T) {
	const pluginErr = "AccessDenied: not authorized to perform iam:CreateRole"
	ru := &ResourceUpdate{
		State:         ResourceUpdateStateFailed,
		FailureReason: "some resolve reason that must not win",
		ProgressResult: []plugin.TrackedProgress{
			{
				ProgressResult: resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusFailure,
					StatusMessage:   pluginErr,
				},
			},
		},
	}

	assert.Equal(t, pluginErr, ru.MostRecentFailureMessage(),
		"a plugin failure message must take precedence over the resolve FailureReason fallback")
}

// stubUpdaterLog swallows all log output for handleProgressUpdate tests.
type stubUpdaterLog struct{ gen.Log }

func (stubUpdaterLog) Trace(string, ...any)   {}
func (stubUpdaterLog) Debug(string, ...any)   {}
func (stubUpdaterLog) Info(string, ...any)    {}
func (stubUpdaterLog) Warning(string, ...any) {}
func (stubUpdaterLog) Error(string, ...any)   {}
func (stubUpdaterLog) Panic(string, ...any)   {}

// stubUpdaterNode returns a fixed node name so ProcessID comparisons work.
type stubUpdaterNode struct{ gen.Node }

func (stubUpdaterNode) Name() gen.Atom { return gen.Atom("test-node") }

// stubUpdaterProcess is a hand-rolled gen.Process double for handleProgressUpdate
// tests. It records every proc.Send call so tests can assert health observations.
type stubUpdaterProcess struct {
	gen.Process

	mu    sync.Mutex
	sends []any
}

func (p *stubUpdaterProcess) Log() gen.Log    { return stubUpdaterLog{} }
func (p *stubUpdaterProcess) Node() gen.Node  { return stubUpdaterNode{} }
func (p *stubUpdaterProcess) PID() gen.PID    { return gen.PID{Node: "test-node", ID: 1} }
func (p *stubUpdaterProcess) Send(_ any, msg any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sends = append(p.sends, msg)
	return nil
}

// sentUpdateTargetHealthMessages returns all UpdateTargetHealth messages sent via
// proc.Send, in order.
func (p *stubUpdaterProcess) sentUpdateTargetHealthMessages() []messages.UpdateTargetHealth {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []messages.UpdateTargetHealth
	for _, s := range p.sends {
		if m, ok := s.(messages.UpdateTargetHealth); ok {
			out = append(out, m)
		}
	}
	return out
}

// TestHandleProgressUpdate_FilteredDiscoveryEmitsTargetHealth asserts that a
// discovery progress that is filtered out (early return from the filter branch)
// still emits an UpdateTargetHealth observation to the resource persister.
// Before the fix, the reachable emission was placed after the persist block and
// was never reached on the filter path.
func TestHandleProgressUpdate_FilteredDiscoveryEmitsTargetHealth(t *testing.T) {
	const targetLabel = "us-east-1"

	ru := &ResourceUpdate{
		Operation: OperationRead,
		DesiredState: pkgmodel.Resource{
			Label:      "my-bucket",
			Type:       "S3Bucket",
			Properties: json.RawMessage(`{"Name":"my-bucket"}`),
		},
		ResourceTarget: pkgmodel.Target{Label: targetLabel},
		// A filter with no conditions always matches — the resource will be filtered.
		MatchFilters: []pkgmodel.MatchFilter{{}},
	}

	data := ResourceUpdateData{
		resourceUpdate: ru,
		commandID:      "cmd-001",
		commandSource:  FormaCommandSourceDiscovery,
		resourceLabeler: NewResourceLabeler(&mockDatastore{
			resourcesByStack: make(map[string][]*pkgmodel.Resource),
			triplet:          make(map[pkgmodel.TripletKey]string),
		}),
	}

	successProgress := plugin.TrackedProgress{
		ProgressResult: resource.ProgressResult{
			Operation:       resource.OperationRead,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        "my-bucket",
		},
	}

	proc := &stubUpdaterProcess{}
	state, _, _, err := handleProgressUpdate(gen.PID{}, StateSynchronizing, data, successProgress, proc)

	require.NoError(t, err)
	assert.Equal(t, StateFinishedSuccessfully, state, "filtered discovery resource must finish successfully")

	healthMsgs := proc.sentUpdateTargetHealthMessages()
	require.Len(t, healthMsgs, 1, "exactly one UpdateTargetHealth must be sent even on the filter path")
	assert.Equal(t, targetLabel, healthMsgs[0].Observation.TargetLabel)
	assert.Equal(t, pkgmodel.TargetHealthStateReachable, healthMsgs[0].Observation.State)
}
