// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_process_supervisor

import (
	"encoding/base64"
	"encoding/json"
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

	plugins       map[string]*PluginInfo
	authPlugin    *authPluginEntry
	shuttingDown  bool
	pluginConfigs map[string]json.RawMessage // plugin-specific config keyed by type (lowercase)

	// oidcBrokers holds the credential broker subprocesses, keyed by plugin
	// name. oidcCredentialConfigs is their opaque config keyed by type
	// (lowercase).
	oidcBrokers           map[string]*oidcBrokerEntry
	oidcCredentialConfigs map[string]json.RawMessage
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
	p.oidcBrokers = make(map[string]*oidcBrokerEntry)
	p.Log().Debug("PluginProcessSupervisor started")

	// Read per-plugin custom configs before spawning plugins so the env vars are available
	if val, ok := p.Env("ResourcePluginConfigs"); ok {
		if configs, ok := val.([]pkgmodel.ResourcePluginUserConfig); ok {
			p.pluginConfigs = make(map[string]json.RawMessage)
			for _, cfg := range configs {
				if len(cfg.PluginConfig) > 0 {
					p.pluginConfigs[cfg.Type] = cfg.PluginConfig
				}
			}
		}
	}
	if p.pluginConfigs == nil {
		p.pluginConfigs = make(map[string]json.RawMessage)
	}

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
			p.Log().Error("Failed to spawn plugin namespace=%s: %v", namespace, err)
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

			p.Log().Debug("Spawning auth plugin name=%s tag=%s", handle.Name(), tag)

			if err := p.spawnAuthPluginProcess(tag, entry); err != nil {
				p.Log().Error("Failed to spawn auth plugin process name=%s: %v", handle.Name(), err)
			}
			// RPC Init is deferred: the plugin writes a ready signal to stdout
			// when it starts, which arrives as MessagePortData in HandleMessage.
			// That triggers completeAuthPluginInit() in a goroutine.
		}
	}

	p.initOidcCredentialBrokers()

	authConfigured := p.authPlugin != nil
	p.Log().Debug("PluginProcessSupervisor initialized with %d resource plugins, %d oidc-credential brokers, auth=%v",
		len(p.plugins), len(p.oidcBrokers), authConfigured)
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

// Plugins do not agree on a log format, so two are recognised.
//
// Ergo's runtime writes "timestamp [level] rest_of_message", e.g.
// "1764458575429677244 [info] <79F4473F.0.1004>: message". Anything built on
// log/slog writes logfmt with an uppercase level, e.g.
// `time=... level=WARN msg="rejected credential"`, which is what the auth
// plugins emit. Reading only the first left every slog line unclassified.
var (
	pluginLogRegex = regexp.MustCompile(`^\d+\s+\[(trace|debug|info|warning|error)\]\s+(.*)$`)
	// The level must start a token, so one quoted inside a message is not
	// mistaken for the line's own.
	pluginLogfmtLevelRegex = regexp.MustCompile(`(?:^|\s)level=([A-Za-z]+)`)
)

// pluginLevel reports the level a plugin gave one of its own log lines,
// normalised to the names Log() uses, or "" when the line names none.
func pluginLevel(output string) string {
	if matches := pluginLogRegex.FindStringSubmatch(output); matches != nil {
		return matches[1]
	}
	if matches := pluginLogfmtLevelRegex.FindStringSubmatch(output); matches != nil {
		switch strings.ToLower(matches[1]) {
		case "trace":
			return "trace"
		case "debug":
			return "debug"
		case "info":
			return "info"
		case "warn", "warning":
			return "warning"
		case "error":
			return "error"
		}
	}
	return ""
}

