# Distributed Plugin Architecture - Clarifications

**Date:** 2025-11-18
**Branch:** `feat/azure-plugin`
**Purpose:** Address specific questions raised about the distributed plugin architecture research

---

## 1. meta.Port Capabilities

### Question
> You mention using os/exec or meta.Port for spawning processes. AFAIK meta.Port is for IPC over stdin/stdout, not for spawning processes. Is that correct?

### Answer: **meta.Port DOES spawn processes**

**From Ergo Documentation** (v3.1.0+):

```go
// meta.Port API
options := meta.PortOptions{
    Cmd:  "path/to/plugin-binary",    // Command to execute
    Args: []string{"--agent-node", "agent@localhost"},  // Arguments
    Env:  []string{"AWS_REGION=us-east-1"},  // Environment variables
    EnableEnvMeta: true,   // Include meta-process env vars
    EnableEnvOS: false,    // Include OS env vars
    Tag: "aws-plugin",     // Distinguish multiple instances
    Process: pid,          // Where to send MessagePort* messages
}

metaport, err := meta.CreatePort(options)
id, err := actor.SpawnMeta(metaport, gen.MetaOptions{})
```

**Capabilities**:
- ✅ **Spawns external processes** via `Cmd` + `Args`
- ✅ **Manages lifecycle** (start via SpawnMeta, stop on termination)
- ✅ **Bidirectional communication** via stdin/stdout/stderr
- ✅ **Multiple instances** using `Tag` to distinguish
- ✅ **Integrated with supervision** (can be supervised like any other process)

**Text vs Binary Mode**:
- **Text mode** (default): Line-by-line via `bufio.Scanner`
- **Binary mode**: Configurable chunking with fixed/dynamic-length headers

**Messages Sent to Handler**:
```go
// Actor receives these messages
type MessagePortStdout struct {
    Data []byte
    Tag  string
}

type MessagePortStderr struct {
    Data []byte
    Tag  string
}

type MessagePortExit struct {
    Code int
    Tag  string
}
```

### Recommendation for Formae

We should use **both**:

1. **meta.Port for production**: Integrated supervision, clean actor integration
2. **os/exec.Cmd for development/testing**: Direct control, easier debugging

**Why meta.Port is better**:
- Automatic supervision integration
- Actor-native message handling
- Multiple instances with Tag
- Unified error handling

**Updated Architecture**:
```go
type PluginCoordinator struct {
    act.Actor
    plugins map[string]*PluginInfo  // namespace → info
}

type PluginInfo struct {
    namespace   string
    metaPortPID gen.PID           // PID of meta.Port process
    nodeName    string            // Remote node name
    tag         string            // For distinguishing instances
}

func (p *PluginCoordinator) Init(args ...any) error {
    // Spawn each plugin as meta.Port
    for namespace, config := range p.pluginConfigs {
        options := meta.PortOptions{
            Cmd:  config.BinaryPath,
            Args: []string{
                fmt.Sprintf("--agent-node=%s", p.Node().Name()),
                fmt.Sprintf("--plugin-node=%s-%s@localhost", namespace, uuid.New()),
                fmt.Sprintf("--cookie=%s", p.cookie),
            },
            Tag: namespace,
            Process: p.PID(),  // Send messages to coordinator
        }

        metaport, _ := meta.CreatePort(options)
        pid, _ := p.SpawnMeta(metaport, gen.MetaOptions{})

        p.plugins[namespace] = &PluginInfo{
            namespace:   namespace,
            metaPortPID: pid,
            nodeName:    options.Args[1], // Extract from args
            tag:         namespace,
        }
    }
}

func (p *PluginCoordinator) HandleMessage(from gen.PID, message any) error {
    switch msg := message.(type) {
    case meta.MessagePortStdout:
        // Plugin process stdout (could be logs)
        p.Log().Debug("Plugin output", "tag", msg.Tag, "data", string(msg.Data))

    case meta.MessagePortExit:
        // Plugin process died
        p.Log().Error("Plugin exited", "tag", msg.Tag, "code", msg.Code)
        // Restart plugin
        go p.restartPlugin(msg.Tag)
    }
}
```

---

## 2. PluginCoordinator vs Registrar vs actornames

### Question
> I like having a central actor registry. It is unclear from the research what the responsibilities of the PluginCoordinator vs the built-in registry vs the actornames package are. Do we need all three?

### Answer: **Yes, we need all three - they serve different purposes**

| Component | Purpose | Scope | Example |
|-----------|---------|-------|---------|
| **actornames Package** | Naming conventions/patterns | Code-level constants | `actornames.PluginOperator(uri, op, id)` → `"formae://aws/.../..."` |
| **Ergo Registrar** | Node/application discovery | Network-level service registry | Finds which host runs "aws-plugin@host1" |
| **PluginCoordinator Actor** | Plugin lifecycle management | Agent-level orchestration | Spawns plugins, tracks health, provides PIDs |

