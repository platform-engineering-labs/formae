// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"context"
	"testing"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func TestConstructParentResourceDescriptors(t *testing.T) {
	supportedResources := []plugin.ResourceDescriptor{
		{
			Type:         "FakeAWS::S3::Bucket",
			Discoverable: true,
		},
		{
			Type:         "FakeAWS::EC2::VPC",
			Discoverable: true,
		},
		{
			Type:         "FakeAWS::EC2::VPCCidrBlock",
			Discoverable: true,
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"FakeAWS::EC2::VPC": {{ParentProperty: "Id", ListProperty: "VpcId", QueryPath: "$.Id"}},
			},
		},
		{
			Type:         "FakeAWS::EC2::LegacyVPCCidrBlock",
			Discoverable: false,
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"FakeAWS::EC2::VPC": {{ParentProperty: "Id", ListProperty: "VpcId", QueryPath: "$.Id"}},
			},
		},
		{
			Type:         "FakeAWS::EC2::Instance",
			Discoverable: false,
		},
	}

	descriptors := constructParentResourceDescriptors(supportedResources)

	assert.Len(t, descriptors, 2)
	assert.Contains(t, descriptors, "FakeAWS::EC2::VPC")
	assert.Contains(t, descriptors, "FakeAWS::S3::Bucket")

	assert.Len(t, descriptors["FakeAWS::EC2::VPC"].NestedResourcesWithMappingProperties, 1)
	// There should only be one entry for the VPC CIDR Block
	var mappingProperties []plugin.ListParameter
	var resourceType string
	for t, props := range descriptors["FakeAWS::EC2::VPC"].NestedResourcesWithMappingProperties {
		mappingProperties = props
		resourceType = t.ResourceType
	}
	assert.Equal(t, "FakeAWS::EC2::VPCCidrBlock", resourceType)
	assert.Equal(t, []plugin.ListParameter{{ParentProperty: "Id", ListProperty: "VpcId", QueryPath: "$.Id"}}, mappingProperties)
}

func TestTriggerOnce_StartsDiscovery_WhenIdle(t *testing.T) {
	ds, err := newTestDatastore()
	require.NoError(t, err)

	pm := plugin.NewManager()
	pm.Load()

	actor, err := unit.Spawn(t, NewDiscovery, unit.WithEnv(map[gen.Env]any{
		"PluginManager":   pm,
		"Datastore":       ds,
		"ServerConfig":    pkgmodel.ServerConfig{},
		"DiscoveryConfig": pkgmodel.DiscoveryConfig{Enabled: false},
	}))
	require.NoError(t, err)

	sender := gen.PID{Node: "test", ID: 100}
	result := actor.Call(sender, DiscoverSync{Once: true})
	require.NoError(t, result.Error)
	require.NotNil(t, result.Response)
	discoverResult, ok := result.Response.(*DiscoverSyncResult)
	require.True(t, ok)
	assert.True(t, discoverResult.Started)
	assert.False(t, discoverResult.AlreadyRunning)
}

func TestTriggerOnce_ReturnsAlreadyRunning_WhenDiscovering(t *testing.T) {
	ds, err := newTestDatastore()
	require.NoError(t, err)

	pm := plugin.NewManager()
	pm.Load()

	actor, err := unit.Spawn(t, NewDiscovery, unit.WithEnv(map[gen.Env]any{
		"PluginManager":   pm,
		"Datastore":       ds,
		"ServerConfig":    pkgmodel.ServerConfig{},
		"DiscoveryConfig": pkgmodel.DiscoveryConfig{Enabled: false},
	}))
	require.NoError(t, err)

	sender := gen.PID{Node: "test", ID: 100}
	firstResult := actor.Call(sender, DiscoverSync{Once: true})
	require.NoError(t, firstResult.Error)
	require.NotNil(t, firstResult.Response)
	firstDiscoverResult, ok := firstResult.Response.(*DiscoverSyncResult)
	require.True(t, ok)
	assert.True(t, firstDiscoverResult.Started)

	result := actor.Call(sender, DiscoverSync{Once: true})
	require.NoError(t, result.Error)
	require.NotNil(t, result.Response)
	discoverResult, ok := result.Response.(*DiscoverSyncResult)
	require.True(t, ok)

	if discoverResult.AlreadyRunning {
		assert.False(t, discoverResult.Started)
		assert.True(t, discoverResult.AlreadyRunning)
	} else {
		assert.True(t, discoverResult.Started)
	}
}

func newTestDatastore() (datastore.Datastore, error) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite: pkgmodel.SqliteConfig{
			FilePath: ":memory:",
		},
	}

	return datastore.NewDatastoreSQLite(context.Background(), cfg, "test")
}
