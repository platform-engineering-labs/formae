# Distributed Plugin Architecture for Formae
## Research Document

**Date:** 2025-11-18
**Branch:** `feat/azure-plugin`
**Purpose:** Design a distributed actor-based plugin system to replace Go's plugin.Open() mechanism

---

## Executive Summary

This document proposes a fundamental architectural shift from Go's built-in plugin system (`.so` files loaded via `plugin.Open()`) to a **distributed actor-based plugin system** using the Ergo framework. This change addresses the binary compatibility issues that currently block Azure plugin development while positioning Formae for future requirements such as multi-cloud support, host agent capabilities, and high-availability clustering.

### Key Benefits

1. **Eliminates Dependency Hell**: Each plugin runs as a separate process with its own dependencies
2. **Enables True Multi-Cloud**: Different cloud provider SDKs can use conflicting dependencies
3. **Prepares for Future Architecture**: Aligns with the eventual host agent vision
4. **Improves Fault Isolation**: Plugin crashes don't bring down the agent
5. **Simplifies Development**: Plugin developers don't need to coordinate dependencies with core team
6. **Enables Hot Reload**: Plugins can be restarted without restarting the agent (future)

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Formae Agent                         │
│  ┌───────────────────────────────────────────────────┐  │
│  │         Ergo Node (agent@hostname)                │  │
│  │                                                   │  │
│  │  ┌──────────────┐  ┌──────────────┐             │  │
│  │  │ Changeset    │  │ Resource     │             │  │
│  │  │ Executor     │  │ Updater      │             │  │
│  │  └──────┬───────┘  └──────┬───────┘             │  │
│  │         │                 │                      │  │
│  │         └────────┬────────┘                      │  │
│  │                  ▼                               │  │
│  │          ┌──────────────┐                        │  │
│  │          │ Plugin       │                        │  │
│  │          │ Coordinator  │ (new component)        │  │
│  │          └──────┬───────┘                        │  │
│  └─────────────────┼────────────────────────────────┘  │
└────────────────────┼───────────────────────────────────┘
                     │ Network (Ergo)
        ┌────────────┼────────────┐
        │            │            │
        ▼            ▼            ▼
┌─────────────┐ ┌─────────────┐ ┌─────────────┐
│AWS Plugin   │ │Azure Plugin │ │K8S Plugin   │
│Process      │ │Process      │ │Process      │
│             │ │             │ │             │
│ Ergo Node   │ │ Ergo Node   │ │ Ergo Node   │
│ Plugin      │ │ Plugin      │ │ Plugin      │
│ Operator    │ │ Operator    │ │ Operator    │
│ Actor       │ │ Actor       │ │ Actor       │
└─────────────┘ └─────────────┘ └─────────────┘
```

---

## Current Architecture Analysis

### How Plugins Work Today

**Plugin Manager** (`pkg/plugin/manager.go`):
- Discovers `.so` files in plugin directories
- Loads plugins via `plugin.Open(path)`
- Type-casts exported `Plugin` symbol to appropriate interface
- Stores plugin references in typed slices

**Plugin Types**:
1. **Resource Plugins**: AWS, Azure (in development)
2. **Schema Plugins**: PKL, JSON, YAML
3. **Network Plugins**: Tailscale
4. **Authentication Plugins**: Basic Auth

**PluginOperator Actor** (`internal/metastructure/plugin_operation/plugin_operator.go`):
- State machine actor that executes CRUD operations
- Receives operation request (Create/Read/Update/Delete/List/Status)
- Calls plugin method directly via `PluginManager.ResourcePlugin(namespace)`
- Returns `resource.ProgressResult` to caller (usually `ResourceUpdater`)
- Handles retries, timeouts, and long-running operations

**Current Flow** (simplified):
```
ResourceUpdater → PluginOperator → PluginManager.ResourcePlugin("AWS")
                                    → (*plugin).Create(ctx, request)
                                    ← ProgressResult
