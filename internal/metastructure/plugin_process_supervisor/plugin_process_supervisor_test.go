package plugin_process_supervisor

//import (
//	"testing"
//
//	"ergo.services/ergo"
//	"ergo.services/ergo/gen"
//	"github.com/platform-engineering-labs/formae/pkg/plugin"
//	"github.com/stretchr/testify/assert"
//	"github.com/stretchr/testify/require"
//)
//
//func TestPluginProcessSupervisor_Init(t *testing.T) {
//	// Setup test node - disable networking to avoid port conflicts with other tests
//	options := gen.NodeOptions{}
//	node, err := ergo.StartNode("test-pps-node@localhost", options)
//	require.NoError(t, err)
//	defer node.Stop()
//
//	// Create a PluginManager (with no plugin paths for testing)
//	pluginManager := plugin.NewManager()
//
//	// Spawn PluginProcessSupervisor with PluginManager in environment
//	processOptions := gen.ProcessOptions{
//		Env: map[gen.Env]any{
//			"PluginManager": pluginManager,
//		},
//	}
//	pid, err := node.SpawnRegister("supervisor", NewPluginProcessSupervisor, processOptions)
//
//	assert.NoError(t, err)
//	assert.NotEqual(t, gen.PID{}, pid)
//}
