// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_process_supervisor

import (
	"fmt"
	"net/rpc"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/meta"
	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/auth"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

const MaxPluginRestarts = 5

// authPluginInitComplete is sent from the completeAuthPluginInit goroutine
// back to the actor loop so that entry.healthy is only written inside the
// actor's message-processing loop, avoiding a data race.
type authPluginInitComplete struct{}

type PluginProcessSupervisor struct {
	act.Actor

	plugins      map[string]*PluginInfo
	authPlugin   *authPluginEntry
	shuttingDown bool
}

// authPluginEntry wraps an AuthPluginHandle with supervisor-specific tracking state.
type authPluginEntry struct {
	handle        *auth.AuthPluginHandle
	metaPortAlias gen.Alias
	conn          *auth.MetaPortConn // held between spawn and init completion
	healthy       bool
	initStarted   bool // true once the ready signal has triggered completeAuthPluginInit
	restartCount  int
	lastCrashTime time.Time
}

type PluginInfo struct {
	name          string
	namespace     string
	binaryPath    string
	metaPortAlias gen.Alias
	nodeName      gen.Atom
	healthy       bool
	restartCount  int
	lastCrashTime time.Time
}

// NewPluginProcessSupervisor creates a new PluginProcessSupervisor actor
func NewPluginProcessSupervisor() gen.ProcessBehavior {
	return &PluginProcessSupervisor{}
}

func (p *PluginProcessSupervisor) Init(args ...any) error {
	p.plugins = make(map[string]*PluginInfo)
	p.Log().Debug("PluginProcessSupervisor started")

	// Get external resource plugins from environment
	var externalPlugins []plugin.ResourcePluginInfo
	if val, ok := p.Env("ExternalResourcePlugins"); ok {
		if plugins, ok := val.([]plugin.ResourcePluginInfo); ok {
			externalPlugins = plugins
		}
	}
	p.Log().Debug("Discovered %d resource plugins", len(externalPlugins))

	// Store plugin info and spawn each plugin
	for _, pluginInfo := range externalPlugins {
		name := pluginInfo.Name
		namespace := pluginInfo.Namespace
		version := pluginInfo.Version
		binaryPath := pluginInfo.BinaryPath

		p.plugins[namespace] = &PluginInfo{
			name:       name,
			namespace:  namespace,
			binaryPath: binaryPath,
			healthy:    false,
		}

		p.Log().Debug("Discovered plugin: name=%s namespace=%s version=%s path=%s", name, namespace, version, binaryPath)

		// Spawn the plugin
		err := p.spawnResourcePlugin(namespace, p.plugins[namespace])
		if err != nil {
			p.Log().Error("Failed to spawn plugin", "namespace", namespace, "error", err)
			// Continue with other plugins even if one fails
			continue
		}
	}

	// Spawn auth plugin if configured
	if handleVal, ok := p.Env("AuthPluginHandle"); ok {
		if handle, ok := handleVal.(*auth.AuthPluginHandle); ok {
			tag := auth.AuthTag(handle.Name())
			entry := &authPluginEntry{handle: handle}
			p.authPlugin = entry

			p.Log().Debug("Spawning auth plugin", "name", handle.Name(), "tag", tag)

			if err := p.spawnAuthPluginProcess(tag, entry); err != nil {
				p.Log().Error("Failed to spawn auth plugin process", "name", handle.Name(), "error", err)
			}
			// RPC Init is deferred: the plugin writes a ready signal to stdout
			// when it starts, which arrives as MessagePortData in HandleMessage.
			// That triggers completeAuthPluginInit() in a goroutine.
		}
	}

	authConfigured := p.authPlugin != nil
	p.Log().Debug("PluginProcessSupervisor initialized with %d resource plugins, auth=%v",
		len(p.plugins), authConfigured)
	return nil
}

// getPluginName returns the plugin name for a given namespace (tag).
// Falls back to the namespace if the plugin is not found.
func (p *PluginProcessSupervisor) getPluginName(namespace string) string {
	if info, ok := p.plugins[namespace]; ok {
		return info.name
	}
	return namespace
}

// logPluginOutput parses Ergo's log format and logs with appropriate level
// Format: "timestamp [level] rest_of_message" e.g. "1764458575429677244 [info] <79F4473F.0.1004>: message"
var pluginLogRegex = regexp.MustCompile(`^\d+\s+\[(trace|debug|info|warning|error)\]\s+(.*)$`)

func (p *PluginProcessSupervisor) logPluginOutput(pluginName, output string) {
	if output == "" {
		return
	}

	matches := pluginLogRegex.FindStringSubmatch(output)
	if matches == nil {
		// No level found, log as info
		p.Log().Info("[%s] %s", pluginName, output)
		return
	}

	level := matches[1]
	message := matches[2]
	formattedMsg := fmt.Sprintf("[%s] %s", pluginName, message)

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
		// Text output (stderr for auth plugins, stdout for resource plugins)
		output := strings.TrimSpace(msg.Text)
		if auth.IsAuthTag(msg.Tag) {
			p.logPluginOutput(auth.AuthTagName(msg.Tag), output)
		} else {
			pluginName := p.getPluginName(msg.Tag)
			p.logPluginOutput(pluginName, output)
		}

	case meta.MessagePortData:
		if auth.IsAuthTag(msg.Tag) && p.authPlugin != nil {
			if !p.authPlugin.initStarted {
				// First binary data from the auth plugin is the ready signal.
				// Discard it (not RPC data) and start the RPC handshake in a
				// goroutine. By the time HandleMessage runs, the actor and
				// meta.Port are fully operational, so SendAlias works.
				p.authPlugin.initStarted = true
				go p.completeAuthPluginInit()
				return nil
			}
			// Subsequent binary data is RPC traffic — feed to MetaPortConn.
			// Use entry.conn directly because handle.conn is nil until
			// Connect() is called after the RPC handshake completes.
			if p.authPlugin.conn != nil {
				p.authPlugin.conn.Feed(msg.Data)
			}
		} else if !auth.IsAuthTag(msg.Tag) {
			// Resource plugin binary data - log it
			output := strings.TrimSpace(string(msg.Data))
			pluginName := p.getPluginName(msg.Tag)
			p.logPluginOutput(pluginName, output)
		}

	case meta.MessagePortError:
		// Plugin error output
		p.Log().Error("Plugin error", "tag", msg.Tag, "error", msg.Error)

	case meta.MessagePortTerminate:
		if auth.IsAuthTag(msg.Tag) {
			p.handleAuthPluginTerminate(msg.Tag)
		} else {
			p.handleResourcePluginTerminate(msg.Tag)
		}

	case authPluginInitComplete:
		if p.authPlugin != nil {
			p.authPlugin.healthy = true
			p.Log().Info("Auth plugin initialized", "name", p.authPlugin.handle.Name())
		}

	}

	return nil
}