```

### Critical Constraint: Binary Compatibility

Go's plugin system requires **exact dependency version matching** across:
- Main formae binary
- All loaded plugins
- All transitive dependencies

**Example Failure**:
```
panic: plugin.Open("plugins/azure/azure"):
plugin was built with a different version of package golang.org/x/net/idna
```

This is fundamentally unsustainable for a multi-cloud IaC tool.

### What Already Works Well

✅ **Message-Based Communication**: All actors communicate via serializable messages
✅ **No Shared Memory**: No pointers shared between actors
✅ **Environment Injection**: Configuration passed via `gen.NodeOptions.Env`
✅ **Supervision Trees**: Robust error handling and recovery
✅ **Actor Isolation**: Clean boundaries between components

**Critical Insight**: The codebase is already architected for distribution. We just need to enable Ergo's networking capabilities and restructure plugin loading.

---

## Proposed Architecture

### Core Principles

1. **One Binary Per Plugin**: Each resource plugin is a standalone executable
2. **Ergo Network Transparency**: Use Ergo's built-in distributed actor capabilities
3. **Plugin Coordinator**: New actor responsible for plugin lifecycle management
4. **Remote PluginOperator**: PluginOperator actors run in plugin processes, not agent
5. **Backward Compatibility**: Schema/Auth/Network plugins remain in-process initially

### Architecture Components

#### 1. Plugin Coordinator Actor (New)

**Responsibility**: Manages the lifecycle of plugin processes

**Location**: Runs in the agent's Ergo node

**Capabilities**:
- Spawns plugin processes on startup (via `os/exec` or `meta.Port`)
- Monitors plugin process health
- Handles plugin crashes and restarts
- Registers plugin services in service registry
- Provides plugin discovery to other actors

**Message Interface**:
```go
// Incoming messages
type GetPluginOperator struct {
    Namespace   string  // "AWS", "Azure", etc.
    ResourceURI string
    Operation   string
    OperationID string
}

type ShutdownPlugin struct {
    Namespace string
}

type ListPlugins struct{}

// Outgoing messages
type PluginOperatorRef struct {
    PID         gen.ProcessID  // Remote PID on plugin node
    Namespace   string
}

type PluginStatus struct {
    Namespace string
    NodeName  string
    Healthy   bool
}
```

**Implementation Pattern**:
```go
type PluginCoordinator struct {
    act.Actor

    pluginProcesses map[string]*PluginProcess  // namespace → process
    registrar       gen.Registrar              // Service discovery
}

type PluginProcess struct {
    namespace   string
    nodeName    string
    cmd         *exec.Cmd
    pid         int
    healthy     bool
    lastSeen    time.Time
}

func (p *PluginCoordinator) Init(args ...any) error {
    // Read plugin configuration
    // Spawn plugin processes
    // Register with service discovery
    // Link to plugin nodes for crash detection
}

func (p *PluginCoordinator) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
    switch req := request.(type) {
    case GetPluginOperator:
        // Look up plugin node
        // Return remote PluginOperator PID
        return PluginOperatorRef{
            PID: gen.ProcessID{
                Name: actornames.PluginOperator(req.ResourceURI, req.Operation, req.OperationID),
                Node: p.pluginProcesses[req.Namespace].nodeName,
            },
            Namespace: req.Namespace,
        }, nil
    }
}
```

#### 2. Plugin Binary Structure

**Each plugin becomes a standalone executable**:

```
plugins/aws/
  ├── main.go              (new - entry point)
  ├── plugin_operator.go   (moved from metastructure)
  ├── aws.go               (existing - plugin implementation)
  ├── pkg/...              (existing - SDK wrappers)
  └── go.mod               (independent dependencies)
```

**Plugin Main Entry Point**:
```go
// plugins/aws/main.go
package main

import (
    "context"
    "ergo.services/ergo"
    "ergo.services/ergo/gen"

    "formae.io/formae/plugins/aws"
)