### Detailed Responsibilities

#### actornames Package (`internal/metastructure/actornames/`)
**Purpose**: Consistent naming scheme for actors

```go
// Current implementation
func PluginOperator(resourceURI, operation, operationID string) gen.Atom {
    return gen.Atom(fmt.Sprintf("%s/%s/%s", resourceURI, operation, operationID))
}

// Future: Remote-aware naming
func PluginOperatorRemote(namespace, resourceURI, operation, operationID string) gen.ProcessID {
    return gen.ProcessID{
        Name: PluginOperator(resourceURI, operation, operationID),
        Node: gen.Atom(fmt.Sprintf("%s-plugin@localhost", namespace)),
    }
}
```

**Doesn't change** - still generates names, now includes node info

#### Ergo Registrar (Framework-provided)
**Purpose**: Discover which nodes are available and where applications run

```go
// Built-in registrar capabilities
registrar, _ := node.Network().Registrar()
resolver := registrar.Resolver()

// Find all nodes in cluster
nodes, _ := registrar.Nodes()  // ["agent@host1", "aws-plugin@host1", "azure-plugin@host2"]

// Find where an application runs
routes, _ := resolver.ResolveApplication(gen.Atom("aws-plugin"))
// Returns: ApplicationRoute{Name: "aws-plugin", Node: "aws-plugin@host1"}

// Resolve connection info for a node
routes, _ := resolver.Resolve(gen.Atom("aws-plugin@host1"))
// Returns: Route{Host: "host1", Port: 15000, TLS: false}
```

**We use for**: Discovering plugin nodes if they're on different hosts

#### PluginCoordinator Actor (New)
**Purpose**: Plugin-specific lifecycle management and routing

```go
type PluginCoordinator struct {
    act.Actor

    // Plugin process management
    plugins       map[string]*PluginInfo     // namespace → plugin info
    registrar     gen.Registrar              // Service discovery

    // Configuration
    pluginConfigs map[string]PluginConfig    // From formae.conf.pkl
}

// Key responsibilities
func (p *PluginCoordinator) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
    switch req := request.(type) {

    // 1. Provide plugin operator PIDs
    case GetPluginOperator:
        pluginInfo := p.plugins[req.Namespace]
        return gen.ProcessID{
            Name: actornames.PluginOperator(req.ResourceURI, req.Operation, req.OperationID),
            Node: gen.Atom(pluginInfo.nodeName),
        }, nil

    // 2. Health checks
    case GetPluginStatus:
        return p.plugins[req.Namespace].healthy, nil

    // 3. Plugin discovery
    case ListPlugins:
        var list []string
        for ns := range p.plugins {
            list = append(list, ns)
        }
        return list, nil
    }
}

// Lifecycle management
func (p *PluginCoordinator) HandleMessage(from gen.PID, message any) error {
    switch msg := message.(type) {
    case meta.MessagePortExit:
        // Plugin died - restart it
        p.restartPlugin(msg.Tag)
    }
}
```

**Why we need PluginCoordinator**:
- Manages plugin-specific concerns (config, health, restart)
- Abstracts plugin location from ResourceUpdater
- Single place to query "which plugins are available?"
- Handles plugin configuration (binary paths, env vars, etc.)

### Flow Example

```
┌─────────────────────────────────────────────────────────────────┐
│ ResourceUpdater wants to create AWS resource                    │
└─────────────────────┬───────────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────────┐
│ 1. Call PluginCoordinator.GetPluginOperator("AWS", uri, op, id) │
│    → Returns gen.ProcessID{                                     │
│         Name: actornames.PluginOperator(...),  ← actornames pkg │
│         Node: "aws-plugin@localhost"           ← PluginCoord    │
│       }                                                          │
└─────────────────────┬───────────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────────┐
│ 2. Send message to remote ProcessID                             │
│    → Ergo network stack handles routing                         │
│    → Registrar resolves "aws-plugin@localhost" if needed        │
└─────────────────────┬───────────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────────┐
│ 3. Message arrives at PluginOperator on aws-plugin node         │
│    → PluginOperator executes CRUD operation                     │
│    → Returns ProgressResult                                     │
└─────────────────────────────────────────────────────────────────┘
```

### Do We Need All Three? **Yes**

