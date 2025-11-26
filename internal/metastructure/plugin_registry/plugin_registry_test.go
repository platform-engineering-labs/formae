package plugin_registry

import (
	"testing"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginRegistry_Init(t *testing.T) {
	// Setup test node
	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	node, err := ergo.StartNode("test-node@localhost", options)
	require.NoError(t, err)
	defer node.Stop()

	// Spawn PluginRegistry
	pid, err := node.SpawnRegister("registry", NewPluginRegistry, gen.ProcessOptions{})

	assert.NoError(t, err)
	assert.NotEqual(t, gen.PID{}, pid)
}