// logAtPluginLevel reports a plugin's line at the level the plugin chose,
// deferring to fallback when it named none.
//
// The message travels as an argument, never as the format string: it is text
// the plugin wrote, and a stray verb in it would garble the line.
func (p *PluginProcessSupervisor) logAtPluginLevel(level, message string, fallback func(string, ...any)) {
	switch level {
	case "trace":
		p.Log().Trace("%s", message)
	case "debug":
		p.Log().Debug("%s", message)
	case "info":
		p.Log().Info("%s", message)
	case "warning":
		p.Log().Warning("%s", message)
	case "error":
		p.Log().Error("%s", message)
	default:
		fallback("%s", message)
	}
}

func (p *PluginProcessSupervisor) logPluginOutput(pluginName, output string) {
	if output == "" {
		return
	}

	message := output
	if matches := pluginLogRegex.FindStringSubmatch(output); matches != nil {
		message = matches[2]
	}

	p.logAtPluginLevel(pluginLevel(output), fmt.Sprintf("[%s] %s", pluginName, message), p.Log().Info)
}

func (p *PluginProcessSupervisor) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case meta.MessagePortText:
		// Text output (stderr for auth plugins, stdout for resource plugins)
		output := strings.TrimSpace(msg.Text)
		switch {
		case auth.IsAuthTag(msg.Tag):
			p.logPluginOutput(auth.AuthTagName(msg.Tag), output)
		case isOidcCredentialTag(msg.Tag):
			p.logPluginOutput(oidcCredentialTagName(msg.Tag), output)
		default:
			p.logPluginOutput(p.getPluginName(msg.Tag), output)
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
		} else if isOidcCredentialTag(msg.Tag) {
			// Brokers run in text mode, so binary data is unexpected — log it
			// rather than dropping it silently.
			output := strings.TrimSpace(string(msg.Data))
			p.logPluginOutput(oidcCredentialTagName(msg.Tag), output)
		} else if !auth.IsAuthTag(msg.Tag) {
			// Resource plugin binary data - log it
			output := strings.TrimSpace(string(msg.Data))
			pluginName := p.getPluginName(msg.Tag)
			p.logPluginOutput(pluginName, output)
		}

	case meta.MessagePortError:
		// The port is named for the channel, not the severity: a plugin's whole
		// log stream arrives here at whatever level its author chose. Reporting
		// all of it at Error made an ordinary rejected credential
		// indistinguishable from a fault, and an installation's "error logs
		// occurring" alert fired on a mistyped password.
		//
		// A line naming no level stays at Error: on the error port, assuming
		// the worst is the right default. The wording is unchanged because it
		// names the port, and operators filter alert queries on it.
		p.logAtPluginLevel(
			pluginLevel(fmt.Sprint(msg.Error)),
			fmt.Sprintf("Plugin error tag=%s: %v", msg.Tag, msg.Error),
			p.Log().Error,
		)

	case meta.MessagePortTerminate:
		switch {
		case auth.IsAuthTag(msg.Tag):
			p.handleAuthPluginTerminate(msg.Tag)
		case isOidcCredentialTag(msg.Tag):
			p.handleOidcCredentialBrokerTerminate(msg.Tag)
		default:
			p.handleResourcePluginTerminate(msg.Tag)
		}

	case authPluginInitComplete:
		if p.authPlugin != nil {
			p.authPlugin.healthy = true
			p.Log().Info("Auth plugin initialized name=%s", p.authPlugin.handle.Name())
		}

	}

	return nil
}

