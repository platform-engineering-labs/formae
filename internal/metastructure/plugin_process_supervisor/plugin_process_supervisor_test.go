package plugin_process_supervisor

import (
	"testing"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginProcessSupervisor_Init(t *testing.T) {
	// Setup test node
	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	node, err := ergo.StartNode("test-node@localhost", options)
	require.NoError(t, err)
	defer node.Stop()

	// Spawn PluginProcessSupervisor
	pid, err := node.SpawnRegister("supervisor", NewPluginProcessSupervisor, gen.ProcessOptions{})

	assert.NoError(t, err)
	assert.NotEqual(t, gen.PID{}, pid)
}
