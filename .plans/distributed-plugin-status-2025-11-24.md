# Distributed Plugin Implementation - Status Update

**Date:** 2025-11-24
**Branch:** `feat/distributed-plugins`
**Previous Session:** 2025-11-23 (Plugin startup passing)
**Current Session:** Phase 1 Complete - Architecture refactoring with all tests passing

---

## Current Status: Phase 1 Complete ✅

**Integration Test Status:** ✅ PASSING (0.85s)
**Critical Bug Fixed:** Actor name mismatch in meta.Port configuration
**Ready For:** Phase 2 - Plugin Announcement Implementation

See `.handoffs/2025-11-24-phase1-complete-distributed-plugins.md` for complete session details.

### What We Accomplished

#### 1. Plugin Discovery Refactoring (2025-11-24)

**Problem**: PluginCoordinator was duplicating plugin discovery logic that PluginManager already handled.

**Solution**: Clean separation of responsibilities following the actor pattern:
- **PluginManager** (`pkg/plugin/manager.go`): Discovers ALL plugins (in-process .so AND external binaries)
- **PluginCoordinator** (`internal/metastructure/plugin_coordinator/`): Spawns and manages plugin processes

**Changes Made**:

1. **Extended PluginManager**:
   ```go
   // pkg/plugin/manager.go

   type ResourcePluginInfo struct {
       Namespace  string
       Version    string
       BinaryPath string
   }

   type Manager struct {
       // ... existing fields ...
       externalResourcePlugins []ResourcePluginInfo  // NEW
   }

   func (m *Manager) Load() {
       // Scan for .so files (skip Resource type - handled externally)
       // ... existing logic, modified to skip Resource plugins ...

       // Discover external resource plugins
       m.discoverExternalResourcePlugins()  // NEW
   }

   func (m *Manager) ListExternalResourcePlugins() []ResourcePluginInfo {
       return m.externalResourcePlugins
   }
   ```

2. **Simplified PluginCoordinator**:
   ```go
   // internal/metastructure/plugin_coordinator/plugin_coordinator.go

   func (p *PluginCoordinator) Init(args ...any) error {
       // Fetch PluginManager from Env (like other actors)
       pluginManagerVal, _ := p.Env("PluginManager")
       pluginManager := pluginManagerVal.(*plugin.Manager)

       // Get discovered external plugins
       externalPlugins := pluginManager.ListExternalResourcePlugins()

       // Spawn each plugin process
       for _, info := range externalPlugins {
           p.spawnPlugin(info.Namespace, info)
       }
   }

   // REMOVED: discoverPlugins() method (moved to PluginManager)
   ```

3. **Production Code Resilience**:
   - `Load()` now handles .so loading errors gracefully (logs warning, continues)
   - No test-specific code paths
   - Semantic versioning for plugins: `~/.pel/formae/plugins/{namespace}/v1.0.0/{namespace}-plugin`

**Test Results**: ✅ `TestMetastructure_ApplyForma_DistributedPlugin` passes (0.98s)

**TODO Note Added**: Move PluginManager out of `pkg/plugin` (shouldn't be exposed to library users)

---

## Architecture Evolution: Remote Plugin Support

### Current State (Phase 0)

**Local Plugins Only**:
- Agent scans `~/.pel/formae/plugins/` directory
- PluginManager discovers plugin binaries by scanning filesystem
- PluginCoordinator spawns processes via meta.Port (localhost only)
- Plugins run as child processes of the agent

**Limitations**:
- Only works for plugins on the same host as agent
- Filesystem-based discovery doesn't work for remote hosts
- No mechanism for remote plugins to register themselves

### Future State: Local + Remote Plugins

**Remote Plugin Support Required**:
- Plugins can run on different hosts
- Plugins announce themselves to agent (not discovered via filesystem)
- Agent maintains registry of all available plugins (local + remote)
- Identical behavior for local and remote plugins

---

## Architectural Refactoring: Separation of Concerns

### Current Problem

`PluginCoordinator` has two unrelated responsibilities:
1. **Process Management**: Spawn/monitor/restart local plugin processes
2. **Plugin Orchestration**: Track available plugins, route operations

This won't work for remote plugins (can't spawn processes on remote hosts).

### Proposed Architecture

**Two Separate Actors**:

#### 1. PluginProcessManager (Renamed from PluginCoordinator)

**Purpose**: Manage LOCAL plugin processes only