// handleResourcePluginTerminate handles termination of a resource plugin process.
func (p *PluginProcessSupervisor) handleResourcePluginTerminate(tag string) {
	if p.shuttingDown {
		p.Log().Debug("Resource plugin stopped during shutdown namespace=%s", tag)
		return
	}
	p.Log().Error("Resource plugin terminated unexpectedly namespace=%s", tag)

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
		p.Log().Error("Failed to send UnregisterPlugin message namespace=%s: %v", tag, err)
	}

	// Check restart limit
	if pluginInfo.restartCount >= MaxPluginRestarts {
		p.Log().Error("Plugin exceeded max restart attempts namespace=%s restartCount=%d maxRestarts=%d",
			tag, pluginInfo.restartCount, MaxPluginRestarts)
		return
	}

	// Increment restart count and attempt restart
	pluginInfo.restartCount++
	p.Log().Info("Attempting to restart resource plugin namespace=%s restartCount=%d maxRestarts=%d",
		tag, pluginInfo.restartCount, MaxPluginRestarts)

	err = p.spawnResourcePlugin(tag, pluginInfo)
	if err != nil {
		p.Log().Error("Failed to restart resource plugin namespace=%s restartCount=%d: %v",
			tag, pluginInfo.restartCount, err)
	} else {
		p.Log().Info("Resource plugin restarted successfully namespace=%s restartCount=%d",
			tag, pluginInfo.restartCount)
	}
}

// handleAuthPluginTerminate handles termination of an auth plugin process.
func (p *PluginProcessSupervisor) handleAuthPluginTerminate(tag string) {
	name := auth.AuthTagName(tag)
	if p.shuttingDown {
		p.Log().Debug("Auth plugin stopped during shutdown name=%s", name)
		return
	}
	p.Log().Error("Auth plugin terminated unexpectedly name=%s tag=%s", name, tag)

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
		p.Log().Error("Auth plugin exceeded max restart attempts name=%s restartCount=%d maxRestarts=%d",
			name, entry.restartCount, MaxPluginRestarts)
		return
	}

	// Increment restart count and attempt restart
	entry.restartCount++
	p.Log().Info("Attempting to restart auth plugin name=%s restartCount=%d maxRestarts=%d",
		name, entry.restartCount, MaxPluginRestarts)

	// Reset so the next ready signal triggers completeAuthPluginInit again.
	entry.initStarted = false

	err := p.spawnAuthPluginProcess(tag, entry)
	if err != nil {
		p.Log().Error("Failed to restart auth plugin name=%s restartCount=%d: %v",
			name, entry.restartCount, err)
	} else {
		// RPC Init deferred — the new process will send a ready signal.
		p.Log().Info("Auth plugin restarted successfully name=%s restartCount=%d",
			name, entry.restartCount)
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
			p.Log().Debug("OTel config passed to plugin namespace=%s endpoint=%s", namespace, otelConfig.OTLP.Endpoint)
		}
	}

	// Add plugin-specific config if available
	if pluginCfg, ok := p.pluginConfigs[strings.ToLower(namespace)]; ok {
		env[gen.Env("FORMAE_PLUGIN_CONFIG")] = base64.StdEncoding.EncodeToString(pluginCfg)
	}

	// Configure meta.Port options
	portOptions := meta.PortOptions{
		Cmd:         pluginInfo.binaryPath,
		EnableEnvOS: true, // Inherit OS environment (PATH, etc.) so plugins can find dependencies like pkl
		Env:         env,
		Tag:         namespace,
		Process:     gen.Atom("PluginProcessSupervisor"), // Send messages to the PluginProcessSupervisor actor
		SysProcAttr: pluginSysProcAttr(),                 // Kill the plugin if the agent dies abruptly (Linux Pdeathsig)
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

	p.Log().Debug("Spawned plugin namespace=%s node=%s alias=%v", namespace, nodeName, alias)
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
		SysProcAttr: pluginSysProcAttr(), // Kill the auth plugin if the agent dies abruptly (Linux Pdeathsig)
	}

	metaport, err := meta.CreatePort(portOptions)
	if err != nil {
		return fmt.Errorf("failed to create meta.Port for auth plugin %q: %w", handle.Name(), err)
	}

	alias, err := p.SpawnMeta(metaport, gen.MetaOptions{})
	if err != nil {
		return fmt.Errorf("failed to spawn auth plugin %q via meta.Port: %w", handle.Name(), err)
	}

	// Create MetaPortConn bridging meta.Port to io.ReadWriteCloser for net/rpc.
	// Use Node().Send instead of process SendAlias because the RPC init runs
	// in a goroutine, and Ergo v3.2.0 forbids process-level Send/SendAlias
	// from goroutines when the process is sleeping. Node.Send is not bound to
	// process state and works from any goroutine.
	node := p.Node()
	conn := auth.NewMetaPortConn(alias, func(a gen.Alias, msg any) error {
		return node.Send(a, msg)
	})

	entry.metaPortAlias = alias
	entry.conn = conn

	p.Log().Debug("Spawned auth plugin process name=%s tag=%s alias=%v", handle.Name(), tag, alias)
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
		p.Log().Error("Auth plugin init call failed — agent cannot serve authenticated requests name=%s: %v",
			handle.Name(), initErr)
		handle.SetInitError(fmt.Errorf("auth plugin %q init failed: %w", handle.Name(), initErr))
		return
	}
	if resp.Error != "" {
		_ = rpcClient.Close()
		_ = conn.Close()
		p.Log().Error("Auth plugin rejected config — agent cannot serve authenticated requests name=%s: %s",
			handle.Name(), resp.Error)
		handle.SetInitError(fmt.Errorf("auth plugin %q: %s", handle.Name(), resp.Error))
		return
	}

	handle.Connect(conn, rpcClient)

	// Signal the actor loop to mark the plugin as healthy. Writing
	// entry.healthy directly from this goroutine would be a data race
	// on actor-owned state. Use Node().Send (not bound to process state)
	// because this runs in a goroutine where process Send is not allowed.
	if err := p.Node().Send(p.PID(), authPluginInitComplete{}); err != nil {
		p.Log().Error("Failed to send auth init complete message: %v", err)
	}
}