- **actornames**: Local naming scheme (refactor to include node)
- **Registrar**: Network-level discovery (use framework's built-in)
- **PluginCoordinator**: Plugin lifecycle + business logic (new actor)

---

## 3. Registry Interaction Details

### Question
> Be more specific on how we interact with the registry. How does resolving remote nodes work? Where do routes come into play? How does this interact with our network plugin?

### Detailed Flow

#### Initial Setup: Agent Starts

```go
// metastructure.go - Agent node startup
func (m *Metastructure) Start() error {
    m.options.Network.Mode = gen.NetworkModeEnabled  // ← Changed from Disabled
    m.options.Network.Cookie = cfg.Agent.Server.Secret

    // Use built-in registrar (no custom registrar needed initially)
    // m.options.Network.Registrar = <default>

    node, err := ergo.StartNode(gen.Atom(m.nodeName), m.options)
    // Node is now registered with built-in registrar

    // Spawn PluginCoordinator
    node.Spawn(gen.Atom("plugin-coordinator"), gen.ProcessOptions{}, PluginCoordinator{})
}
```

#### Plugin Process Starts

```go
// plugins/aws/main.go - Plugin binary entry point
func main() {
    agentNode := os.Getenv("FORMAE_AGENT_NODE")  // "agent@hostname"
    pluginNode := os.Getenv("FORMAE_PLUGIN_NODE") // "aws-plugin@hostname"
    cookie := os.Getenv("FORMAE_NETWORK_COOKIE")

    options := gen.NodeOptions{}
    options.Network.Mode = gen.NetworkModeEnabled
    options.Network.Cookie = cookie  // ← SAME cookie as agent

    node, _ := ergo.StartNode(gen.Atom(pluginNode), options)
    // Plugin node is now registered with built-in registrar
}
```

#### Remote Node Resolution

**Scenario 1: Nodes on Same Host (default)**

```go
// ResourceUpdater calls PluginCoordinator
remotePID := gen.ProcessID{
    Name: "plugin-operator-xyz",
    Node: "aws-plugin@localhost",  // ← Same host
}

// Send message
proc.Call(remotePID, CreateResource{...})

// What happens internally:
// 1. Ergo checks: "Do I have a connection to aws-plugin@localhost?"
// 2. If no: Query registrar for connection info
// 3. Registrar returns: Route{Host: "localhost", Port: <assigned-port>}
// 4. Establish TCP connection
// 5. Send message over connection
```

**Registrar Query**:
```go
// Internal to Ergo (we don't call this directly)
registrar, _ := node.Network().Registrar()
resolver := registrar.Resolver()

routes, _ := resolver.Resolve(gen.Atom("aws-plugin@localhost"))
// Returns: []Route{
//     {Host: "localhost", Port: 45123, TLS: false}
// }
```

**Scenario 2: Nodes on Different Hosts (future)**

```go
// Plugin on different host
remotePID := gen.ProcessID{
    Name: "plugin-operator-xyz",
    Node: "aws-plugin@192.168.1.100",  // ← Different host
}

// Registrar resolves
routes, _ := resolver.Resolve(gen.Atom("aws-plugin@192.168.1.100"))
// Returns: []Route{
//     {Host: "192.168.1.100", Port: 15000, TLS: false}
// }

// Or use static routes for firewall scenarios
node.Network().AddRoute("aws-plugin@192.168.1.100", gen.NetworkRoute{
    Route: gen.Route{Host: "192.168.1.100", Port: 15000},
    Cookie: "shared-secret",
}, 100 /* weight */)
```

#### Routes Explained

**Routes** = connection information for reaching a node

```go
type Route struct {
    Host             string   // Hostname or IP
    Port             uint16   // TCP port
    TLS              bool     // Use TLS?
    HandshakeVersion Version
    ProtoVersion     Version
}
```

**Three ways routes are used**:

1. **Automatic (built-in registrar)**:
   - Node registers itself: "I'm aws-plugin@localhost on port 45123"
   - Other nodes query registrar when connecting
   - Works for same-host or LAN without firewall

2. **Static routes (explicit configuration)**:
   ```go
   // For production: plugins behind firewall
   node.Network().AddRoute("aws-plugin@internal-host", gen.NetworkRoute{
       Route: gen.Route{Host: "10.0.1.50", Port: 15000, TLS: true},
   })
   ```

3. **etcd registrar (multi-agent clusters)**:
   ```go
   // All nodes register in etcd
   options.Network.Registrar = etcd.Create(etcdOptions)
   // Dynamic discovery across cloud regions
   ```

#### Network Plugin Interaction

**Question**: How does this interact with our network plugin (Tailscale)?

**Answer**: **Different layers**

| Layer | Purpose | Example |
|-------|---------|---------|
| **Network Plugin** (Tailscale) | HTTP client for API calls | Agent → Cloud Provider API |
| **Ergo Network** | Actor communication | Agent actors ↔ Plugin actors |

**Network Plugin (Tailscale)**:
```go
// Used by CLI/Agent for making HTTP requests
netPlugin := pluginManager.NetworkPlugin(config)
httpClient := (*netPlugin).Client(config)

// Agent uses this to call cloud provider APIs
// (But this is INSIDE the plugin, not for actor communication)
```

**Ergo Network**:
```go
// Used for inter-actor communication
// Agent node ↔ Plugin nodes
// Uses TCP connections (not HTTP)
// Can use Tailscale network if nodes are on Tailscale network
```

**They don't conflict**:
- Ergo's network stack handles actor messages over TCP
- Network plugin handles HTTP client customization
- Both can coexist (e.g., Ergo over Tailscale network)

**If you want Ergo to use Tailscale network**:
```go
// Plugin nodes and agent on same Tailscale network
// Just use Tailscale IPs in node names
options := gen.NodeOptions{}
node, _ := ergo.StartNode("aws-plugin@100.64.1.5", options)  // Tailscale IP
```

---

## 4. Configuration: PKL vs Environment Variables

### Question
> You are passing env vars to the plugin process. Ergo allows for sharing env vars during RemoteSpawn operations. The agent config right now is a PKL config file in ~/.config/formae/formae.conf.pkl.

### Answer: **Hybrid approach - PKL for agent, env vars for plugins**

#### Current State

**Agent Configuration** (`~/.config/formae/formae.conf.pkl`):
```pkl
// Config.pkl
class Config {
    agent: Agent
    plugins: Plugins
}

class Agent {
    server: Server
    datastore: Datastore
    // ...
}

class Plugins {
    authentication: Authentication
    network: Network
    resources: Listing<ResourceConfig>  // ← New: plugin configs
}

// New in this architecture
class ResourceConfig {
    namespace: String    // "AWS", "Azure"
    enabled: Boolean
    binaryPath: String   // "/opt/formae/plugins/aws-plugin"
    environment: Mapping<String, String>  // Plugin-specific env vars
}
```

**Example Configuration**:
```pkl
// formae.conf.pkl
amends "Config.pkl"

agent {
    server {
        nodename = "agent"
        hostname = "localhost"
        secret = "my-secret-cookie"
    }
}

plugins {
    resources {
        new {
            namespace = "AWS"
            enabled = true
            binaryPath = "/opt/formae/plugins/aws-plugin"
            environment {
                ["AWS_REGION"] = "us-east-1"
                ["AWS_PROFILE"] = "default"
            }
        }
        new {
            namespace = "Azure"
            enabled = true
            binaryPath = "/opt/formae/plugins/azure-plugin"
            environment {
                ["AZURE_SUBSCRIPTION_ID"] = "xxx-yyy-zzz"
            }
        }
    }
}
```

#### Plugin Spawning Flow

**Agent reads PKL config**:
```go
// internal/agent/agent.go
func (a *Agent) Start() error {
    // Load PKL config (existing)
    config := loadConfig("~/.config/formae/formae.conf.pkl")

    // Pass to metastructure (existing)
    meta, _ := metastructure.NewMetastructure(ctx, config, pluginManager, agentID)

    // Metastructure starts, spawns PluginCoordinator
    meta.Start()
}
```

**PluginCoordinator spawns plugins via meta.Port**:
```go
// internal/metastructure/plugin_coordinator.go
func (p *PluginCoordinator) Init(args ...any) error {
    // Get plugin configs from environment
    pluginConfigs := p.Env("PluginConfig").(*model.PluginsConfig)

    for _, resourceConfig := range pluginConfigs.Resources {
        if !resourceConfig.Enabled {
            continue
        }

        // Build environment variables for plugin process
        env := []string{
            fmt.Sprintf("FORMAE_AGENT_NODE=%s@%s",
                serverConfig.Nodename, serverConfig.Hostname),
            fmt.Sprintf("FORMAE_PLUGIN_NODE=%s-plugin@%s",
                strings.ToLower(resourceConfig.Namespace), serverConfig.Hostname),
            fmt.Sprintf("FORMAE_NETWORK_COOKIE=%s",
                serverConfig.Secret),
        }

        // Add plugin-specific env vars from PKL config
        for key, value := range resourceConfig.Environment {
            env = append(env, fmt.Sprintf("%s=%s", key, value))
        }

        // Spawn plugin via meta.Port
        portOptions := meta.PortOptions{
            Cmd:  resourceConfig.BinaryPath,
            Env:  env,
            Tag:  resourceConfig.Namespace,
            Process: p.PID(),
        }

        metaport, _ := meta.CreatePort(portOptions)
        pid, _ := p.SpawnMeta(metaport, gen.MetaOptions{})

        p.plugins[resourceConfig.Namespace] = &PluginInfo{
            namespace:   resourceConfig.Namespace,
            metaPortPID: pid,
            nodeName:    env[1], // Extract plugin node name
        }
    }
}
```

**Plugin binary reads environment**:
```go
// plugins/aws/main.go
func main() {
    // Read connection info from env
    agentNode := os.Getenv("FORMAE_AGENT_NODE")
    pluginNode := os.Getenv("FORMAE_PLUGIN_NODE")
    cookie := os.Getenv("FORMAE_NETWORK_COOKIE")

    // Read AWS-specific config from env
    region := os.Getenv("AWS_REGION")
    profile := os.Getenv("AWS_PROFILE")

    // Start Ergo node
    options := gen.NodeOptions{}
    options.Network.Cookie = cookie
    options.Env = map[gen.Env]any{
        gen.Env("AWS_REGION"): region,
        gen.Env("AWS_PROFILE"): profile,
    }

    node, _ := ergo.StartNode(gen.Atom(pluginNode), options)
}
```

#### Why Not RemoteSpawn?

**RemoteSpawn limitations**:
- Requires ExposeEnvRemoteSpawn flag (security concern)
- Only works for values that are EDF-encodable
- Plugin node must already be running

**meta.Port advantages**:
- Spawns the process (don't need it running beforehand)
- Standard environment variables (12-factor app pattern)
- Works with any external process
- No serialization concerns

### Configuration Hierarchy

```
┌─────────────────────────────────────────────────────────────┐
│ ~/.config/formae/formae.conf.pkl (PKL file)                 │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ agent.server.secret = "cookie"                          │ │
│ │ plugins.resources[0].namespace = "AWS"                  │ │
│ │ plugins.resources[0].binaryPath = "/opt/.../aws-plugin" │ │
│ │ plugins.resources[0].environment["AWS_REGION"] = "..."  │ │
│ └─────────────────────────────────────────────────────────┘ │
└──────────────────────────┬──────────────────────────────────┘
                           │ Parsed by agent
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ Agent Process - model.Config struct                         │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ Injected into Ergo Env                                  │ │
│ │ gen.Env("PluginConfig") = cfg.Plugins                   │ │
│ └─────────────────────────────────────────────────────────┘ │
└──────────────────────────┬──────────────────────────────────┘
                           │ Accessed by PluginCoordinator
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ PluginCoordinator Actor                                     │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ Spawns plugins via meta.Port with env vars:             │ │
│ │ - FORMAE_AGENT_NODE=agent@localhost                     │ │
│ │ - FORMAE_NETWORK_COOKIE=cookie                          │ │
│ │ - AWS_REGION=us-east-1                                  │ │
│ └─────────────────────────────────────────────────────────┘ │
└──────────────────────────┬──────────────────────────────────┘
                           │ Environment passed to process
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ Plugin Process (aws-plugin binary)                          │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ os.Getenv("FORMAE_AGENT_NODE")                          │ │
│ │ os.Getenv("AWS_REGION")                                 │ │
│ └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

---

## 5. Plugin Binary Discovery and Versioning

### Question
> We shouldn't have to hard-code the path to the plugin binary. We should load them from a known location, e.g., ~/.pel/formae/plugins. Let's think about versions as well.

### Answer: **Hierarchical lookup with version support**

#### Plugin Discovery Strategy

**Search Paths** (in order):
1. **Explicit path in PKL config** (if provided)
2. **User plugins**: `~/.pel/formae/plugins/{namespace}/{version}/{namespace}-plugin`
3. **System plugins**: `/opt/formae/plugins/{namespace}/{version}/{namespace}-plugin`
4. **Bundled plugins**: `{agent-binary-dir}/../plugins/{namespace}/{version}/{namespace}-plugin`

**Directory Structure**:
```
~/.pel/formae/plugins/
├── aws/
│   ├── 1.0.0/
│   │   ├── aws-plugin              (binary)
│   │   ├── aws-plugin.json         (manifest)
│   │   └── checksums.txt
│   ├── 1.1.0/
│   │   ├── aws-plugin
│   │   ├── aws-plugin.json
│   │   └── checksums.txt
│   └── current -> 1.1.0/           (symlink to default version)
├── azure/
│   ├── 0.1.0/
│   │   ├── azure-plugin
│   │   └── azure-plugin.json
│   └── current -> 0.1.0/
└── plugins.lock                     (version lock file)
```

#### Plugin Manifest Format

**{namespace}-plugin.json**:
```json
{
  "name": "aws",
  "namespace": "AWS",
  "version": "1.1.0",
  "api_version": "v1",
  "formae_min_version": "0.4.0",
  "formae_max_version": "0.6.x",
  "capabilities": [
    "create",
    "read",
    "update",
    "delete",
    "list",
    "status"
  ],
  "supported_resources": [
    "AWS::S3::Bucket",
    "AWS::EC2::Instance",
    "AWS::RDS::DBInstance"
  ],
  "binary": "aws-plugin",
  "checksum": "sha256:abcd1234...",
  "release_date": "2025-11-15",
  "changelog_url": "https://github.com/formae/plugin-aws/releases/tag/v1.1.0"
}
```

#### Configuration in PKL

```pkl
// formae.conf.pkl
plugins {
    resources {
        new {
            namespace = "AWS"
            enabled = true
            version = "1.1.0"              // ← Specific version
            // OR
            // version = "current"         // Use symlink
            // OR
            // binaryPath = "/custom/path"  // Explicit path (skip discovery)

            environment {
                ["AWS_REGION"] = "us-east-1"
            }
        }
    }
}
```

#### Plugin Discovery Implementation

```go
// pkg/plugin/discovery.go (new file)
package plugin

type PluginDiscovery struct {
    searchPaths []string
}

func NewPluginDiscovery() *PluginDiscovery {
    homeDir, _ := os.UserHomeDir()
    execPath, _ := os.Executable()

    return &PluginDiscovery{
        searchPaths: []string{
            filepath.Join(homeDir, ".pel", "formae", "plugins"),
            "/opt/formae/plugins",
            filepath.Join(filepath.Dir(execPath), "..", "plugins"),
        },
    }
}

func (pd *PluginDiscovery) FindPlugin(namespace, version string) (*PluginInfo, error) {
    for _, basePath := range pd.searchPaths {
        pluginDir := filepath.Join(basePath, strings.ToLower(namespace))

        // Resolve version (could be "current", "1.0.0", "latest")
        versionPath, err := pd.resolveVersion(pluginDir, version)
        if err != nil {
            continue  // Try next search path
        }

        // Load manifest
        manifestPath := filepath.Join(versionPath, fmt.Sprintf("%s-plugin.json", strings.ToLower(namespace)))
        manifest, err := loadManifest(manifestPath)
        if err != nil {
            continue
        }

        // Verify compatibility
        if err := pd.checkCompatibility(manifest); err != nil {
            return nil, fmt.Errorf("incompatible plugin version: %w", err)
        }

        // Verify checksum
        binaryPath := filepath.Join(versionPath, manifest.Binary)
        if err := verifyChecksum(binaryPath, manifest.Checksum); err != nil {
            return nil, fmt.Errorf("checksum mismatch: %w", err)
        }

        return &PluginInfo{
            Namespace:   manifest.Namespace,
            Version:     manifest.Version,
            BinaryPath:  binaryPath,
            Manifest:    manifest,
        }, nil
    }

    return nil, fmt.Errorf("plugin not found: %s@%s", namespace, version)
}

func (pd *PluginDiscovery) resolveVersion(pluginDir, version string) (string, error) {
    switch version {
    case "current", "":
        // Follow symlink
        return filepath.EvalSymlinks(filepath.Join(pluginDir, "current"))

    case "latest":
        // Find highest version number
        versions, _ := filepath.Glob(filepath.Join(pluginDir, "*"))
        // ... semver parsing and comparison
        return highestVersion, nil

    default:
        // Specific version
        versionPath := filepath.Join(pluginDir, version)
        if _, err := os.Stat(versionPath); err != nil {
            return "", err
        }
        return versionPath, nil
    }
}

func (pd *PluginDiscovery) checkCompatibility(manifest *PluginManifest) error {
    // Check Formae version
    formaeVersion := semver.MustParse(constants.Version)

    minVersion, _ := semver.NewConstraint(manifest.FormaeMinVersion)
    if !minVersion.Check(formaeVersion) {
        return fmt.Errorf("requires formae >= %s, have %s",
            manifest.FormaeMinVersion, formaeVersion)
    }

    if manifest.FormaeMaxVersion != "" {
        maxVersion, _ := semver.NewConstraint(manifest.FormaeMaxVersion)
        if !maxVersion.Check(formaeVersion) {
            return fmt.Errorf("requires formae <= %s, have %s",
                manifest.FormaeMaxVersion, formaeVersion)
        }
    }

    // Check API version
    if manifest.APIVersion != constants.PluginAPIVersion {
        return fmt.Errorf("incompatible API version: %s (need %s)",
            manifest.APIVersion, constants.PluginAPIVersion)
    }

    return nil
}
```

#### PluginCoordinator Integration

```go
func (p *PluginCoordinator) Init(args ...any) error {
    pluginConfigs := p.Env("PluginConfig").(*model.PluginsConfig)
    discovery := plugin.NewPluginDiscovery()

    for _, config := range pluginConfigs.Resources {
        if !config.Enabled {
            continue
        }

        var binaryPath string

        if config.BinaryPath != "" {
            // Explicit path provided
            binaryPath = config.BinaryPath
        } else {
            // Discover plugin
            pluginInfo, err := discovery.FindPlugin(config.Namespace, config.Version)
            if err != nil {
                return fmt.Errorf("failed to find plugin %s: %w", config.Namespace, err)
            }
            binaryPath = pluginInfo.BinaryPath

            p.Log().Info("Discovered plugin",
                "namespace", config.Namespace,
                "version", pluginInfo.Version,
                "path", binaryPath)
        }

        // Spawn plugin process
        // ... (meta.Port code from earlier)
    }
}
```

#### Version Lock File

**plugins.lock** (generated, similar to package-lock.json):
```json
{
  "version": "1",
  "generated": "2025-11-18T10:30:00Z",
  "plugins": {
    "AWS": {
      "version": "1.1.0",
      "resolved": "/home/user/.pel/formae/plugins/aws/1.1.0/aws-plugin",
      "checksum": "sha256:abcd1234...",
      "api_version": "v1"
    },
    "Azure": {
      "version": "0.1.0",
      "resolved": "/home/user/.pel/formae/plugins/azure/0.1.0/azure-plugin",
      "checksum": "sha256:xyz789...",
      "api_version": "v1"
    }
  }
}
```

**Purpose**:
- Reproducible deployments (lock to specific versions)
- Detect when plugins need updating
- CI/CD validation

---

## 6. FakeAWS and Workflow Tests

### Question
> All our workflow_tests are currently using the FakeAWS plugin. We override the behavior before the tests run. Let's make sure we have a plan to keep those tests working with the new plugin system (I'd imagine we have to start 2 nodes now).

### Current Testing Mechanism

**How it works today**:
```go
// internal/workflow_tests/test_helpers/test_helpers.go
func NewTestMetastructure(t *testing.T, overrides *plugin.ResourcePluginOverrides) (*Metastructure, func(), error) {
    // Create context with overrides
    ctx, cancel := testutil.PluginOverridesContext(overrides)

    // Load FakeAWS plugin (in-process .so file)
    pluginManager := plugin.NewManager()
    pluginManager.Load()  // Loads fake-aws.so

    // Start metastructure (single node)
    m, _ := metastructure.NewMetastructureWithDataStoreAndContext(ctx, cfg, pluginManager, db, "test")
    m.Start()
}
```

**FakeAWS checks context for overrides**:
```go
// plugins/fake-aws/fake_aws.go
func (s FakeAWS) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
    // Check for test override
    if overrides := s.overrides(ctx); overrides != nil && overrides.Create != nil {
        return overrides.Create(request)
    }

    // Default behavior (in-memory store)
    // ...
}

func (s FakeAWS) overrides(ctx context.Context) *plugin.ResourcePluginOverrides {
    if f, ok := ctx.Value(plugin.ResourcePluginOverridesContextKey).(*plugin.ResourcePluginOverrides); ok {
        return f
    }
    return nil
}
```

### Challenge with Distributed Architecture

**Problem**: Overrides are in context, context is in agent process, plugin is in separate process

**Solutions**:

#### Option A: Keep FakeAWS In-Process (Recommended for Phase 1)

**Approach**: Have a special mode for testing where FakeAWS loads as in-process plugin

```go
// internal/workflow_tests/test_helpers/test_helpers.go
func NewTestMetastructure(t *testing.T, overrides *plugin.ResourcePluginOverrides) (*Metastructure, func(), error) {
    ctx, cancel := testutil.PluginOverridesContext(overrides)

    // Create plugin manager with test mode
    pluginManager := plugin.NewManager()

    // CHANGE: Load FakeAWS in-process, other plugins externally
    pluginManager.LoadInProcess("fake-aws")  // Load .so file
    // pluginManager.LoadExternal("aws")     // Would spawn process (skip for tests)

    // Rest stays the same
    m, _ := metastructure.NewMetastructureWithDataStoreAndContext(ctx, cfg, pluginManager, db, "test")
}
```

**Plugin Manager Enhancement**:
```go
// pkg/plugin/manager.go
func (m *Manager) LoadInProcess(namespaces ...string) {
    // Load specific plugins as .so files (current mechanism)
    for _, path := range m.pluginPaths {
        matches, _ := filepath.Glob(path + "/*.so")
        for _, soPath := range matches {
            if shouldLoadInProcess(soPath, namespaces) {
                dl, _ := goplugin.Open(soPath)
                lookup, _ := dl.Lookup("Plugin")
                // ... existing logic
            }
        }
    }
}

func (m *Manager) LoadExternal(namespaces ...string) {
    // Spawn plugins as separate processes
    // ... (PluginCoordinator logic)
}
```

**Benefits**:
- ✅ Minimal changes to existing tests
- ✅ Fast test execution (no process spawning overhead)
- ✅ Context-based overrides still work
- ✅ Tests real plugin code, just in-process

**Drawbacks**:
- ⚠️ Doesn't test distributed architecture
- ⚠️ Still has .so dependency conflicts (but only FakeAWS, which is simple)

#### Option B: Two-Node Test Setup (Full Integration)

**Approach**: Spawn FakeAWS as external process, pass overrides via HTTP or shared state

```go
// internal/workflow_tests/test_helpers/test_helpers.go
func NewTestMetastructure(t *testing.T, overrides *plugin.ResourcePluginOverrides) (*Metastructure, func(), error) {
    // 1. Start mock override server
    overrideServer := startOverrideServer(t, overrides)
    defer overrideServer.Stop()

    // 2. Spawn FakeAWS plugin with override server URL
    fakeAWSProcess := spawnFakeAWSPlugin(t, overrideServer.URL)
    defer fakeAWSProcess.Kill()

    // 3. Wait for FakeAWS node to be ready
    waitForPluginNode(t, "fake-aws-plugin@localhost")

    // 4. Start agent
    m, _ := metastructure.NewMetastructureWithDataStoreAndContext(ctx, cfg, pluginManager, db, "test")
    m.Start()
}
```

**Mock Override Server**:
```go
// internal/workflow_tests/test_helpers/override_server.go
type OverrideServer struct {
    overrides *plugin.ResourcePluginOverrides
    server    *http.Server
}

func startOverrideServer(t *testing.T, overrides *plugin.ResourcePluginOverrides) *OverrideServer {
    mux := http.NewServeMux()
    s := &OverrideServer{overrides: overrides}

    mux.HandleFunc("/override/create", s.handleCreate)
    mux.HandleFunc("/override/read", s.handleRead)
    // ... etc

    server := &http.Server{Addr: ":0", Handler: mux}  // Random port
    go server.ListenAndServe()
    return s
}

func (s *OverrideServer) handleCreate(w http.ResponseWriter, r *http.Request) {
    var req resource.CreateRequest
    json.NewDecoder(r.Body).Decode(&req)

    // Call override
    result, err := s.overrides.Create(&req)

    // Return result
    json.NewEncoder(w).Encode(map[string]any{
        "result": result,
        "error": err,
    })
}
```

**FakeAWS Plugin Modification**:
```go
// plugins/fake-aws/fake_aws.go
func (s FakeAWS) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
    // Check for override server URL in env
    if overrideURL := os.Getenv("FORMAE_TEST_OVERRIDE_SERVER"); overrideURL != "" {
        // Call override server
        resp, _ := http.Post(overrideURL+"/override/create", "application/json", toJSON(request))
        var result struct {
            Result *resource.CreateResult
            Error  string
        }
        json.NewDecoder(resp.Body).Decode(&result)

        if result.Error != "" {
            return nil, errors.New(result.Error)
        }
        return result.Result, nil
    }

    // Default in-memory behavior
    // ...
}
```

**Benefits**:
- ✅ Tests actual distributed architecture
- ✅ No .so dependencies
- ✅ Validates inter-process communication

**Drawbacks**:
- ⚠️ More complex test setup
- ⚠️ Slower tests (process spawning)
- ⚠️ HTTP overhead for every operation

#### Option C: Hybrid Approach (Recommended Long-term)

**Phase 1** (MVP): Use Option A
- Keep FakeAWS in-process for existing tests
- Add NEW tests for distributed architecture validation

**Phase 2** (Production): Migrate to Option B
- Convert critical tests to two-node setup
- Keep fast in-process tests for unit testing
- Use distributed tests for integration testing

**Test Organization**:
```
internal/workflow_tests/
├── unit/                 (in-process FakeAWS, fast)
│   ├── resource_updater_test.go
│   ├── changeset_executor_test.go
│   └── ...
└── integration/          (distributed, slower)
    ├── plugin_lifecycle_test.go
    ├── plugin_crash_recovery_test.go
    └── multi_plugin_test.go