**Responsibilities**:
- Spawn local plugin binaries via meta.Port
- Monitor process health (stdout/stderr/exit)
- Restart crashed local plugins
- NOT responsible for remote plugins

**Location**: `internal/metastructure/plugin_process_manager/`

**Key Methods**:
```go
type PluginProcessManager struct {
    act.Actor

    localProcesses map[string]*ProcessInfo  // namespace → process info
}

type ProcessInfo struct {
    namespace     string
    binaryPath    string
    metaPortAlias gen.Alias
    processID     int
    healthy       bool
}

func (p *PluginProcessManager) Init(args ...any) error {
    // Get local plugins from PluginManager
    pluginManager := p.Env("PluginManager").(*plugin.Manager)
    localPlugins := pluginManager.ListExternalResourcePlugins()

    // Spawn each local plugin
    for _, info := range localPlugins {
        p.spawnProcess(info)
    }
}

func (p *PluginProcessManager) HandleMessage(from gen.PID, message any) error {
    switch msg := message.(type) {
    case meta.MessagePortText:
        // Log plugin stdout
    case meta.MessagePortTerminate:
        // Restart crashed process
    }
}
```

#### 2. PluginRegistry (New Actor)

**Purpose**: Maintain registry of ALL available plugins (local + remote)

**Responsibilities**:
- Receive plugin announcements from local AND remote plugins
- Track plugin capabilities (namespace, rate limits, supported resources)
- Provide plugin lookup for ResourceUpdater
- Monitor plugin health via heartbeat
- NOT responsible for spawning processes

**Location**: `internal/metastructure/plugin_registry/`

**Key Methods**:
```go
type PluginRegistry struct {
    act.Actor

    plugins map[string]*RegisteredPlugin  // namespace → plugin info
}

type RegisteredPlugin struct {
    Namespace       string
    NodeName        string         // e.g., "aws-plugin@remote-host"
    RateLimit       int            // ops per second
    SupportedTypes  []string       // AWS::S3::Bucket, AWS::EC2::Instance, etc.
    LastHeartbeat   time.Time
    Healthy         bool
    Location        PluginLocation // Local or Remote
}

type PluginLocation int
const (
    PluginLocationLocal  PluginLocation = 0
    PluginLocationRemote PluginLocation = 1
)

// Messages
type PluginAnnouncement struct {
    Namespace      string
    NodeName       string
    RateLimit      int
    SupportedTypes []string
}

type GetPluginOperator struct {
    Namespace   string
    ResourceURI string
    Operation   string
    OperationID string
}

type PluginOperatorPID struct {
    PID gen.ProcessID
}

func (r *PluginRegistry) HandleMessage(from gen.PID, message any) error {
    switch msg := message.(type) {
    case PluginAnnouncement:
        // Register or update plugin
        r.registerPlugin(msg)
        r.Log().Info("Plugin registered", "namespace", msg.Namespace, "node", msg.NodeName)
    }
}

func (r *PluginRegistry) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
    switch req := request.(type) {
    case GetPluginOperator:
        // Look up plugin by namespace
        plugin, exists := r.plugins[req.Namespace]
        if !exists {
            return nil, fmt.Errorf("no plugin registered for namespace: %s", req.Namespace)
        }

        // Return remote PID for PluginOperator on plugin node
        return PluginOperatorPID{
            PID: gen.ProcessID{
                Name: actornames.PluginOperator(req.ResourceURI, req.Operation, req.OperationID),
                Node: gen.Atom(plugin.NodeName),
            },
        }, nil
    }
}
```

### Message Flow

**Plugin Startup (Local)**:
```
1. Agent starts
   ↓
2. PluginManager.Load() discovers local plugins
   ↓
3. PluginProcessManager.Init() spawns processes
   ↓
4. Plugin process starts, Ergo node initializes
   ↓
5. Plugin sends PluginAnnouncement to PluginRegistry
   ↓
6. PluginRegistry registers plugin
   ↓
7. Plugin is now available for operations
```

**Plugin Startup (Remote)**:
```
1. Remote host starts plugin process manually
   ↓
2. Plugin connects to agent's Ergo network
   ↓
3. Plugin sends PluginAnnouncement to PluginRegistry
   ↓
4. PluginRegistry registers plugin
   ↓
5. Plugin is now available for operations
   ↓
(No PluginProcessManager involvement for remote plugins)
```