func main() {
    // Read configuration from env or flags
    agentNode := os.Getenv("FORMAE_AGENT_NODE")  // "agent@hostname"
    pluginNode := os.Getenv("FORMAE_PLUGIN_NODE") // "aws-plugin@hostname"
    cookie := os.Getenv("FORMAE_NETWORK_COOKIE")

    // Configure Ergo node
    options := gen.NodeOptions{}
    options.Network.Cookie = cookie
    options.Network.Mode = gen.NetworkModeEnabled

    // Inject plugin-specific environment
    options.Env = map[gen.Env]any{
        gen.Env("Plugin"): aws.Plugin,  // The actual AWS plugin implementation
        gen.Env("Context"): context.Background(),
    }

    // Start node
    node, err := ergo.StartNode(gen.Atom(pluginNode), options)
    if err != nil {
        log.Fatal(err)
    }

    // Spawn application that manages PluginOperator instances
    node.Spawn("plugin-app", gen.ProcessOptions{}, PluginApplication{})

    // Wait for shutdown signal
    <-signals.Wait()
    node.Stop()
}
```

**Plugin Application** (supervises PluginOperator instances):
```go
type PluginApplication struct {
    act.Supervisor
}

func (p *PluginApplication) Init(args ...any) (act.SupervisorSpec, error) {
    return act.SupervisorSpec{
        Type: act.SupervisorTypeSimpleOneForOne,  // Dynamic children
        Children: []act.SupervisorChildSpec{
            {
                Factory: NewPluginOperator,  // Reuse existing implementation
            },
        },
        Restart: act.SupervisorRestart{
            Strategy:  act.SupervisorStrategyTransient,
            Intensity: 5,
            Period:    10,
        },
    }, nil
}
```

#### 3. Modified ResourceUpdater Flow

**Before**:
```go
// ResourceUpdater calls PluginOperator locally
proc.Call(
    pluginOperatorSupervisorProcess(proc),
    plugin_operation.EnsurePluginOperator{...},
)

result := proc.CallWithTimeout(
    pluginOperatorProcess(proc, uri, op, opID),
    operation,
    timeout,
)
```

**After**:
```go
// ResourceUpdater calls PluginCoordinator to get remote PID
coordinatorPID := gen.ProcessID{
    Name: "plugin-coordinator",
    Node: proc.Node().Name(),  // Local to agent
}

pluginRef, err := proc.Call(coordinatorPID, GetPluginOperator{
    Namespace:   namespace,
    ResourceURI: uri,
    Operation:   op,
    OperationID: opID,
})

ref := pluginRef.(PluginOperatorRef)

// Call remote PluginOperator directly
result, err := proc.CallWithTimeout(
    ref.PID,  // Points to plugin process
    operation,
    timeout,
)
```

**Key Changes**:
- Indirection through PluginCoordinator instead of PluginOperatorSupervisor
- Remote PID instead of local PID
- Network communication handled transparently by Ergo

#### 4. Configuration & Discovery

**Plugin Discovery Options**:

**Option A: Built-in Registrar** (Simplest)
- Use Ergo's default registrar
- Plugins register on startup
- Agent queries registrar for available plugins
- No external dependencies

**Option B: etcd Registrar** (Production-ready)
- Deploy etcd cluster alongside agent
- Plugins auto-register with hierarchical keys
- Support for dynamic configuration updates
- Better for multi-agent deployments

**Recommended**: Start with Option A, migrate to Option B for production clusters

**Configuration Sharing**:

**Agent Configuration** (`formae.yaml`):
```yaml
agent:
  plugins:
    resource:
      aws:
        enabled: true
        binary: /opt/formae/plugins/aws-plugin
        node_name: aws-plugin@localhost
        env:
          AWS_REGION: us-east-1
      azure:
        enabled: true
        binary: /opt/formae/plugins/azure-plugin
        node_name: azure-plugin@localhost
        env:
          AZURE_SUBSCRIPTION_ID: xxx