// handleResourcePluginTerminate handles termination of a resource plugin process.
func (p *PluginProcessSupervisor) handleResourcePluginTerminate(tag string) {
	if p.shuttingDown {
		p.Log().Debug("Resource plugin stopped during shutdown", "namespace", tag)
		return
	}
	p.Log().Error("Resource plugin terminated unexpectedly", "namespace", tag)

	pluginInfo, ok := p.plugins[tag]
	if !ok {
		return
	}

	pluginInfo.healthy = false
	pluginInfo.lastCrashTime = time.Now()

	// Send UnregisterPlugin message to PluginCoordinator first
	err := p.Send(actornames.PluginCoordinator, messages.UnregisterPlugin{
		Namespace: tag,
		Reason:    "crashed",
	})
	if err != nil {
		p.Log().Error("Failed to send UnregisterPlugin message", "namespace", tag, "error", err)
	}

	// Check restart limit
	if pluginInfo.restartCount >= MaxPluginRestarts {
		p.Log().Error("Plugin exceeded max restart attempts",
			"namespace", tag,
			"restartCount", pluginInfo.restartCount,
			"maxRestarts", MaxPluginRestarts)
		return
	}

	// Increment restart count and attempt restart
	pluginInfo.restartCount++
	p.Log().Info("Attempting to restart resource plugin",
		"namespace", tag,
		"restartCount", pluginInfo.restartCount,
		"maxRestarts", MaxPluginRestarts)

	err = p.spawnResourcePlugin(tag, pluginInfo)
	if err != nil {
		p.Log().Error("Failed to restart resource plugin",
			"namespace", tag,
			"restartCount", pluginInfo.restartCount,
			"error", err)
	} else {
		p.Log().Info("Resource plugin restarted successfully",
			"namespace", tag,
			"restartCount", pluginInfo.restartCount)
	}
}