**Resource Operation**:
```
1. ResourceUpdater needs to create AWS resource
   ↓
2. Call PluginRegistry.GetPluginOperator(namespace="AWS")
   ↓
3. PluginRegistry looks up "AWS" → returns PID on "aws-plugin@host"
   ↓
4. ResourceUpdater calls remote PluginOperator
   ↓
5. Ergo handles network routing (transparent to ResourceUpdater)
```

---

## Plugin Announcement Mechanism

### Design Goals

1. **Automatic**: Plugin announces itself on startup (no manual registration)
2. **Unified**: Same mechanism for local and remote plugins
3. **Simple**: Plugin developer only implements ResourcePlugin interface
4. **Hidden Complexity**: Ergo/actor magic hidden in SDK

### Plugin Binary Architecture

**Plugin Developer Experience**:
```go
// plugins/my-cloud/plugin.go
package main

import "github.com/platform-engineering-labs/formae/pkg/plugin"

type MyCloudPlugin struct{}

func (p *MyCloudPlugin) Namespace() string { return "MyCloud" }
func (p *MyCloudPlugin) Create(ctx, req) (*Result, error) { /* ... */ }
func (p *MyCloudPlugin) Read(ctx, req) (*Result, error) { /* ... */ }
// ... other ResourcePlugin methods ...

func main() {
    // That's it! SDK handles everything else
    plugin.Run(&MyCloudPlugin{})
}
```

**SDK Responsibilities** (hidden from developer):
```go
// pkg/plugin/sdk/run.go
package sdk

func Run(p plugin.ResourcePlugin) {
    // 1. Read config from environment
    agentNode := os.Getenv("FORMAE_AGENT_NODE")
    pluginNode := os.Getenv("FORMAE_PLUGIN_NODE")
    cookie := os.Getenv("FORMAE_NETWORK_COOKIE")

    // 2. Start Ergo node
    options := gen.NodeOptions{
        Network: gen.NetworkOptions{
            Mode:   gen.NetworkModeEnabled,
            Cookie: cookie,
        },
        Security: gen.SecurityOptions{
            ExposeEnvRemoteSpawn: true,
        },
        Env: map[gen.Env]any{
            gen.Env("Plugin"): p,
        },
    }

    node, _ := ergo.StartNode(gen.Atom(pluginNode), options)

    // 3. Spawn PluginApplication (supervises PluginOperators)
    node.SpawnRegister("plugin-app", "plugin-app",
        gen.ProcessOptions{}, PluginApplication{})

    // 4. Send announcement to agent's PluginRegistry
    sendAnnouncement(node, agentNode, p)

    // 5. Start heartbeat (every 30s)
    startHeartbeat(node, agentNode, p.Namespace())

    // 6. Wait for shutdown
    waitForShutdown(node)
}

func sendAnnouncement(node gen.Node, agentNode string, p plugin.ResourcePlugin) {
    announcement := PluginAnnouncement{
        Namespace:      p.Namespace(),
        NodeName:       string(node.Name()),
        RateLimit:      p.RateLimit(),
        SupportedTypes: p.SupportedResourceTypes(),
    }

    registryPID := gen.ProcessID{
        Name: "PluginRegistry",
        Node: gen.Atom(agentNode),
    }

    // Send announcement message
    node.Send(gen.PID{}, registryPID, announcement)

    log.Printf("Plugin %s announced to %s", p.Namespace(), agentNode)
}
```

**Benefits**:
- Plugin developer writes ~20 lines of code
- All Ergo complexity hidden
- Consistent pattern across all plugins
- Easy to test (mock the SDK)

---

## Next Steps (Phased Approach)

### Phase 1: Refactor to PluginProcessManager + PluginRegistry

**Goal**: Separate process management from plugin orchestration

**Tasks**:
1. **Rename PluginCoordinator → PluginProcessManager**
   - Update all references
   - Update tests
   - Update application.go registration

2. **Create PluginRegistry Actor**
   - Define RegisteredPlugin struct
   - Implement Init() (starts empty registry)
   - Implement HandleMessage(PluginAnnouncement)
   - Implement HandleCall(GetPluginOperator)

3. **Update ResourceUpdater**
   - Change from calling PluginCoordinator to calling PluginRegistry
   - Update to use GetPluginOperator message

4. **Update Application Registration**
   ```go
   // internal/metastructure/application.go
   {Name: "PluginProcessManager", Factory: plugin_process_manager.New},
   {Name: "PluginRegistry", Factory: plugin_registry.New},
   ```