```

**Plugin Receives**:
- Environment variables from agent config
- Network cookie for authentication
- Agent node name for registration
- Plugin-specific configuration (passed as Env or args)

#### 5. Process Lifecycle Management

**Supervisor Strategy**:

**For Plugin Processes**:
- Agent manages plugin processes via `os/exec.Cmd` or `meta.Port`
- PluginCoordinator links to plugin nodes
- On plugin crash: restart process, re-establish connection
- On repeated crashes: exponential backoff, alert user

**For PluginOperator Actors**:
- Within plugin process: supervised by PluginApplication
- Transient restart strategy (only restart on crash)
- Intensity limit to prevent restart loops

**Graceful Shutdown**:
1. Agent receives SIGTERM
2. PluginCoordinator sends shutdown messages to all plugins
3. Wait for plugins to drain in-flight operations
4. Kill plugin processes if timeout exceeded
5. Agent shuts down

**Implementation Pattern**:
```go
func (p *PluginCoordinator) Terminate(reason error) {
    // Send shutdown to all plugins
    for namespace, process := range p.pluginProcesses {
        shutdownPID := gen.ProcessID{
            Name: "plugin-app",
            Node: process.nodeName,
        }
        p.Send(shutdownPID, gen.MessageExit{Reason: "shutdown"})
    }

    // Wait with timeout
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    for _, process := range p.pluginProcesses {
        select {
        case <-process.cmd.Wait():
            // Clean shutdown
        case <-ctx.Done():
            // Force kill
            process.cmd.Process.Kill()
        }
    }
}
```

---

## Key Design Decisions

### Decision 1: Which Plugins to Externalize?

**Recommendation**: Start with **resource plugins only**

**Rationale**:

| Plugin Type | Externalize? | Reason |
|-------------|--------------|---------|
| Resource (AWS, Azure, K8S) | ✅ Yes | Primary source of dependency conflicts; heavy SDKs |
| Schema (PKL, JSON, YAML) | ❌ No (initially) | Lightweight; used by CLI; no conflicting deps |
| Network (Tailscale) | ⚠️ Maybe | Depends on whether it causes conflicts |
| Authentication | ❌ No | Lightweight; minimal dependencies |

**CLI Consideration**:
- CLI uses schema plugins for `eval`, `extract` commands
- CLI does not have an Ergo node
- Keeping schema plugins in-process avoids CLI complexity

**Future**: If schema plugins need provider-specific features (e.g., PKL modules per cloud), consider externalizing them too.

### Decision 2: PluginOperator Location

**Recommendation**: PluginOperator runs in plugin process

**Alternatives Considered**:

| Approach | Pros | Cons |
|----------|------|------|
| PluginOperator in agent, calls remote plugin methods | Minimal changes to existing code | Requires defining RPC interface; loses state machine benefits |
| PluginOperator in plugin, agent calls it remotely | **Keeps existing state machine logic; clean separation** | Need to move code between packages |
| Hybrid: thin proxy in agent | Avoids moving code | Added complexity; network calls for every message |

**Chosen**: Move PluginOperator to plugin process
- Preserves existing FSM logic
- Clean separation of concerns
- Plugin crashes don't affect agent state machines
- All plugin state lives with the plugin

### Decision 3: Service Discovery Mechanism

**Recommendation**: Start with built-in Registrar, add etcd support later

**Phase 1** (MVP):
- Use Ergo's default registrar
- Simple node discovery via `node.Network().GetNode(nodeName)`
- Node names configured statically in `formae.yaml`
- Suitable for single-agent deployments

**Phase 2** (Production):
- Deploy etcd cluster
- Use `ergo.services/registrar/etcd`
- Dynamic plugin registration
- Hierarchical configuration management
- Supports multi-agent HA deployments

**Benefits of Phased Approach**:
- Lower barrier to entry (no etcd required for development)
- Validates architecture before adding complexity
- Clear upgrade path

### Decision 4: Configuration Management

**Recommendation**: Environment variables + structured config in Env

**Plugin Configuration Sources** (priority order):
1. **Command-line flags** (e.g., `--agent-node`, `--cookie`)
2. **Environment variables** (e.g., `FORMAE_AGENT_NODE`, cloud credentials)
3. **Structured config in Env** (passed from agent to plugin node)

**Example**:
```go
// Agent passes to plugin process via env vars
FORMAE_AGENT_NODE=agent@localhost
FORMAE_PLUGIN_NODE=aws-plugin@localhost
FORMAE_NETWORK_COOKIE=secret123
AWS_REGION=us-east-1
AWS_PROFILE=default