// handleAuthPluginTerminate handles termination of an auth plugin process.
func (p *PluginProcessSupervisor) handleAuthPluginTerminate(tag string) {
	name := auth.AuthTagName(tag)
	if p.shuttingDown {
		p.Log().Debug("Auth plugin stopped during shutdown", "name", name)
		return
	}
	p.Log().Error("Auth plugin terminated unexpectedly", "name", name, "tag", tag)

	if p.authPlugin == nil {
		return
	}
	entry := p.authPlugin

	entry.healthy = false
	entry.lastCrashTime = time.Now()

	// Close old connection so pending RPC calls fail fast
	entry.handle.Close()

	// Check restart limit
	if entry.restartCount >= MaxPluginRestarts {
		p.Log().Error("Auth plugin exceeded max restart attempts",
			"name", name,
			"restartCount", entry.restartCount,
			"maxRestarts", MaxPluginRestarts)
		return
	}

	// Increment restart count and attempt restart
	entry.restartCount++
	p.Log().Info("Attempting to restart auth plugin",
		"name", name,
		"restartCount", entry.restartCount,
		"maxRestarts", MaxPluginRestarts)

	// Reset so the next ready signal triggers completeAuthPluginInit again.
	entry.initStarted = false

	err := p.spawnAuthPluginProcess(tag, entry)
	if err != nil {
		p.Log().Error("Failed to restart auth plugin",
			"name", name,
			"restartCount", entry.restartCount,
			"error", err)
	} else {
		// RPC Init deferred — the new process will send a ready signal.
		p.Log().Info("Auth plugin restarted successfully",
			"name", name,
			"restartCount", entry.restartCount)
	}
}