5. **Write Tests**
   - Unit test: PluginRegistry receives announcement, registers plugin
   - Unit test: PluginRegistry lookup returns correct PID
   - Integration test: ResourceUpdater gets PID from registry

**Checkpoint**: ✅ Tests pass, separation complete (no functional change yet)

### Phase 2: Implement Plugin Announcement

**Goal**: Plugins announce themselves to PluginRegistry

**Tasks**:
1. **Define PluginAnnouncement Message**
   ```go
   // internal/metastructure/messages/plugin.go
   type PluginAnnouncement struct {
       Namespace      string
       NodeName       string
       RateLimit      int
       SupportedTypes []string
   }
   ```

2. **Register with EDF**
   ```go
   // internal/metastructure/edf_types.go
   edf.RegisterTypeOf(messages.PluginAnnouncement{})
   ```

3. **Update Test to Expect Announcement**
   ```go
   // internal/workflow_tests/distributed/plugin_test.go
   func TestMetastructure_ApplyForma_DistributedPlugin(t *testing.T) {
       // ... existing setup ...

       // Wait for plugin announcement (NOT just startup log)
       foundAnnouncement := logCapture.WaitForLog("Plugin registered", 5*time.Second)
       require.True(t, foundAnnouncement, "Plugin should announce itself to registry")

       // ... rest of test ...
   }
   ```