// Plugin reads and injects into Ergo Env
options.Env = map[gen.Env]any{
    gen.Env("AgentNode"): os.Getenv("FORMAE_AGENT_NODE"),
    gen.Env("Region"): os.Getenv("AWS_REGION"),
    // ... plugin-specific config
}
```

**Benefits**:
- Standard 12-factor app pattern
- Easy integration with container orchestrators
- No shared config files to coordinate
- Secrets can be injected externally

### Decision 5: Plugin Versioning

**Recommendation**: Semantic versioning with compatibility checks

**Approach**:
1. Each plugin exports a version (already exists: `Version() *semver.Version`)
2. Agent declares required plugin API version
3. On plugin startup: handshake validates compatibility
4. Incompatible versions → agent refuses to use plugin

**Plugin Manifest** (in plugin binary or sidecar file):
```json
{
  "name": "aws",
  "version": "1.2.3",
  "api_version": "v1",
  "formae_min_version": "0.4.0",
  "capabilities": ["create", "read", "update", "delete", "list", "status"]
}
```

**Compatibility Matrix**:
- **Agent API v1** supports plugins with `api_version: "v1"`
- **Breaking change** → bump to v2, support both v1 and v2 plugins during transition
- **Plugin too old** → warning in logs, refuse to load
- **Plugin too new** → warning in logs, refuse to load

**Future**: Support multiple plugin versions simultaneously (blue/green plugin deployments)

### Decision 6: Error Handling & Network Failures

**Recommendation**: Leverage Ergo's link/monitor + application-level retry

**Network Failure Scenarios**:

| Scenario | Detection | Recovery |
|----------|-----------|----------|
| Plugin process crashes | PluginCoordinator linked to plugin node → receives exit signal | Restart plugin process; ResourceUpdater retries operation |
| Network partition | Call timeout after 60s | ResourceUpdater marks operation as failed; user can retry |
| Plugin hangs | Operation timeout in PluginOperator state machine | PluginOperator transitions to error state; supervision restarts actor |
| Agent crashes | Plugin loses connection to agent | Plugin shuts down gracefully (agent will restart it) |

**Implementation Pattern**:
```go
// In PluginCoordinator.Init()
for namespace, process := range p.pluginProcesses {
    p.LinkNode(gen.Atom(process.nodeName))
}

// In PluginCoordinator message handler
func (p *PluginCoordinator) HandleMessage(from gen.PID, message any) error {
    switch msg := message.(type) {
    case gen.MessageDown:
        // Plugin node died
        namespace := p.findNamespaceByNode(msg.Node)
        p.Log().Error("Plugin node died", "namespace", namespace, "reason", msg.Reason)

        // Restart plugin process
        go p.restartPlugin(namespace)
    }
}

