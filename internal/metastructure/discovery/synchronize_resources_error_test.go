// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package discovery

import (
	"errors"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// stubSyncResourcesDatastore is a minimal datastore double for synchronizeResources
// tests. LoadAllTargets returns a fixed list; LoadTarget injects the configured
// error so tests can drive NewChangeset failure without full actor infrastructure.
type stubSyncResourcesDatastore struct {
	datastore.Datastore
	targets     []*pkgmodel.Target
	loadTargetErr error
}

func (s *stubSyncResourcesDatastore) LoadAllTargets() ([]*pkgmodel.Target, error) {
	return s.targets, nil
}

func (s *stubSyncResourcesDatastore) LoadResourcesByStack(_ string) ([]*pkgmodel.Resource, error) {
	return nil, nil
}

func (s *stubSyncResourcesDatastore) LoadTarget(label string) (*pkgmodel.Target, error) {
	if s.loadTargetErr != nil {
		return nil, s.loadTargetErr
	}
	for _, t := range s.targets {
		if t.Label == label {
			return t, nil
		}
	}
	return nil, nil
}

// persistCommandProcess is a gen.Process double that handles all actor calls
// that synchronizeResources issues: StoreNewFormaCommand (before NewChangeset)
// and EnsureChangesetExecutor (after). All other messages are delegated to the
// embedded stubProcess.
type persistCommandProcess struct {
	*stubProcess
}

func (p *persistCommandProcess) Call(_ any, message any) (any, error) {
	switch message.(type) {
	case forma_persister.StoreNewFormaCommand:
		return struct{}{}, nil
	case changeset.EnsureChangesetExecutor:
		return struct{}{}, nil
	default:
		return p.stubProcess.Call(nil, message)
	}
}

// newSyncResourcesData builds a DiscoveryData with a valid plugin schema cache
// for the given namespace and resource type, ready to drive synchronizeResources
// directly without a running actor system.
func newSyncResourcesData(ds datastore.Datastore, namespace, resourceType string) DiscoveryData {
	return DiscoveryData{
		ds:           ds,
		discoveryCfg: &pkgmodel.DiscoveryConfig{Enabled: true, Interval: 20 * time.Second},
		serverCfg:    &pkgmodel.ServerConfig{},
		targets:      map[string]pkgmodel.Target{},
		resourceDescriptors: map[string]plugin.ResourceDescriptor{
			resourceType: {Type: resourceType, Discoverable: true},
		},
		queuedListOperations:          map[string][]ListOperation{},
		outstandingListOperations:     map[string]ListOperation{},
		outstandingSyncCommands:       map[string]ListOperation{},
		recentlyDiscoveredResourceIDs: map[string]struct{}{},
		summary:                       map[string]int{},
		typesWithChildrenQueued:       map[string]struct{}{},
		nativeIDsByCommand:            map[string][]string{},
		pluginInfoCache: map[string]*messages.PluginInfoResponse{
			namespace: {
				Found:     true,
				Namespace: namespace,
				ResourceSchemas: map[string]pkgmodel.Schema{
					resourceType: {},
				},
			},
		},
	}
}

// TestSynchronizeResources_PropagatesNewChangesetError asserts that
// synchronizeResources returns an error rather than proceeding with a zero-value
// changeset when NewChangeset fails. The failure is injected via a datastore
// stub that returns an error from LoadTarget, which triggers the
// synthesizeResolveTargetUpdates path inside NewChangeset.
func TestSynchronizeResources_PropagatesNewChangesetError(t *testing.T) {
	const (
		namespace    = "FakeAWS"
		resourceType = "FakeAWS::S3::Bucket"
		targetLabel  = "us-east-1"
		nativeID     = "my-bucket"
	)

	// The datastore returns a valid target list so GenerateResourceUpdates
	// succeeds, but then errors on LoadTarget so NewChangeset fails inside
	// synthesizeResolveTargetUpdates.
	loadTargetErr := errors.New("datastore read error")
	ds := &stubSyncResourcesDatastore{
		targets: []*pkgmodel.Target{
			{Label: targetLabel, Namespace: namespace},
		},
		loadTargetErr: loadTargetErr,
	}

	data := newSyncResourcesData(ds, namespace, resourceType)
	proc := &persistCommandProcess{stubProcess: &stubProcess{}}

	op := ListOperation{
		ResourceType: resourceType,
		TargetLabel:  targetLabel,
	}
	target := pkgmodel.Target{Label: targetLabel, Namespace: namespace}
	resources := []plugin.ListedResource{
		{NativeID: nativeID, ResourceType: resourceType},
	}

	commandID, err := synchronizeResources(op, namespace, target, resources, data, proc)

	require.Error(t, err, "synchronizeResources must propagate a NewChangeset failure, not silently proceed")
	assert.Empty(t, commandID, "a failed synchronizeResources must return an empty command ID")
	assert.ErrorContains(t, err, "failed to build changeset")
}

// TestSynchronizeResources_ReturnsCommandIDOnSuccess is a companion smoke-test:
// synchronizeResources returns a non-empty command ID when NewChangeset succeeds
// so we can distinguish the error path from a genuine no-op.
func TestSynchronizeResources_ReturnsCommandIDOnSuccess(t *testing.T) {
	const (
		namespace    = "FakeAWS"
		resourceType = "FakeAWS::S3::Bucket"
		targetLabel  = "us-east-1"
		nativeID     = "my-bucket"
	)

	// No loadTargetErr: NewChangeset will succeed (the target has no opaque refs
	// so synthesizeResolveTargetUpdates produces nothing and returns nil error).
	ds := &stubSyncResourcesDatastore{
		targets: []*pkgmodel.Target{
			{Label: targetLabel, Namespace: namespace},
		},
	}

	data := newSyncResourcesData(ds, namespace, resourceType)
	proc := &persistCommandProcess{stubProcess: &stubProcess{}}

	op := ListOperation{
		ResourceType: resourceType,
		TargetLabel:  targetLabel,
	}
	target := pkgmodel.Target{Label: targetLabel, Namespace: namespace}
	resources := []plugin.ListedResource{
		{NativeID: nativeID, ResourceType: resourceType},
	}

	commandID, err := synchronizeResources(op, namespace, target, resources, data, proc)

	// The process stub accepts but ignores the Start message sent to the
	// ChangesetExecutor, so no further errors are expected.
	require.NoError(t, err)
	assert.NotEmpty(t, commandID, "synchronizeResources must return a non-empty command ID on success")
}

// Ensure persistCommandProcess satisfies the gen.Process interface.
var _ gen.Process = (*persistCommandProcess)(nil)
