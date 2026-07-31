// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package target_update

import (
	"encoding/json"
	"sync"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stubTargetUpdaterLog swallows all log output.
type stubTargetUpdaterLog struct{ gen.Log }

func (stubTargetUpdaterLog) Trace(string, ...any)   {}
func (stubTargetUpdaterLog) Debug(string, ...any)   {}
func (stubTargetUpdaterLog) Info(string, ...any)    {}
func (stubTargetUpdaterLog) Warning(string, ...any) {}
func (stubTargetUpdaterLog) Error(string, ...any)   {}
func (stubTargetUpdaterLog) Panic(string, ...any)   {}

// stubTargetUpdaterProcess is a gen.Process double for onTargetUpdaterStateChange
// tests. It records every Send call so tests can inspect the messages sent to the
// requester.
type stubTargetUpdaterProcess struct {
	gen.Process

	mu    sync.Mutex
	sends []any
}

func (p *stubTargetUpdaterProcess) Log() gen.Log { return stubTargetUpdaterLog{} }
func (p *stubTargetUpdaterProcess) PID() gen.PID { return gen.PID{Node: "test-node", ID: 1} }
func (p *stubTargetUpdaterProcess) Send(_ any, msg any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sends = append(p.sends, msg)
	return nil
}

func (p *stubTargetUpdaterProcess) sentFinishedMessages() []TargetUpdateFinished {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []TargetUpdateFinished
	for _, s := range p.sends {
		if m, ok := s.(TargetUpdateFinished); ok {
			out = append(out, m)
		}
	}
	return out
}

// TestOnTargetUpdaterStateChange_SuccessCarriesResolvedConfig asserts that a
// successful Resolve op sends TargetUpdateFinished with the resolved config
// populated so downstream resource ops can propagate it.
func TestOnTargetUpdaterStateChange_SuccessCarriesResolvedConfig(t *testing.T) {
	resolvedConfig := json.RawMessage(`{"endpoint":"https://cluster.example.com","token":"resolved-token"}`)

	data := TargetUpdaterData{
		targetUpdate: TargetUpdate{
			Target: pkgmodel.Target{
				Label:  "my-cluster",
				Config: resolvedConfig,
			},
		},
		requestedBy: gen.PID{Node: "test-node", ID: 99},
	}

	proc := &stubTargetUpdaterProcess{}
	_, _, err := onTargetUpdaterStateChange(StateResolving, StateFinishedSuccessfully, data, proc)
	require.NoError(t, err)

	finished := proc.sentFinishedMessages()
	require.Len(t, finished, 1, "exactly one TargetUpdateFinished must be sent on success")
	assert.Equal(t, TargetUpdateStateSuccess, finished[0].State)
	assert.Equal(t, resolvedConfig, finished[0].ResolvedConfig,
		"ResolvedConfig must be populated on success so dependent resource ops receive credentials")
}

// TestOnTargetUpdaterStateChange_FailureOmitsResolvedConfig asserts that a
// failed Resolve op sends TargetUpdateFinished with a nil ResolvedConfig.
// On failure the config may be partially resolved (some $value placeholders
// still present); sending it to downstream consumers would be misleading and
// could expose intermediate plaintext unnecessarily.
func TestOnTargetUpdaterStateChange_FailureOmitsResolvedConfig(t *testing.T) {
	partialConfig := json.RawMessage(`{"endpoint":"https://cluster.example.com","token":{"$ref":"formae://abc#/Token"}}`)

	data := TargetUpdaterData{
		targetUpdate: TargetUpdate{
			Target: pkgmodel.Target{
				Label:  "my-cluster",
				Config: partialConfig,
			},
		},
		requestedBy: gen.PID{Node: "test-node", ID: 99},
	}

	proc := &stubTargetUpdaterProcess{}
	_, _, err := onTargetUpdaterStateChange(StateResolving, StateFinishedWithError, data, proc)
	require.NoError(t, err)

	finished := proc.sentFinishedMessages()
	require.Len(t, finished, 1, "exactly one TargetUpdateFinished must be sent on failure")
	assert.Equal(t, TargetUpdateStateFailed, finished[0].State)
	assert.Nil(t, finished[0].ResolvedConfig,
		"ResolvedConfig must be nil on failure to avoid propagating partial or plaintext credentials")
}
