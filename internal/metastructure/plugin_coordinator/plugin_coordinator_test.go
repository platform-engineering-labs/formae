package plugin_coordinator

import (
	"testing"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginCoordinator_Init(t *testing.T) {
	// Setup test node - disable networking to avoid port conflicts with other tests
	options := gen.NodeOptions{}
	node, err := ergo.StartNode("test-pc-node@localhost", options)
	require.NoError(t, err)
	defer node.Stop()

	// Spawn PluginCoordinator
	pid, err := node.SpawnRegister("coordinator", NewPluginCoordinator, gen.ProcessOptions{})

	assert.NoError(t, err)
	assert.NotEqual(t, gen.PID{}, pid)
}