// Terminate is called when the actor is being stopped.
// We gracefully terminate all plugin meta.Ports to avoid race conditions
// during shutdown that would cause "unable to send MessagePortError" errors.
func (p *PluginProcessSupervisor) Terminate(reason error) {
	p.shuttingDown = true
	p.Log().Debug("PluginProcessSupervisor terminating, stopping all plugins reason=%v", reason)

	var zeroAlias gen.Alias

	// Terminate resource plugins
	for namespace, pluginInfo := range p.plugins {
		if pluginInfo.metaPortAlias != zeroAlias {
			p.Log().Debug("Terminating resource plugin namespace=%s alias=%v", namespace, pluginInfo.metaPortAlias)
			if err := p.SendExitMeta(pluginInfo.metaPortAlias, reason); err != nil {
				p.Log().Debug("Failed to send exit to resource plugin namespace=%s: %v", namespace, err)
			}
		}
	}

	// Terminate oidc-credential brokers, unregistering the launch that is
	// going away so the coordinator stops routing to it.
	for name, entry := range p.oidcBrokers {
		if entry.metaPortAlias == zeroAlias {
			continue
		}
		p.unregisterOidcBroker(entry, "shutdown")
		p.Log().Debug("Terminating oidc-credential broker name=%s alias=%v", name, entry.metaPortAlias)
		if err := p.SendExitMeta(entry.metaPortAlias, reason); err != nil {
			p.Log().Debug("Failed to send exit to oidc-credential broker name=%s: %v", name, err)
		}
	}

	// Terminate auth plugin
	if p.authPlugin != nil {
		p.authPlugin.handle.Close()
		if p.authPlugin.metaPortAlias != zeroAlias {
			tag := auth.AuthTag(p.authPlugin.handle.Name())
			p.Log().Debug("Terminating auth plugin tag=%s alias=%v", tag, p.authPlugin.metaPortAlias)
			if err := p.SendExitMeta(p.authPlugin.metaPortAlias, reason); err != nil {
				p.Log().Debug("Failed to send exit to auth plugin tag=%s: %v", tag, err)
			}
		}
	}

	p.Log().Debug("PluginProcessSupervisor terminated")
}