// spawnResourcePlugin spawns a resource plugin process via meta.Port
func (p *PluginProcessSupervisor) spawnResourcePlugin(namespace string, pluginInfo *PluginInfo) error {
	// Get server config from environment
	serverConfigVal, ok := p.Env("ServerConfig")
	if !ok {
		return fmt.Errorf("ServerConfig not found in environment")
	}

	serverConfig, ok := serverConfigVal.(pkgmodel.ServerConfig)
	if !ok {
		return fmt.Errorf("ServerConfig has wrong type")
	}

	// Create node name for the plugin - include agent nodename prefix for uniqueness when running parallel agents
	nodeName := fmt.Sprintf("%s-%s-plugin@%s", serverConfig.Nodename, strings.ToLower(namespace), serverConfig.Hostname)
	agentNode := fmt.Sprintf("%s@%s", serverConfig.Nodename, serverConfig.Hostname)

	// Build environment variables for the plugin process
	env := map[gen.Env]string{
		gen.Env("FORMAE_AGENT_NODE"):     agentNode,
		gen.Env("FORMAE_PLUGIN_NODE"):    nodeName,
		gen.Env("FORMAE_NETWORK_COOKIE"): serverConfig.Secret,
		gen.Env("FORMAE_VERSION"):        formae.Version,
		gen.Env("FORMAE_ERGO_PORT"):      strconv.Itoa(serverConfig.ErgoPort),
		gen.Env("FORMAE_REGISTRAR_PORT"): strconv.Itoa(serverConfig.RegistrarPort),
	}

	// Add OTel configuration if available
	if otelConfigVal, ok := p.Env("OTelConfig"); ok {
		if otelConfig, ok := otelConfigVal.(pkgmodel.OTelConfig); ok && otelConfig.Enabled {
			env[gen.Env("FORMAE_OTEL_ENABLED")] = "true"
			env[gen.Env("FORMAE_OTEL_ENDPOINT")] = otelConfig.OTLP.Endpoint
			env[gen.Env("FORMAE_OTEL_PROTOCOL")] = otelConfig.OTLP.Protocol
			if otelConfig.OTLP.Insecure {
				env[gen.Env("FORMAE_OTEL_INSECURE")] = "true"
			} else {
				env[gen.Env("FORMAE_OTEL_INSECURE")] = "false"
			}
			p.Log().Debug("OTel config passed to plugin", "namespace", namespace, "endpoint", otelConfig.OTLP.Endpoint)
		}
	}

	// Configure meta.Port options
	portOptions := meta.PortOptions{
		Cmd:         pluginInfo.binaryPath,
		EnableEnvOS: true, // Inherit OS environment (PATH, etc.) so plugins can find dependencies like pkl
		Env:         env,
		Tag:         namespace,
		Process:     gen.Atom("PluginProcessSupervisor"), // Send messages to the PluginProcessSupervisor actor
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

	p.Log().Debug("Spawned plugin", "namespace", namespace, "node", nodeName, "alias", alias)
	return nil
}

// spawnAuthPluginProcess spawns an auth plugin binary via meta.Port with binary mode
// and sets up the MetaPortConn. Does NOT call RPC Init — that must be deferred to
// completeAuthPluginInit() after the actor's Init() returns, because the RPC call
// needs HandleMessage to route meta.Port data.
func (p *PluginProcessSupervisor) spawnAuthPluginProcess(tag string, entry *authPluginEntry) error {
	handle := entry.handle

	// Configure meta.Port with binary mode for RPC communication.
	// Binary mode sends stdout data as MessagePortData (raw bytes)
	// instead of MessagePortText (line-split text).
	portOptions := meta.PortOptions{
		Cmd:         handle.BinaryPath(),
		EnableEnvOS: true,
		Tag:         tag,
		Process:     gen.Atom("PluginProcessSupervisor"),
		Binary:      meta.PortBinaryOptions{Enable: true, ReadBufferSize: 4096},
	}

	metaport, err := meta.CreatePort(portOptions)
	if err != nil {
		return fmt.Errorf("failed to create meta.Port for auth plugin %q: %w", handle.Name(), err)
	}

	alias, err := p.SpawnMeta(metaport, gen.MetaOptions{})
	if err != nil {
		return fmt.Errorf("failed to spawn auth plugin %q via meta.Port: %w", handle.Name(), err)
	}

	// Create MetaPortConn bridging meta.Port to io.ReadWriteCloser for net/rpc
	conn := auth.NewMetaPortConn(alias, p.SendAlias)

	entry.metaPortAlias = alias
	entry.conn = conn

	p.Log().Debug("Spawned auth plugin process", "name", handle.Name(), "tag", tag, "alias", alias)
	return nil
}

// completeAuthPluginInit finishes the auth plugin handshake by creating an RPC client,
// calling Init, and storing the client in the handle. Launched as a goroutine from
// HandleMessage when the first MessagePortData (ready signal) arrives from the auth plugin.
func (p *PluginProcessSupervisor) completeAuthPluginInit() {
	if p.authPlugin == nil || p.authPlugin.conn == nil {
		return
	}

	entry := p.authPlugin
	handle := entry.handle
	conn := entry.conn

	rpcClient := rpc.NewClient(conn)

	type initResult struct {
		resp pkgauth.InitResponse
		err  error
	}
	ch := make(chan initResult, 1)
	go func() {
		var r initResult
		r.err = rpcClient.Call("AuthPlugin.Init", &pkgauth.InitRequest{Config: handle.ConfigJSON()}, &r.resp)
		ch <- r
	}()

	var resp pkgauth.InitResponse
	var initErr error
	select {
	case r := <-ch:
		resp, initErr = r.resp, r.err
	case <-time.After(30 * time.Second):
		initErr = fmt.Errorf("auth plugin %q: init timed out after 30s", handle.Name())
	}

	if initErr != nil {
		_ = rpcClient.Close()
		_ = conn.Close()
		p.Log().Error("Auth plugin init call failed — agent cannot serve authenticated requests",
			"name", handle.Name(), "error", initErr)
		handle.SetInitError(fmt.Errorf("auth plugin %q init failed: %w", handle.Name(), initErr))
		return
	}
	if resp.Error != "" {
		_ = rpcClient.Close()
		_ = conn.Close()
		p.Log().Error("Auth plugin rejected config — agent cannot serve authenticated requests",
			"name", handle.Name(), "error", resp.Error)
		handle.SetInitError(fmt.Errorf("auth plugin %q: %s", handle.Name(), resp.Error))
		return
	}

	handle.Connect(conn, rpcClient)

	// Signal the actor loop to mark the plugin as healthy. Writing
	// entry.healthy directly from this goroutine would be a data race
	// on actor-owned state.
	if err := p.Send(p.PID(), authPluginInitComplete{}); err != nil {
		p.Log().Error("Failed to send auth init complete message", "error", err)
	}
}

// Terminate is called when the actor is being stopped.
// We gracefully terminate all plugin meta.Ports to avoid race conditions
// during shutdown that would cause "unable to send MessagePortError" errors.
func (p *PluginProcessSupervisor) Terminate(reason error) {
	p.shuttingDown = true
	p.Log().Debug("PluginProcessSupervisor terminating, stopping all plugins", "reason", reason)

	var zeroAlias gen.Alias

	// Terminate resource plugins
	for namespace, pluginInfo := range p.plugins {
		if pluginInfo.metaPortAlias != zeroAlias {
			p.Log().Debug("Terminating resource plugin", "namespace", namespace, "alias", pluginInfo.metaPortAlias)
			if err := p.SendExitMeta(pluginInfo.metaPortAlias, reason); err != nil {
				p.Log().Debug("Failed to send exit to resource plugin", "namespace", namespace, "error", err)
			}
		}
	}

	// Terminate auth plugin
	if p.authPlugin != nil {
		p.authPlugin.handle.Close()
		if p.authPlugin.metaPortAlias != zeroAlias {
			tag := auth.AuthTag(p.authPlugin.handle.Name())
			p.Log().Debug("Terminating auth plugin", "tag", tag, "alias", p.authPlugin.metaPortAlias)
			if err := p.SendExitMeta(p.authPlugin.metaPortAlias, reason); err != nil {
				p.Log().Debug("Failed to send exit to auth plugin", "tag", tag, "error", err)
			}
		}
	}

	p.Log().Debug("PluginProcessSupervisor terminated")
}
