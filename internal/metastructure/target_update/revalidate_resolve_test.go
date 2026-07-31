// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package target_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// revalidateStubDatastore returns a preset target on LoadTarget, simulating the
// live persisted row an executing Resolve op re-reads. Setting target to nil
// simulates a target that was deleted between changeset build and execute.
type revalidateStubDatastore struct {
	target    *pkgmodel.Target
	loadErr   error
	loadCalls int
}

func (s *revalidateStubDatastore) LoadTarget(_ string) (*pkgmodel.Target, error) {
	s.loadCalls++
	return s.target, s.loadErr
}

// configA is the build-time (snapshot) config the Resolve TU carries; configB is
// the current persisted config a concurrent command wrote before execute. They
// reference distinct resolvables so a rebuild is observable.
var (
	configA = json.RawMessage(`{"secret":{"$ref":"formae://aaa111#/SecretString","$visibility":"Opaque"}}`)
	configB = json.RawMessage(`{"secret":{"$ref":"formae://bbb222#/SecretString","$visibility":"Opaque"}}`)
	// configMixed carries one opaque credential $ref and one non-opaque
	// cross-resource $ref, both current (post-revision-bump).
	configMixed = json.RawMessage(`{"cred":{"$ref":"formae://sec111#/SecretString","$visibility":"Opaque"},"peer":{"$ref":"formae://res222#/Id"}}`)
)

func TestRevalidateResolveTarget_StaleRevision_RebuildsAgainstCurrentConfig(t *testing.T) {
	// The Resolve TU was built from a target at Version=1 carrying configA. A
	// concurrent command bumped the persisted target to Version=2 carrying configB.
	// Re-validation must detect the changed revision and rebuild the TU so
	// resolution runs against the CURRENT config (configB), never the stale one.
	tu := NewResolveTargetUpdate(
		pkgmodel.Target{Label: "consumer", Version: 1, Config: configA},
		[]pkgmodel.FormaeURI{"formae://aaa111#/SecretString"},
	)

	ds := &revalidateStubDatastore{
		target: &pkgmodel.Target{Label: "consumer", Version: 2, Config: configB},
	}

	revised, err := revalidateResolveTarget(tu, ds)
	require.NoError(t, err)

	assert.JSONEq(t, string(configB), string(revised.Target.Config),
		"config must be re-read from the current persisted target")
	assert.Equal(t, 2, revised.Target.Version,
		"snapshot version must advance to the current revision")
	require.Len(t, revised.RemainingResolvables, 1)
	assert.Equal(t, pkgmodel.FormaeURI("formae://bbb222#/SecretString"), revised.RemainingResolvables[0],
		"resolvables must be rebuilt from the current config")
	assert.Equal(t, 1, ds.loadCalls, "a single re-read suffices")
}

func TestRevalidateResolveTarget_OpaqueOnly_StaleRevision_RebuildsOpaqueOnly(t *testing.T) {
	// An opaque-only synthetic Resolve (a cascade-deleted secret-backed target)
	// whose revision advanced under a concurrent command must rebuild against the
	// current config using only its OPAQUE refs. Its non-opaque cross-resource
	// refs point at sources being deleted in the same command; re-including them
	// would attempt to resolve a vanishing value and fail the cascade teardown.
	tu := NewResolveTargetUpdate(
		pkgmodel.Target{Label: "consumer", Version: 1, Config: configA},
		[]pkgmodel.FormaeURI{"formae://aaa111#/SecretString"},
	)
	tu.OpaqueOnly = true

	ds := &revalidateStubDatastore{
		target: &pkgmodel.Target{Label: "consumer", Version: 2, Config: configMixed},
	}

	revised, err := revalidateResolveTarget(tu, ds)
	require.NoError(t, err)

	require.Len(t, revised.RemainingResolvables, 1,
		"opaque-only rebuild must exclude the non-opaque cross-resource ref")
	assert.Equal(t, pkgmodel.FormaeURI("formae://sec111#/SecretString"), revised.RemainingResolvables[0],
		"only the opaque credential ref survives the opaque-only rebuild")
}

func TestRevalidateResolveTarget_FullSelection_StaleRevision_RebuildsAllRefs(t *testing.T) {
	// A default (non-opaque-only) synthetic Resolve rebuilds with EVERY ref in the
	// current config, opaque and non-opaque alike — the OpaqueOnly flag is the only
	// thing that narrows the selection.
	tu := NewResolveTargetUpdate(
		pkgmodel.Target{Label: "consumer", Version: 1, Config: configA},
		[]pkgmodel.FormaeURI{"formae://aaa111#/SecretString"},
	)

	ds := &revalidateStubDatastore{
		target: &pkgmodel.Target{Label: "consumer", Version: 2, Config: configMixed},
	}

	revised, err := revalidateResolveTarget(tu, ds)
	require.NoError(t, err)

	require.Len(t, revised.RemainingResolvables, 2,
		"the default selection rebuilds with both the opaque and non-opaque refs")
}

func TestRevalidateResolveTarget_UnchangedRevision_NoRebuild(t *testing.T) {
	// When the persisted revision matches the snapshot, re-validation is a no-op:
	// the TU keeps its snapshot config and resolvables untouched.
	tu := NewResolveTargetUpdate(
		pkgmodel.Target{Label: "consumer", Version: 5, Config: configA},
		[]pkgmodel.FormaeURI{"formae://aaa111#/SecretString"},
	)

	ds := &revalidateStubDatastore{
		target: &pkgmodel.Target{Label: "consumer", Version: 5, Config: configB},
	}

	revised, err := revalidateResolveTarget(tu, ds)
	require.NoError(t, err)

	assert.JSONEq(t, string(configA), string(revised.Target.Config),
		"unchanged revision must keep the snapshot config, not re-read")
	assert.Equal(t, 5, revised.Target.Version)
	require.Len(t, revised.RemainingResolvables, 1)
	assert.Equal(t, pkgmodel.FormaeURI("formae://aaa111#/SecretString"), revised.RemainingResolvables[0],
		"unchanged revision must keep the snapshot resolvables")
}

func TestRevalidateResolveTarget_TargetDeleted_SurfacesError(t *testing.T) {
	// If the target the Resolve op targets was deleted between build and execute,
	// re-validation must surface a clear error rather than resolve a phantom.
	tu := NewResolveTargetUpdate(
		pkgmodel.Target{Label: "consumer", Version: 1, Config: configA},
		[]pkgmodel.FormaeURI{"formae://aaa111#/SecretString"},
	)

	ds := &revalidateStubDatastore{target: nil}

	_, err := revalidateResolveTarget(tu, ds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consumer",
		"error must name the target that no longer exists")
}

func TestRevalidateResolveTarget_NonResolveOp_Unchanged(t *testing.T) {
	// Re-validation only applies to synthetic Resolve ops. A real create/update
	// TU is returned untouched and never triggers a re-read.
	tu := TargetUpdate{
		Target:               pkgmodel.Target{Label: "consumer", Version: 1, Config: configA},
		Operation:            TargetOperationUpdate,
		RemainingResolvables: []pkgmodel.FormaeURI{"formae://aaa111#/SecretString"},
	}

	ds := &revalidateStubDatastore{
		target: &pkgmodel.Target{Label: "consumer", Version: 99, Config: configB},
	}

	revised, err := revalidateResolveTarget(tu, ds)
	require.NoError(t, err)
	assert.JSONEq(t, string(configA), string(revised.Target.Config))
	assert.Equal(t, 0, ds.loadCalls, "non-Resolve ops must not re-read")
}