// In ResourceUpdater operation flow
result, err := proc.CallWithTimeout(remotePID, operation, 60*time.Second)
if err == gen.ErrNoConnection {
    // Network failure or plugin died
    return &resource.ProgressResult{
        Status: resource.OperationStatusFailure,
        ErrorCode: resource.ErrorCodePluginUnavailable,
        ErrorMessage: "Plugin unavailable - connection lost",
    }, nil
}
```

---

## Implementation Considerations

### Phase 1: Foundation (Week 1-2)

**Goals**: Prove the architecture works with a single plugin

**Tasks**:
1. Enable Ergo networking in agent (`NetworkModeEnabled`)
2. Create PluginCoordinator actor skeleton
3. Create AWS plugin binary with main.go entry point
4. Move PluginOperator code to plugin package (share via internal pkg)
5. Update ResourceUpdater to call remote PluginOperator
6. Test: Create/Read/Update/Delete via distributed actors

**Success Criteria**:
- AWS plugin runs as separate process
- Agent can create AWS resources via remote PluginOperator
- Plugin crashes are detected and handled
- Performance is acceptable (< 10% overhead vs in-process)

### Phase 2: Azure Plugin (Week 2-3)

**Goals**: Validate multi-plugin architecture; solve original problem

**Tasks**:
1. Create Azure plugin binary
2. Update PluginCoordinator to manage multiple plugins
3. Test: Azure resources can be created independently of AWS
4. Verify: No dependency conflicts between AWS and Azure SDKs
5. Run plugin SDK test suite against Azure plugin

**Success Criteria**:
- Both AWS and Azure plugins run simultaneously
- No dependency conflicts
- Plugin SDK tests pass for Azure
- Discovery and synchronization work

### Phase 3: Production Hardening (Week 4-5)

**Goals**: Make it production-ready

**Tasks**:
1. Implement robust error handling and retries
2. Add health checks and monitoring
3. Implement graceful shutdown
4. Add metrics for plugin performance
5. Document plugin development guide
6. Add integration tests for distributed scenarios
7. Performance testing and optimization

**Success Criteria**:
- Plugin restarts don't lose data
- Shutdown is clean (no orphaned processes)
- Metrics show plugin health
- Documentation enables third-party plugin development

### Phase 4: Advanced Features (Future)

**Goals**: Leverage distributed architecture for new capabilities

**Potential Features**:
1. **Hot reload**: Restart plugin without restarting agent
2. **Plugin scaling**: Multiple instances of same plugin for parallelism
3. **Remote plugins**: Plugins on different physical hosts
4. **etcd integration**: Dynamic plugin discovery and configuration
5. **Host agent**: Use same pattern for host management
6. **Multi-agent clustering**: Multiple agents sharing plugin pool

---

## Challenges and Mitigations

### Challenge 1: Increased Complexity

**Risk**: Distributed systems are harder to debug than in-process code

**Mitigation**:
- ✅ Comprehensive logging with correlation IDs
- ✅ Ergo Observer for visualizing actor interactions
- ✅ Local development mode (all plugins in agent process for debugging)
- ✅ Integration tests that exercise distributed paths
- ✅ Clear error messages that include node information

### Challenge 2: Performance Overhead

**Risk**: Network calls slower than in-process function calls

**Analysis**:
- Ergo achieves ~5M msg/sec over network (vs 21M local)
- Resource operations are I/O bound (cloud API calls)
- Network overhead << cloud API latency

**Mitigation**:
- ✅ Benchmark to establish baseline
- ✅ Use local Unix sockets for same-host communication
- ✅ Connection pooling (Ergo does this automatically)
- ✅ Accept that this is a reasonable trade-off for benefits

**Expected Impact**: < 10% overhead, negligible compared to cloud API latency

### Challenge 3: Operational Complexity

**Risk**: More processes to manage, monitor, and deploy

**Mitigation**:
- ✅ Package plugins as container sidecars
- ✅ systemd units for traditional deployments
- ✅ Health checks and auto-restart
- ✅ Clear operational runbooks
- ✅ Metrics for plugin health

**Deployment Models**:
```yaml
# Docker Compose
version: '3'
services:
  formae-agent:
    image: formae/agent:latest
    depends_on:
      - aws-plugin
      - azure-plugin
    environment:
      - FORMAE_NETWORK_COOKIE=secret

  aws-plugin:
    image: formae/plugin-aws:latest
    environment:
      - FORMAE_AGENT_NODE=agent@formae-agent
      - FORMAE_NETWORK_COOKIE=secret

  azure-plugin:
    image: formae/plugin-azure:latest
    environment:
      - FORMAE_AGENT_NODE=agent@formae-agent
      - FORMAE_NETWORK_COOKIE=secret
