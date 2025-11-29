package plugin_process_supervisor

import (
	"fmt"
	"regexp"
	"strings"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/meta"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

type PluginProcessSupervisor struct {
	act.Actor

	plugins map[string]*PluginInfo
}

type PluginInfo struct {
	namespace     string
	binaryPath    string
	metaPortAlias gen.Alias
	nodeName      gen.Atom
	healthy       bool
}

// NewPluginProcessSupervisor creates a new PluginProcessSupervisor actor
func NewPluginProcessSupervisor() gen.ProcessBehavior {
	return &PluginProcessSupervisor{}
}

func (p *PluginProcessSupervisor) Init(args ...any) error {
	p.plugins = make(map[string]*PluginInfo)
	p.Log().Info("PluginProcessSupervisor started")

	// Fetch PluginManager from environment
	pluginManagerVal, ok := p.Env("PluginManager")
	if !ok {
		p.Log().Error("PluginProcessSupervisor: missing 'PluginManager' in environment")
		return fmt.Errorf("plugin_process_supervisor: missing 'PluginManager' in environment")
	}

	pluginManager, ok := pluginManagerVal.(*plugin.Manager)
	if !ok {
		p.Log().Error("PluginProcessSupervisor: PluginManager has wrong type")
		return fmt.Errorf("plugin_process_supervisor: PluginManager has wrong type")
	}

	// Get external resource plugins from PluginManager
	externalPlugins := pluginManager.ListExternalResourcePlugins()
	p.Log().Info("Discovered %d external resource plugins", len(externalPlugins))

	// Store plugin info and spawn each plugin
	for _, pluginInfo := range externalPlugins {
		namespace := pluginInfo.Namespace
		version := pluginInfo.Version
		binaryPath := pluginInfo.BinaryPath

		p.plugins[namespace] = &PluginInfo{
			namespace:  namespace,
			binaryPath: binaryPath,
			healthy:    false,
		}

		p.Log().Info("Discovered plugin: namespace=%s version=%s path=%s", namespace, version, binaryPath)

		// Spawn the plugin
		err := p.spawnPlugin(namespace, p.plugins[namespace])
		if err != nil {
			p.Log().Error("Failed to spawn plugin", "namespace", namespace, "error", err)
			// Continue with other plugins even if one fails
			continue
		}
	}

	p.Log().Info("PluginProcessSupervisor initialized with %d plugins", len(p.plugins))
	return nil
}

func (p *PluginProcessSupervisor) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	// TODO: Implement message handling in later tasks
	return nil, nil
}

// logPluginOutput parses Ergo's log format and logs with appropriate level
// Format: "timestamp [level] rest_of_message" e.g. "1764458575429677244 [info] <79F4473F.0.1004>: message"
var pluginLogRegex = regexp.MustCompile(`^\d+\s+\[(trace|debug|info|warning|error)\]\s+(.*)$`)

func (p *PluginProcessSupervisor) logPluginOutput(namespace, output string) {
	if output == "" {
		return
	}

	matches := pluginLogRegex.FindStringSubmatch(output)
	if matches == nil {
		// No level found, log as info
		p.Log().Info("[%s] %s", namespace, output)
		return
	}

	level := matches[1]
	message := matches[2]
	formattedMsg := fmt.Sprintf("[%s] %s", namespace, message)

	switch level {
	case "trace":
		p.Log().Trace(formattedMsg)
	case "debug":
		p.Log().Debug(formattedMsg)
	case "info":
		p.Log().Info(formattedMsg)
	case "warning":
		p.Log().Warning(formattedMsg)
	case "error":
		p.Log().Error(formattedMsg)
	default:
		p.Log().Info(formattedMsg)
	}
}

func (p *PluginProcessSupervisor) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case meta.MessagePortText:
		// Plugin text output - parse level and log appropriately
		output := strings.TrimSpace(msg.Text)
		p.logPluginOutput(msg.Tag, output)

	case meta.MessagePortData:
		// Plugin binary data - parse level and log appropriately
		output := strings.TrimSpace(string(msg.Data))
		p.logPluginOutput(msg.Tag, output)

	case meta.MessagePortError:
		// Plugin error output
		p.Log().Error("Plugin error", "namespace", msg.Tag, "error", msg.Error)

	case meta.MessagePortTerminate:
		// Plugin process terminated
		p.Log().Error("Plugin terminated", "namespace", msg.Tag)

		// Mark plugin as unhealthy
		if pluginInfo, ok := p.plugins[msg.Tag]; ok {
			pluginInfo.healthy = false
			// TODO: Restart plugin
			p.Log().Info("Plugin restart not yet implemented", "namespace", msg.Tag)
		}
	}

	return nil
}

// spawnPlugin spawns a plugin process via meta.Port
func (p *PluginProcessSupervisor) spawnPlugin(namespace string, pluginInfo *PluginInfo) error {
	// Get server config from environment
	serverConfigVal, ok := p.Env("ServerConfig")
	if !ok {
		return fmt.Errorf("ServerConfig not found in environment")
	}

	serverConfig, ok := serverConfigVal.(pkgmodel.ServerConfig)
	if !ok {
		return fmt.Errorf("ServerConfig has wrong type")
	}

	// Create node name for the plugin
	nodeName := fmt.Sprintf("%s-plugin@%s", strings.ToLower(namespace), serverConfig.Hostname)
	agentNode := fmt.Sprintf("%s@%s", serverConfig.Nodename, serverConfig.Hostname)

	// Configure meta.Port options
	portOptions := meta.PortOptions{
		Cmd:         pluginInfo.binaryPath,
		EnableEnvOS: true, // Inherit OS environment (PATH, etc.) so plugins can find dependencies like pkl
		Env: map[gen.Env]string{
			gen.Env("FORMAE_AGENT_NODE"):     agentNode,
			gen.Env("FORMAE_PLUGIN_NODE"):    nodeName,
			gen.Env("FORMAE_NETWORK_COOKIE"): serverConfig.Secret,
		},
		Tag:     namespace,
		Process: gen.Atom("PluginProcessSupervisor"), // Send messages to the PluginProcessSupervisor actor
	}

	// Create meta.Port
	metaport, err := meta.CreatePort(portOptions)
	if err != nil {
		return fmt.Errorf("failed to create meta.Port: %w", err)
	}

	// Spawn the plugin process
	alias, err := p.SpawnMeta(metaport, gen.MetaOptions{})
	if err != nil {
		return fmt.Errorf("failed to spawn plugin via meta.Port: %w", err)
	}

	// Update plugin info
	pluginInfo.metaPortAlias = alias
	pluginInfo.nodeName = gen.Atom(nodeName)
	pluginInfo.healthy = true

	p.Log().Info("Spawned plugin", "namespace", namespace, "node", nodeName, "alias", alias)
	return nil
}