```

### Recommended Approach

**For this implementation**:

1. **Phase 1**: Implement Option A
   - Add `LoadInProcess()` method to PluginManager
   - Keep all existing tests working with minimal changes
   - FakeAWS remains in-process, overrides work via context

2. **Phase 2**: Add distributed tests
   - Create new test suite using Option B approach
   - Test plugin crashes, restarts, network failures
   - Validate actor communication patterns

3. **Phase 3**: Migrate gradually
   - Convert critical tests to distributed setup
   - Keep fast unit tests in-process
   - Full integration tests use real distributed architecture

**Code Changes for Phase 1**:
```go
// pkg/plugin/manager.go
func (m *Manager) Load() {
    // Default: load all plugins in-process (.so files)
    m.LoadInProcess()
}

func (m *Manager) LoadInProcess(namespaces ...string) {
    // Existing Load() logic, optionally filtered by namespace
    // ...
}

func (m *Manager) LoadExternal(namespaces ...string) {
    // New: spawn plugins as external processes
    // Uses PluginCoordinator
    // ...
}

// Test helper stays mostly the same
func NewTestMetastructure(t *testing.T, overrides *plugin.ResourcePluginOverrides) (*Metastructure, func(), error) {
    ctx, cancel := testutil.PluginOverridesContext(overrides)
    pluginManager := plugin.NewManager()
    pluginManager.LoadInProcess("FakeAWS")  // ← Explicit in-process loading
    // ...
}
```

---

## Summary of Clarifications

| Question | Answer |
|----------|--------|
| **meta.Port capabilities** | ✅ Spawns processes + bidirectional IPC via stdin/stdout |
| **PluginCoordinator vs Registrar** | All needed: actornames (naming), Registrar (network discovery), Coordinator (lifecycle) |
| **Registry interaction** | Automatic via Ergo's network stack, manual via AddRoute() for firewall scenarios |
| **Configuration approach** | PKL for agent config, env vars for plugin processes (via meta.Port) |
| **Plugin discovery** | Hierarchical search in ~/.pel/formae/plugins/{namespace}/{version}/ with manifest validation |
| **Testing with FakeAWS** | Phase 1: Keep in-process with LoadInProcess(), Phase 2: Add distributed integration tests |

---

**Next Steps**: Address these clarifications in the implementation plan.