```

### Challenge 4: Schema Plugin Architecture

**Risk**: CLI uses schema plugins but doesn't have Ergo node

**Options**:

**Option A**: Keep schema plugins in-process (recommended for MVP)
- Minimal changes to CLI
- Schema plugins are lightweight
- No immediate benefit from externalizing

**Option B**: CLI spawns Ergo node
- Consistent architecture
- More complex CLI initialization
- May impact CLI startup time

**Option C**: Schema plugins as HTTP services
- Language-agnostic
- Can be shared across agents
- Requires HTTP server in each plugin

**Recommendation**: Start with Option A, revisit if schema plugins become problematic

### Challenge 5: Message Size Limits

**Risk**: EDF has 2^32 byte limit; large resources might exceed this

**Analysis**:
- Typical resource properties: < 1MB
- PKL evaluations: < 10MB
- Very unlikely to hit limit

**Mitigation**:
- ✅ Monitor message sizes in production
- ✅ If needed: implement chunking for large payloads
- ✅ Or: use shared storage (S3) with references in messages

### Challenge 6: Type Registration

**Risk**: Custom types must be registered before connection

**Mitigation**:
- ✅ Register all message types in init() functions
- ✅ Shared package for message type definitions
- ✅ CI check that verifies type registration
- ✅ Clear error messages if unregistered type encountered

**Pattern**:
```go
// pkg/plugin/messages/types.go (shared package)
package messages

import "ergo.services/ergo/net/edf"

