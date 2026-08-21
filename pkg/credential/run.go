// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package credential

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/registrar"
)

// node is a thin seam over the started Ergo node: just enough for the
// broker to announce itself and, on shutdown, stop cleanly. Real callers get
// a wrapper around gen.Node; tests get a fake.
type node interface {
	Send(to gen.ProcessID, message any) error
	Stop()
}

// nodeConfig carries everything startNode needs to bring the broker's Ergo
// node up: network identity plus the (already configured) plugin to wire
// into the serving actor's environment.
type nodeConfig struct {
	pluginNode    string
	cookie        string
	ergoPort      int
	registrarPort int
	plugin        OidcCredentialPlugin
}

// runSteps seams Run's startup sequence for testing: configure, then start
// the node, then announce. Each step is swapped for a stub in tests so the
// sequencing and error-abort behavior is testable without a live Ergo node.
type runSteps struct {
	configure func(p OidcCredentialPlugin, raw json.RawMessage) error
	startNode func(cfg nodeConfig) (node, error)
	announce  func(n node, a OidcCredentialPluginAnnouncement) error
}

// runEnv holds the environment configuration Run reads before starting up.
type runEnv struct {
	agentNode     string
	pluginNode    string
	cookie        string
	ergoPort      int
	registrarPort int
	pluginConfig  json.RawMessage // nil when FORMAE_PLUGIN_CONFIG is absent
	spawnToken    string
}

// readEnv reads the environment Run consumes. FORMAE_AGENT_NODE,
// FORMAE_PLUGIN_NODE, and FORMAE_NETWORK_COOKIE are required; the rest are
// optional (FORMAE_PLUGIN_CONFIG absent is a first-class case - it must
// still reach configure, as nil, not be skipped).
func readEnv() (runEnv, error) {
	e := runEnv{
		agentNode:  os.Getenv("FORMAE_AGENT_NODE"),
		pluginNode: os.Getenv("FORMAE_PLUGIN_NODE"),
		cookie:     os.Getenv("FORMAE_NETWORK_COOKIE"),
		spawnToken: os.Getenv("FORMAE_SPAWN_TOKEN"),
	}

	if e.agentNode == "" {
		return runEnv{}, fmt.Errorf("FORMAE_AGENT_NODE environment variable required")
	}
	if e.pluginNode == "" {
		return runEnv{}, fmt.Errorf("FORMAE_PLUGIN_NODE environment variable required")
	}
	if e.cookie == "" {
		return runEnv{}, fmt.Errorf("FORMAE_NETWORK_COOKIE environment variable required")
	}

	if v := os.Getenv("FORMAE_ERGO_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return runEnv{}, fmt.Errorf("FORMAE_ERGO_PORT: %w", err)
		}
		e.ergoPort = port
	}
	if v := os.Getenv("FORMAE_REGISTRAR_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return runEnv{}, fmt.Errorf("FORMAE_REGISTRAR_PORT: %w", err)
		}
		e.registrarPort = port
	}

	if v := os.Getenv("FORMAE_PLUGIN_CONFIG"); v != "" {
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return runEnv{}, fmt.Errorf("failed to decode FORMAE_PLUGIN_CONFIG: %w", err)
		}
		e.pluginConfig = raw
	}

	return e, nil
}

// requireOidcCredentialManifest fails Run when the manifest at the broker's
// own binary directory doesn't declare itself an oidc-credential plugin.
func requireOidcCredentialManifest(m *Manifest) error {
	if !m.IsOidcCredentialPlugin() {
		return fmt.Errorf("manifest type %q is not %q", m.Type, PluginTypeOidcCredential)
	}
	return nil
}

