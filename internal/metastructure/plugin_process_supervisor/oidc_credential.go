// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_process_supervisor

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/meta"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// oidcCredentialTagPrefix namespaces broker meta.Port tags. Resource plugins
// tag their port with the bare namespace, so without a prefix a broker named
// "aws" and the AWS resource plugin would route each other's port events.
const oidcCredentialTagPrefix = "oidccred:"

func oidcCredentialTag(name string) string { return oidcCredentialTagPrefix + name }

func isOidcCredentialTag(tag string) bool {
	return strings.HasPrefix(tag, oidcCredentialTagPrefix)
}

func oidcCredentialTagName(tag string) string {
	return strings.TrimPrefix(tag, oidcCredentialTagPrefix)
}

// oidcBrokerEntry tracks one credential broker subprocess. spawnToken is
// minted per launch, so a restarted broker announces itself with a token the
// coordinator can tell apart from the dead process's.
type oidcBrokerEntry struct {
	name          string
	version       string
	binaryPath    string
	namespaces    []string
	spawnToken    string
	metaPortAlias gen.Alias
	nodeName      gen.Atom
	healthy       bool
	restartCount  int
	lastCrashTime time.Time
}

// mintSpawnToken returns a fresh, unguessable token identifying a single
// broker launch.
func mintSpawnToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand.Read never returns an error on the platforms we build
		// for; fall back to a value that is still unique per launch.
		return fmt.Sprintf("spawn-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// brokersToSpawn returns the discovered brokers the agent should launch:
// every one found on disk except those a config entry explicitly disables.
// A broker with no config entry is spawned and receives no config payload,
// which the SDK tolerates.
func brokersToSpawn(
	configs []pkgmodel.OidcCredentialPluginUserConfig,
	infos []plugin.OidcCredentialPluginInfo,
) []plugin.OidcCredentialPluginInfo {
	disabled := make(map[string]bool, len(configs))
	for _, cfg := range configs {
		if !cfg.Enabled {
			disabled[strings.ToLower(cfg.Type)] = true
		}
	}

	var spawn []plugin.OidcCredentialPluginInfo
	for _, info := range infos {
		if disabled[strings.ToLower(info.Name)] {
			continue
		}
		spawn = append(spawn, info)
	}
	return spawn
}

// oidcBrokerNodeName is the Ergo node name a broker process runs under. The
// "-oidccred" suffix keeps it distinct from a resource plugin's node even
// when a broker and a namespace share a name.
func oidcBrokerNodeName(serverConfig pkgmodel.ServerConfig, name string) string {
	return fmt.Sprintf("%s-%s-oidccred@%s",
		serverConfig.Nodename, strings.ToLower(name), serverConfig.Hostname)
}

// buildOidcCredentialEnv builds the environment a broker process reads on
// startup (see pkg/credential.Run). Pure, so what the broker is handed is
// testable without spawning anything. FORMAE_ERGO_PORT carries the agent's
// own acceptor port for addressing the agent, not a port for the broker to
// bind; the broker binds port 0 deliberately.
func buildOidcCredentialEnv(
	serverConfig pkgmodel.ServerConfig,
	name string,
	spawnToken string,
	cfg json.RawMessage,
) map[gen.Env]string {
	env := map[gen.Env]string{
		gen.Env("FORMAE_AGENT_NODE"):     fmt.Sprintf("%s@%s", serverConfig.Nodename, serverConfig.Hostname),
		gen.Env("FORMAE_PLUGIN_NODE"):    oidcBrokerNodeName(serverConfig, name),
		gen.Env("FORMAE_NETWORK_COOKIE"): serverConfig.Secret,
		gen.Env("FORMAE_VERSION"):        formae.Version,
		gen.Env("FORMAE_ERGO_PORT"):      strconv.Itoa(serverConfig.ErgoPort),
		gen.Env("FORMAE_REGISTRAR_PORT"): strconv.Itoa(serverConfig.RegistrarPort),
		gen.Env("FORMAE_SPAWN_TOKEN"):    spawnToken,
	}

	if len(cfg) > 0 {
		env[gen.Env("FORMAE_PLUGIN_CONFIG")] = base64.StdEncoding.EncodeToString(cfg)
	}

	return env
}

// initOidcCredentialBrokers registers and spawns every enabled broker found
// by discovery. Called from the supervisor's Init.
func (p *PluginProcessSupervisor) initOidcCredentialBrokers() {
	var infos []plugin.OidcCredentialPluginInfo
	if val, ok := p.Env("OidcCredentialPlugins"); ok {
		if brokers, ok := val.([]plugin.OidcCredentialPluginInfo); ok {
			infos = brokers
		}
	}

	var configs []pkgmodel.OidcCredentialPluginUserConfig
	if val, ok := p.Env("OidcCredentialPluginConfigs"); ok {
		if cfgs, ok := val.([]pkgmodel.OidcCredentialPluginUserConfig); ok {
			configs = cfgs
		}
	}

	p.oidcCredentialConfigs = make(map[string]json.RawMessage)
	for _, cfg := range configs {
		if len(cfg.PluginConfig) > 0 {
			p.oidcCredentialConfigs[strings.ToLower(cfg.Type)] = cfg.PluginConfig
		}
	}

	spawn := brokersToSpawn(configs, infos)
	p.Log().Debug("Discovered %d oidc-credential brokers, spawning %d", len(infos), len(spawn))

	for _, info := range spawn {
		entry := &oidcBrokerEntry{
			name:       info.Name,
			version:    info.Version,
			binaryPath: info.BinaryPath,
			namespaces: info.Namespaces,
		}
		p.oidcBrokers[info.Name] = entry

		if err := p.spawnOidcCredentialBroker(entry); err != nil {
			p.Log().Error("Failed to spawn oidc-credential broker name=%s: %v", info.Name, err)
			continue
		}
	}
}

// spawnOidcCredentialBroker launches a broker subprocess via meta.Port with a
// freshly minted spawn token.
func (p *PluginProcessSupervisor) spawnOidcCredentialBroker(entry *oidcBrokerEntry) error {
	serverConfigVal, ok := p.Env("ServerConfig")
	if !ok {
		return fmt.Errorf("ServerConfig not found in environment")
	}
	serverConfig, ok := serverConfigVal.(pkgmodel.ServerConfig)
	if !ok {
		return fmt.Errorf("ServerConfig has wrong type")
	}

	entry.spawnToken = mintSpawnToken()
	env := buildOidcCredentialEnv(
		serverConfig, entry.name, entry.spawnToken, p.oidcCredentialConfigs[strings.ToLower(entry.name)],
	)

	tag := oidcCredentialTag(entry.name)
	portOptions := meta.PortOptions{
		Cmd:         entry.binaryPath,
		EnableEnvOS: true,
		Env:         env,
		Tag:         tag,
		Process:     gen.Atom("PluginProcessSupervisor"),
		SysProcAttr: pluginSysProcAttr(), // Kill the broker if the agent dies abruptly (Linux Pdeathsig)
	}

	metaport, err := meta.CreatePort(portOptions)
	if err != nil {
		return fmt.Errorf("failed to create meta.Port for oidc-credential broker %q: %w", entry.name, err)
	}

	alias, err := p.SpawnMeta(metaport, gen.MetaOptions{})
	if err != nil {
		return fmt.Errorf("failed to spawn oidc-credential broker %q via meta.Port: %w", entry.name, err)
	}

	entry.metaPortAlias = alias
	entry.nodeName = gen.Atom(oidcBrokerNodeName(serverConfig, entry.name))
	entry.healthy = true

	p.Log().Debug("Spawned oidc-credential broker name=%s node=%s alias=%v", entry.name, entry.nodeName, alias)
	return nil
}

// handleOidcCredentialBrokerTerminate unregisters a departed broker and
// restarts it, up to MaxPluginRestarts.
func (p *PluginProcessSupervisor) handleOidcCredentialBrokerTerminate(tag string) {
	name := oidcCredentialTagName(tag)
	if p.shuttingDown {
		p.Log().Debug("Oidc-credential broker stopped during shutdown name=%s", name)
		return
	}
	p.Log().Error("Oidc-credential broker terminated unexpectedly name=%s", name)

	entry, ok := p.oidcBrokers[name]
	if !ok {
		return
	}

	entry.healthy = false
	entry.lastCrashTime = time.Now()

	// Unregister with the token the dead process was launched under, before a
	// restart mints a new one.
	p.unregisterOidcBroker(entry, "crashed")

	if entry.restartCount >= MaxPluginRestarts {
		p.Log().Error("Oidc-credential broker exceeded max restart attempts name=%s restartCount=%d maxRestarts=%d",
			name, entry.restartCount, MaxPluginRestarts)
		return
	}

	entry.restartCount++
	p.Log().Info("Attempting to restart oidc-credential broker name=%s restartCount=%d maxRestarts=%d",
		name, entry.restartCount, MaxPluginRestarts)

	if err := p.spawnOidcCredentialBroker(entry); err != nil {
		p.Log().Error("Failed to restart oidc-credential broker name=%s restartCount=%d: %v",
			name, entry.restartCount, err)
		return
	}
	p.Log().Info("Oidc-credential broker restarted successfully name=%s restartCount=%d",
		name, entry.restartCount)
}

// unregisterOidcBroker tells the PluginCoordinator the broker launch
// identified by entry.spawnToken is gone.
func (p *PluginProcessSupervisor) unregisterOidcBroker(entry *oidcBrokerEntry, reason string) {
	err := p.Send(actornames.PluginCoordinator, messages.UnregisterOidcCredentialPlugin{
		Name:       entry.name,
		SpawnToken: entry.spawnToken,
		Reason:     reason,
	})
	if err != nil {
		p.Log().Error("Failed to send UnregisterOidcCredentialPlugin message name=%s: %v", entry.name, err)
	}
}
