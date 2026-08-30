// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"fmt"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// countingCallProcess is a gen.Process double that records how many times a
// plugin operator spawn was requested, so a test can assert that the plugin
// was never reached at all.
type countingCallProcess struct {
	gen.Process
	calls int
}

func (p *countingCallProcess) Log() gen.Log   { return stubUpdaterLog{} }
func (p *countingCallProcess) Node() gen.Node { return stubUpdaterNode{} }
func (p *countingCallProcess) PID() gen.PID   { return gen.PID{Node: "test-node", ID: 1} }
func (p *countingCallProcess) Call(_ any, _ any) (any, error) {
	p.calls++
	return nil, fmt.Errorf("no plugin coordinator in this test")
}

func readableResource() pkgmodel.Resource {
	return pkgmodel.Resource{
		Label: "db", Type: "FakeAWS::SecretsManager::Secret",
		Stack: "test-stack", Target: "test-target", NativeID: "native-1",
		Ksuid: "2abcDEFghiJKLmnoPQRstuVWxyz",
	}
}

// A read authenticates with the target's configuration, so a credential bound
// to a generator whose value has not been drawn stops the read before the
// plugin is involved at all: the envelope names a value, and is never that
// value.
func TestReadResourceViaPlugin_UndrawnGeneratorConfigNeverReachesAPlugin(t *testing.T) {
	proc := &countingCallProcess{}
	cfg := json.RawMessage(`{
		"Region": "us-east-1",
		"Token": {
			"$gen":        true,
			"$generator":  "2abcDEFghiJKLmnoPQRstuVWxyz",
			"$output":     "value",
			"$visibility": "Opaque"
		}
	}`)

	_, err := ReadResourceViaPlugin(proc, readableResource(), cfg)

	assert.Zero(t, proc.calls, "the read must be refused before a plugin operator is even spawned")
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "has not been drawn",
			"the refusal must say why the read cannot be made")
	}
}

// An ordinary target config still reaches the dispatch, so the refusal above is
// not the helper declining every read.
func TestReadResourceViaPlugin_OrdinaryConfigStillDispatches(t *testing.T) {
	proc := &countingCallProcess{}

	_, err := ReadResourceViaPlugin(proc, readableResource(), json.RawMessage(`{"Region":"us-east-1"}`))

	require.Error(t, err, "this double has no plugin coordinator, so the spawn fails")
	assert.Equal(t, 1, proc.calls, "an ordinary config must get as far as the spawn")
}
