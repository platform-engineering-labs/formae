// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"fmt"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// stubLog is a gen.Log that swallows all log output.
type stubLog struct {
	gen.Log
}

func (stubLog) Trace(string, ...any)   {}
func (stubLog) Debug(string, ...any)   {}
func (stubLog) Info(string, ...any)    {}
func (stubLog) Warning(string, ...any) {}
func (stubLog) Error(string, ...any)   {}
func (stubLog) Panic(string, ...any)   {}

// stubNode is a gen.Node that only knows its name.
type stubNode struct {
	gen.Node
}

func (stubNode) Name() gen.Atom { return gen.Atom("test-node") }

// stubProcess is a hand-rolled gen.Process test double that stands in for the
// RateLimiter and PluginCoordinator collaborators that the discovery scan loop
// calls. It records every PluginOperator spawn requested and delegates the spawn
// outcome to a configurable responder, so individual tests can make a given
// spawn time out or fail.
type stubProcess struct {
	gen.Process

	// spawn decides the outcome of the nth (1-based) PluginOperator spawn.
	// nil means every spawn succeeds.
	spawn func(n int, req messages.SpawnPluginOperator) (any, error)

	spawnRequests []messages.SpawnPluginOperator

	// pluginInfo, when set, answers GetPluginInfo calls so discover() can be
	// driven end-to-end without a PluginCoordinator.
	pluginInfo *messages.PluginInfoResponse

	// sendAfterMsgs records every message scheduled via SendAfter, so tests can
	// assert on the periodic Discover re-arm behavior.
	sendAfterMsgs []any
}

func (p *stubProcess) Log() gen.Log   { return stubLog{} }
func (p *stubProcess) Node() gen.Node { return stubNode{} }
func (p *stubProcess) PID() gen.PID   { return gen.PID{Node: "test-node", ID: 1} }

func (p *stubProcess) Send(_ any, _ any) error { return nil }

func (p *stubProcess) SendAfter(_ any, message any, _ time.Duration) (gen.CancelFunc, error) {
	p.sendAfterMsgs = append(p.sendAfterMsgs, message)
	return func() bool { return true }, nil
}

// scheduledDiscoverCount returns how many periodic Discover messages have been
// scheduled via SendAfter.
func (p *stubProcess) scheduledDiscoverCount() int {
	n := 0
	for _, m := range p.sendAfterMsgs {
		if _, ok := m.(Discover); ok {
			n++
		}
	}
	return n
}

func (p *stubProcess) Call(_ any, message any) (any, error) {
	switch m := message.(type) {
	case changeset.RequestTokens:
		// Grant every token requested so the whole queue is scanned in one pass.
		return changeset.TokensGranted{N: m.N}, nil
	case messages.GetPluginInfo:
		if p.pluginInfo != nil {
			return *p.pluginInfo, nil
		}
		return nil, fmt.Errorf("stubProcess: no pluginInfo configured")
	case messages.SpawnPluginOperator:
		p.spawnRequests = append(p.spawnRequests, m)
		if p.spawn != nil {
			return p.spawn(len(p.spawnRequests), m)
		}
		return messages.SpawnPluginOperatorResult{}, nil
	default:
		return nil, fmt.Errorf("stubProcess: unexpected Call message %T", message)
	}
}

// newScanData builds a DiscoveryData with the given resource types queued for a
// single discoverable target, ready to be driven through resumeScanning.
func newScanData(resourceTypes ...string) DiscoveryData {
	const namespace = "FakeAWS"
	const targetLabel = "us-east-1"

	data := DiscoveryData{
		targets: map[string]pkgmodel.Target{
			targetLabel: {Label: targetLabel, Namespace: namespace, Discoverable: true},
		},
		resourceDescriptors:       map[string]plugin.ResourceDescriptor{},
		queuedListOperations:      map[string][]ListOperation{},
		outstandingListOperations: map[string]ListOperation{},
		outstandingSyncCommands:   map[string]ListOperation{},
		summary:                   map[string]int{},
	}

	for _, rt := range resourceTypes {
		data.resourceDescriptors[rt] = plugin.ResourceDescriptor{Type: rt, Discoverable: true}
		data.queuedListOperations[namespace] = append(data.queuedListOperations[namespace], ListOperation{
			ResourceType: rt,
			TargetLabel:  targetLabel,
		})
	}

	return data
}

// A spawn timeout for one resource type is transient back-pressure: discovery
// must skip that resource type for the cycle and keep scanning the rest, not
// crash the actor.
func TestResumeScanning_SpawnTimeoutSkipsResourceTypeAndContinues(t *testing.T) {
	data := newScanData("FakeAWS::S3::Bucket", "FakeAWS::EC2::VPC", "FakeAWS::EC2::Instance")
	proc := &stubProcess{spawn: func(n int, _ messages.SpawnPluginOperator) (any, error) {
		if n == 2 { // the second resource type times out
			return nil, gen.ErrTimeout
		}
		return messages.SpawnPluginOperatorResult{}, nil
	}}

	_, _, _, err := resumeScanning(gen.PID{}, StateDiscovering, data, ResumeScanning{}, proc)

	require.NotEqual(t, gen.TerminateReasonPanic, err, "spawn timeout must not crash the Discovery actor")
	assert.NoError(t, err)
	assert.Len(t, proc.spawnRequests, 3, "all three resource types should be attempted; the loop must continue past the timed-out one")
}

// When every spawn in a cycle times out, there is no outstanding listing to
// drive completion via a Listing callback. Discovery must still finish the cycle
// (return to Idle) so the next scheduled run is queued — otherwise it hangs in
// StateDiscovering forever.
func TestResumeScanning_AllSpawnsTimeOutStillCompletesCycle(t *testing.T) {
	data := newScanData("FakeAWS::S3::Bucket", "FakeAWS::EC2::VPC", "FakeAWS::EC2::Instance")
	proc := &stubProcess{spawn: func(int, messages.SpawnPluginOperator) (any, error) {
		return nil, gen.ErrTimeout // every spawn times out
	}}

	nextState, _, _, err := resumeScanning(gen.PID{}, StateDiscovering, data, ResumeScanning{}, proc)

	require.NotEqual(t, gen.TerminateReasonPanic, err, "consecutive spawn timeouts must not crash the Discovery actor")
	assert.NoError(t, err)
	assert.Len(t, proc.spawnRequests, 3, "all resource types should be attempted despite every spawn timing out")
	assert.Equal(t, StateIdle, nextState, "a fully-skipped cycle must return to Idle so the next discovery run is scheduled")
}

// A non-timeout spawn failure for one resource type is likewise skipped, not
// fatal: the actor logs it and keeps scanning.
func TestResumeScanning_NonTimeoutSpawnErrorSkipsResourceTypeAndContinues(t *testing.T) {
	data := newScanData("FakeAWS::S3::Bucket", "FakeAWS::EC2::VPC", "FakeAWS::EC2::Instance")
	proc := &stubProcess{spawn: func(n int, _ messages.SpawnPluginOperator) (any, error) {
		if n == 2 {
			return messages.SpawnPluginOperatorResult{Error: "plugin not found: FakeAWS"}, nil
		}
		return messages.SpawnPluginOperatorResult{}, nil
	}}

	_, _, _, err := resumeScanning(gen.PID{}, StateDiscovering, data, ResumeScanning{}, proc)

	require.NotEqual(t, gen.TerminateReasonPanic, err, "a non-timeout scan error must not crash the Discovery actor")
	assert.NoError(t, err)
	assert.Len(t, proc.spawnRequests, 3, "the loop must continue past the failed resource type")
}