4. **Run Test** (should FAIL - plugin doesn't announce yet)

**Checkpoint**: ❌ Test fails waiting for announcement (expected)

### Phase 3: Implement Plugin SDK

**Goal**: Hide Ergo complexity from plugin developers

**Tasks**:
1. **Create SDK Package**
   ```
   pkg/plugin/sdk/
   ├── run.go           - Main Run() function
   ├── announcement.go  - Announcement logic
   ├── heartbeat.go     - Health monitoring
   └── application.go   - PluginApplication supervisor
   ```

2. **Implement sdk.Run()**
   - Start Ergo node
   - Spawn PluginApplication
   - Send announcement
   - Start heartbeat
   - Wait for shutdown

3. **Update FakeAWS Plugin Binary**
   ```go
   // plugins/fake-aws/main.go
   package main

   import (
       "github.com/platform-engineering-labs/formae/pkg/plugin/sdk"
       fakeaws "github.com/platform-engineering-labs/formae/plugins/fake-aws/internal"
   )

   func main() {
       sdk.Run(&fakeaws.Plugin)  // That's it!
   }
   ```

4. **Run Test** (should PASS now)

**Checkpoint**: ✅ Test passes, plugin announces itself

### Phase 4: Discussion - Plugin Binary Architecture

**Before implementing further, discuss**:

**Questions**:
1. SDK API design - is `sdk.Run(plugin)` the right interface?
2. Configuration - should plugins read from config file or just env vars?
3. Heartbeat frequency - 30s? configurable?
4. Plugin shutdown - how should plugins handle graceful shutdown?
5. Error handling - what happens if announcement fails?
6. Discovery - should plugins also announce supported resource types?

**Topics**:
- Plugin versioning strategy
- Plugin upgrade/downgrade behavior
- Multiple versions running simultaneously?
- Plugin capability negotiation

**Outcome**: Documented design decisions for Phase 5+

---

## Current Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        Agent Process                            │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │               Ergo Node (agent@localhost)                │   │
│  │                                                          │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │   │
│  │  │   Plugin     │  │   Plugin     │  │  Resource    │  │   │
│  │  │   Process    │  │   Registry   │  │  Updater     │  │   │
│  │  │   Manager    │  │              │  │              │  │   │
│  │  └──────┬───────┘  └──────▲───────┘  └──────┬───────┘  │   │
│  │         │                  │                 │          │   │
│  │         │ spawn            │ announce        │ lookup   │   │
│  │         ▼                  │                 ▼          │   │
│  │  ┌──────────────┐         │         GetPluginOperator  │   │
│  │  │  meta.Port   │         │         returns PID        │   │
│  │  │  Processes   │         │                            │   │
│  │  └──────┬───────┘         │                            │   │
│  └─────────┼─────────────────┼────────────────────────────┘   │
│            │                 │                                │
└────────────┼─────────────────┼────────────────────────────────┘
             │                 │
             │ Ergo Network    │
             │                 │
             ▼                 │
    ┌─────────────────┐        │
    │ Plugin Process  │        │
    │ (local)         │        │
    │                 │        │
    │ ┌─────────────┐ │        │
    │ │ Startup     │ ├────────┘ PluginAnnouncement
    │ │ Actor       │ │
    │ └─────────────┘ │
    │                 │
    │ ┌─────────────┐ │
    │ │ Plugin      │ │ ← ResourceUpdater calls this
    │ │ Operator    │ │
    │ │ (FSM)       │ │
    │ └─────────────┘ │
    │                 │
    │ ┌─────────────┐ │
    │ │ Resource    │ │
    │ │ Plugin Impl │ │
    │ └─────────────┘ │
    └─────────────────┘

Remote plugins (future):
    ┌─────────────────┐
    │ Plugin Process  │
    │ (remote host)   │
    │                 │
    │ Same structure  │
    │ but NOT spawned │
    │ by Process Mgr  │
    └─────────────────┘
          │
          │ PluginAnnouncement
          └──────────┐
                     │
            (connects to agent network)
```

---

## Files Modified (2025-11-24 Session)

### Production Code

1. **pkg/plugin/manager.go**
   - Added `ResourcePluginInfo` struct
   - Added `externalResourcePlugins` field to Manager
   - Extended `Load()` to call `discoverExternalResourcePlugins()`
   - Added `discoverExternalResourcePlugins()` method
   - Added `ListExternalResourcePlugins()` method
   - Made `Load()` resilient (logs errors, doesn't panic)
   - Skips Resource type plugins when loading .so files
   - Added TODO note about moving out of pkg/plugin

2. **internal/metastructure/plugin_coordinator/plugin_coordinator.go**
   - Updated `Init()` to fetch PluginManager from Env
   - Removed `discoverPlugins()` method
   - Removed unused imports (os, path/filepath)
   - Added import for pkg/plugin

3. **internal/workflow_tests/distributed/plugin_test.go**
   - Changed plugin location from `/test/` to `/v1.0.0/`
   - Updated to call `pluginManager.Load()` normally
   - Updated comments

### Test Results

✅ All changes verified with integration test passing

---

## Questions & Decisions Needed

### 1. Actor Naming

**Question**: Is "PluginProcessManager" the right name, or would you prefer something else?

**Alternatives**:
- PluginProcessSupervisor
- LocalPluginManager
- PluginSpawner

### 2. Registry Initialization

**Question**: Should PluginRegistry start with an empty registry and only populate via announcements?

**Alternative**: Pre-populate with local plugins from PluginManager

**Recommendation**: Start empty - forces announcement mechanism to work from day 1

### 3. Heartbeat Design

**Question**: How should heartbeat work?

**Options**:
- Plugin sends periodic heartbeat to Registry
- Registry polls plugins
- Both (heartbeat + poll as backup)

### 4. Announcement Timing

**Question**: When should plugin announce?

**Options**:
- Immediately on startup (before PluginApplication spawns)
- After PluginApplication spawns successfully
- After first PluginOperator spawns

**Recommendation**: After PluginApplication spawns (confirms plugin is ready)

---

## Next Session Plan

1. **Review this document** - ensure architectural direction is correct
2. **Answer questions** above
3. **Start Phase 1**: Refactor PluginCoordinator → PluginProcessManager + PluginRegistry
4. **TDD approach**: Write failing test for announcement first

**Estimated Duration**: 4-6 hours for Phase 1-3

---

## Success Metrics

**Phase 0** (Complete ✅):
- [x] PluginManager discovers external plugins
- [x] PluginCoordinator spawns plugins
- [x] Test passes with clean separation
- [x] No test-specific production code
- [x] Semantic versioning for plugins

**Phase 1** (Next):
- [ ] PluginProcessManager manages local processes only
- [ ] PluginRegistry maintains plugin registry
- [ ] ResourceUpdater calls PluginRegistry for lookups
- [ ] Tests pass with no functional changes

**Phase 2**:
- [ ] Test fails waiting for announcement (TDD)
- [ ] PluginAnnouncement message defined
- [ ] PluginRegistry receives and logs announcements

**Phase 3**:
- [ ] SDK package created
- [ ] sdk.Run() hides Ergo complexity
- [ ] FakeAWS uses SDK (~5 lines of code)
- [ ] Test passes (announcement received)

**Phase 4**:
- [ ] Architecture discussion completed
- [ ] Decisions documented
- [ ] Ready for production plugins