// readManifestForRun locates and validates the manifest next to the running
// broker binary, mirroring pkg/plugin/sdk.getPluginDir.
func readManifestForRun() (*Manifest, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}

	manifest, err := ReadManifestFromDir(filepath.Dir(execPath))
	if err != nil {
		return nil, err
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}
	if err := requireOidcCredentialManifest(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// runWithSteps drives the broker's startup sequence: configure the plugin
// first (even with a nil payload), start the Ergo node, then announce. A
// configure error aborts before startNode or announce ever run.
func runWithSteps(p OidcCredentialPlugin, manifest *Manifest, e runEnv, steps runSteps) error {
	if err := steps.configure(p, e.pluginConfig); err != nil {
		return fmt.Errorf("configure: %w", err)
	}

	n, err := steps.startNode(nodeConfig{
		pluginNode:    e.pluginNode,
		cookie:        e.cookie,
		ergoPort:      e.ergoPort,
		registrarPort: e.registrarPort,
		plugin:        p,
	})
	if err != nil {
		return fmt.Errorf("start node: %w", err)
	}

	announcement := OidcCredentialPluginAnnouncement{
		Name:       manifest.Name,
		Version:    manifest.Version,
		Namespaces: manifest.NormalizedNamespaces(),
		SpawnToken: e.spawnToken,
	}
	if err := steps.announce(n, announcement); err != nil {
		return fmt.Errorf("announce: %w", err)
	}

	return nil
}

// configureStep calls Configure on p when it implements Configurable. Not
// every OidcCredentialPlugin needs configuration, but the step itself
// always runs.
func configureStep(p OidcCredentialPlugin, raw json.RawMessage) error {
	cfg, ok := p.(Configurable)
	if !ok {
		return nil
	}
	return cfg.Configure(raw)
}

// liveNode wraps a started gen.Node to satisfy the node seam.
type liveNode struct {
	n gen.Node
}

func (l *liveNode) Send(to gen.ProcessID, message any) error { return l.n.Send(to, message) }
func (l *liveNode) Stop()                                    { l.n.Stop() }

// credentialApplication is the Ergo application hosting CredentialActor.
type credentialApplication struct{}

func (app *credentialApplication) Load(_ gen.Node, _ ...any) (gen.ApplicationSpec, error) {
	return gen.ApplicationSpec{
		Name:        "CredentialApplication",
		Description: "oidc-credential broker serving actor",
		Mode:        gen.ApplicationModePermanent,
		Group: []gen.ApplicationMemberSpec{
			{
				Name:    gen.Atom(ServerActorName),
				Factory: factoryCredentialActor,
			},
		},
	}, nil
}

func (app *credentialApplication) Start(_ gen.ApplicationMode) {}
func (app *credentialApplication) Terminate(_ error)           {}

// startNodeStep starts the broker's Ergo node with CredentialActor
// registered under ServerActorName, mirroring the node options
// pkg/plugin/run.go builds for resource plugins (network mode, cookie,
// registrar, acceptor).
func startNodeStep(cfg nodeConfig) (node, error) {
	if err := RegisterEDFTypes(); err != nil {
		return nil, fmt.Errorf("failed to register EDF types: %w", err)
	}

	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	options.Network.Cookie = cfg.cookie
	options.Security.ExposeEnvRemoteSpawn = true
	options.Log.Level = gen.LogLevelDebug
	options.Log.DefaultLogger.Disable = true
	options.Log.DefaultLogger.DisableBanner = true

	// Port is always 0 (auto-select), exactly like pkg/plugin/run.go:138.
	// cfg.ergoPort (FORMAE_ERGO_PORT) is NOT used here: that env var carries
	// the AGENT's own acceptor port, passed through to every plugin
	// process unmodified (see plugin_process_supervisor.go). Binding to it
	// would make the broker fight the agent for the same localhost port.
	// Agent<->broker addressing goes by node name through the shared
	// registrar (below), so the broker's own listen port never needs to be
	// predictable. Do not "fix" this back to cfg.ergoPort.
	options.Network.Acceptors = []gen.AcceptorOptions{
		{
			Host: "localhost",
			Port: 0,
		},
	}

	if cfg.registrarPort != 0 {
		options.Network.Registrar = registrar.Create(registrar.Options{Port: uint16(cfg.registrarPort)})
	}

	options.Env = map[gen.Env]any{
		gen.Env("Plugin"): cfg.plugin,
	}

	options.Applications = []gen.ApplicationBehavior{
		&credentialApplication{},
	}

	n, err := ergo.StartNode(gen.Atom(cfg.pluginNode), options)
	if err != nil {
		return nil, fmt.Errorf("failed to start node: %w", err)
	}

	return &liveNode{n: n}, nil
}

// Run starts the oidc-credential broker process: it reads its environment
// and manifest, configures the plugin, starts the Ergo node with the
// serving actor registered, and announces itself to the agent's
// PluginCoordinator (the same registered name pkg/plugin's PluginActor
// announces resource plugins to). It then blocks until asked to shut down.
func Run(p OidcCredentialPlugin) error {
	env, err := readEnv()
	if err != nil {
		return err
	}

	manifest, err := readManifestForRun()
	if err != nil {
		return err
	}

	var started node
	steps := runSteps{
		configure: configureStep,
		startNode: func(cfg nodeConfig) (node, error) {
			n, err := startNodeStep(cfg)
			if err != nil {
				return nil, err
			}
			started = n
			return n, nil
		},
		announce: func(n node, a OidcCredentialPluginAnnouncement) error {
			target := gen.ProcessID{Name: "PluginCoordinator", Node: gen.Atom(env.agentNode)}
			return n.Send(target, a)
		},
	}

	if err := runWithSteps(p, manifest, env, steps); err != nil {
		return err
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	if started != nil {
		started.Stop()
	}
	return nil
}