func init() {
    // Register all plugin message types
    edf.RegisterTypeOf(CreateResource{})
    edf.RegisterTypeOf(ReadResource{})
    edf.RegisterTypeOf(UpdateResource{})
    edf.RegisterTypeOf(DeleteResource{})
    edf.RegisterTypeOf(ProgressResult{})
    // ... etc
}
```

---

## Questions for Clarification

### Architecture Decisions

1. **Schema Plugin Strategy**: Should we externalize schema plugins in Phase 1, or wait until there's a proven need?
   - **Recommendation**: Wait. Focus on resource plugins where the pain is felt.

2. **etcd Requirement**: Should we require etcd from day one, or support built-in registrar initially?
   - **Recommendation**: Support both, default to built-in for simplicity.

3. **CLI Changes**: Are we willing to add Ergo node initialization to CLI if it becomes necessary?
   - **Recommendation**: Avoid if possible; CLI should remain lightweight.

### Plugin Development

4. **Shared Code**: How much code should be shared between agent and plugins?
   - **Recommendation**: Share message types, plugin interfaces, and utilities. Keep PluginOperator in plugin.

5. **Plugin Packaging**: Should plugins be separate repositories or monorepo?
   - **Recommendation**: Start in monorepo for velocity; extract later if third-party plugins emerge.

6. **Backward Compatibility**: Do we need to support old `.so` plugins during transition?
   - **Recommendation**: No. Clean break is acceptable for internal tool.

### Operations

7. **Deployment Model**: What's the preferred deployment model (containers, systemd, both)?
   - **Recommendation**: Both. Container-first, systemd for flexibility.

8. **Plugin Discovery**: Should plugins be auto-discovered or explicitly configured?
   - **Recommendation**: Explicit configuration for security and predictability.

9. **Resource Limits**: Should plugin processes have CPU/memory limits enforced?
   - **Recommendation**: Yes (via cgroups or container limits) to prevent resource exhaustion.

### Future Architecture

10. **Host Agent Alignment**: Does this plugin architecture align with the vision for host agents?
    - **Assumption**: Yes - host agents would be additional Ergo nodes with specialized actors.

11. **Multi-Agent Clustering**: Should we design for multiple agents sharing plugins?
    - **Recommendation**: Yes, but don't implement yet. Keep it in mind.

12. **Plugin Marketplace**: Could third parties develop plugins with this architecture?
    - **Recommendation**: Yes - that's a major benefit. Document the SDK well.

---

## Recommended Approach

### Immediate Next Steps (This Week)

1. **Validate Assumptions**:
   - Create POC: Simple Ergo app with remote actors
   - Measure network overhead for resource operations
   - Confirm message serialization works for all types

2. **Get Agreement**:
   - Review this document with team
   - Decide on scope (resource plugins only vs all plugins)
   - Agree on deployment model

3. **Plan Implementation**:
   - Break down Phase 1 into tasks
   - Create TODOs in TodoWrite
   - Estimate effort (2-4 weeks for Phase 1-2)

### Success Metrics

**Technical**:
- [ ] AWS and Azure plugins run simultaneously without conflicts
- [ ] Plugin crashes don't crash agent
- [ ] Performance overhead < 10% vs in-process
- [ ] All existing tests pass

**Operational**:
- [ ] Plugin restarts are automatic and logged
- [ ] Deployment is documented and reproducible
- [ ] Metrics show plugin health
- [ ] Third-party plugin development is feasible

**User-Facing**:
- [ ] No changes to user workflow (transparent)
- [ ] Better error messages (include plugin info)
- [ ] Faster plugin development cycle (no dependency coordination)

---

## Appendix: Code References

### Existing Code to Understand

| Component | File | Key Sections |
|-----------|------|--------------|
| PluginManager | `pkg/plugin/manager.go` | Load(), ResourcePlugin() |
| PluginOperator FSM | `internal/metastructure/plugin_operation/plugin_operator.go` | State machine, CRUD handlers |
| ResourceUpdater | `internal/metastructure/resource_update/resource_updater.go` | doPluginOperation() |
| Metastructure Init | `internal/metastructure/metastructure.go` | NewMetastructure(), Start() |
| Application Structure | `internal/metastructure/application.go` | Supervisor tree |
| Actor Names | `internal/metastructure/actornames/names.go` | Naming conventions |

### Ergo Documentation References

| Topic | URL |
|-------|-----|
| Network Stack | https://docs.ergo.services/networking/network-stack |
| Remote Spawn | https://docs.ergo.services/networking/remote-spawn-process |
| Links & Monitors | https://docs.ergo.services/basics/links-and-monitors |
| Supervision | https://docs.ergo.services/basics/supervision-tree |
| etcd Registrar | https://docs.ergo.services/extra-library/registrars/etcd-client |
| Network Transparency | https://docs.ergo.services/networking/network-transparency |
| Examples | https://github.com/ergo-services/examples |

### External Research

| Topic | URL |
|-------|-----|
| Ergo Framework | https://github.com/ergo-services/ergo |
| meta.Port Example | https://github.com/ergo-services/examples/port |
| Actor Model Best Practices | https://betterprogramming.pub/implementing-the-actor-model-in-golang-3579c2227b5e |

---

## Conclusion

Moving to a distributed actor-based plugin architecture is the right long-term decision for Formae. It solves the immediate Azure plugin problem while setting up the foundation for:

- Multi-cloud support without dependency hell
- Host agent capabilities
- High-availability clustering
- Third-party plugin ecosystem

The Ergo framework provides all the necessary primitives, and the current codebase is already well-architected for this transition (message-based, no pointer sharing, environment injection).

**Recommendation**: Proceed with **Phase 1** (Foundation) using **resource plugins only**, starting with AWS as the reference implementation. Validate the architecture, measure performance, and iterate before expanding to all plugin types.

**Estimated Effort**: 2-3 weeks for basic functionality, 4-5 weeks for production-ready implementation.

**Risk Level**: Medium (distributed systems complexity, but Ergo mitigates most risks)

**Value**: High (solves blocker, enables future architecture, improves developer experience)

---

**End of Research Document**
