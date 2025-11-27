# Distributed Plugin Architecture - Implementation Plan

**Date:** 2025-11-18
**Branch:** `feat/azure-plugin` → `feat/distributed-plugins`
**Approach:** Start with FakeAWS distributed tests, then convert real plugins
**Methodology:** London School TDD with checkpoints

---

## Table of Contents

1. [Overview](#overview)
2. [Design Document](#design-document)
3. [Implementation Phases](#implementation-phases)
4. [Phase 0: Preparation](#phase-0-preparation)
5. [Phase 1: FakeAWS as Distributed Plugin](#phase-1-fakeaws-as-distributed-plugin)
6. [Phase 2: Distributed Workflow Tests](#phase-2-distributed-workflow-tests)
7. [Phase 3: AWS Plugin Conversion](#phase-3-aws-plugin-conversion)
8. [Phase 4: Azure Plugin Integration](#phase-4-azure-plugin-integration)
9. [Testing Strategy](#testing-strategy)
10. [Success Criteria](#success-criteria)

---

## Overview

### Goal
Convert Formae's plugin architecture from Go's `plugin.Open()` (.so files) to a distributed actor system using Ergo's networking capabilities, solving dependency conflicts while enabling future multi-cloud and host agent features.

### Strategy
**Start with tests, validate architecture, then convert plugins**

1. ✅ Enable Ergo networking
2. ✅ Convert FakeAWS to standalone binary
3. ✅ Create distributed workflow tests (validate architecture)
4. ✅ Convert AWS plugin (validate with SDK tests)
5. ✅ Convert Azure plugin (solve original blocker)

### Why Start with FakeAWS Tests?
- **Validates architecture** before touching production code
- **Fast iteration** (no cloud API calls)
- **Debuggable** (controlled behavior via overrides)
- **Proves concept** for real plugins
- **Establishes patterns** for future plugins

---

## Design Document

### Architecture Overview

```
┌────────────────────────────────────────────────────────────────┐
│                        Agent Process                           │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │              Ergo Node (agent@localhost)                 │  │
│  │                                                          │  │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐    │  │
│  │  │  Changeset  │  │  Resource   │  │   Plugin    │    │  │
│  │  │  Executor   │  │  Updater    │  │ Coordinator │    │  │
│  │  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘    │  │
│  │         │                │                 │           │  │
│  │         │                └────────┬────────┘           │  │
│  │         │                         │                    │  │
│  │         │         ┌───────────────▼────────┐           │  │
│  │         │         │ GetPluginOperator      │           │  │
│  │         │         │ returns ProcessID{     │           │  │
│  │         │         │   Name: "op-xyz"       │           │  │
│  │         │         │   Node: "aws-plugin@"  │           │  │
│  │         │         │ }                      │           │  │
│  │         │         └───────────┬────────────┘           │  │
│  └─────────┼─────────────────────┼──────────────────────── ┘  │
│            │                     │                            │
│            │                     │                            │
│  ┌─────────▼─────────────────────▼──────────────────────────┐ │
│  │           meta.Port Processes (monitoring)                │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │ │
│  │  │ fake-aws-    │  │ aws-plugin   │  │ azure-plugin │   │ │
│  │  │ plugin       │  │ (meta.Port)  │  │ (meta.Port)  │   │ │
│  │  │ (meta.Port)  │  │              │  │              │   │ │
│  │  │ • stdout     │  │ • stdout     │  │ • stdout     │   │ │
│  │  │ • stderr     │  │ • stderr     │  │ • stderr     │   │ │
│  │  │ • exit       │  │ • exit       │  │ • exit       │   │ │
│  │  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘   │ │
│  └─────────┼──────────────────┼──────────────────┼───────────┘ │
└────────────┼──────────────────┼──────────────────┼─────────────┘
             │                  │                  │
    Ergo Network (CRUD ops)     │                  │
             │                  │                  │
             ▼                  ▼                  ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│ FakeAWS Plugin  │  │ AWS Plugin      │  │ Azure Plugin    │
│ Process         │  │ Process         │  │ Process         │
│                 │  │                 │  │                 │
│ Ergo Node       │  │ Ergo Node       │  │ Ergo Node       │
│ "fake-aws-      │  │ "aws-plugin@"   │  │ "azure-plugin@" │
│  plugin@..."    │  │                 │  │                 │
│                 │  │                 │  │                 │
│ ┌─────────────┐ │  │ ┌─────────────┐ │  │ ┌─────────────┐ │
│ │ Plugin      │ │  │ │ Plugin      │ │  │ │ Plugin      │ │
│ │ Operator    │ │  │ │ Operator    │ │  │ │ Operator    │ │
│ │ Actors      │ │  │ │ Actors      │ │  │ │ Actors      │ │
│ │ (FSM)       │ │  │ │ (FSM)       │ │  │ │ (FSM)       │ │
│ └─────────────┘ │  │ └─────────────┘ │  │ └─────────────┘ │
│                 │  │                 │  │                 │
│ FakeAWS Plugin  │  │ AWS SDK         │  │ Azure SDK       │
│ (in-memory)     │  │ (CloudControl)  │  │ (ARM)           │
└─────────────────┘  └─────────────────┘  └─────────────────┘
```

### Key Components

#### 1. PluginCoordinator Actor (New)

**Location**: `internal/metastructure/plugin_coordinator.go`

**Purpose**: Manages plugin process lifecycle

**Responsibilities**:
- Spawn plugin processes via meta.Port
- Monitor plugin health (stdout/stderr/exit)
- Provide remote PluginOperator PIDs to ResourceUpdater
- Restart crashed plugins
- Unified logging for all plugin output

**Messages**:
```go
// Incoming
type GetPluginOperator struct {
    Namespace   string  // "FakeAWS", "AWS", "Azure"
    ResourceURI string
    Operation   string
    OperationID string
}

type ShutdownPlugin struct {
    Namespace string
}

// Outgoing
type PluginOperatorPID struct {
    PID gen.ProcessID  // Points to plugin process
}
```

**State**:
```go
type PluginCoordinator struct {
    act.Actor

    plugins       map[string]*PluginInfo  // namespace → info
    pluginConfigs map[string]PluginConfig // from env
}

type PluginInfo struct {
    namespace   string
    metaPortPID gen.PID    // PID of meta.Port process
    nodeName    string     // "fake-aws-plugin@localhost"
    binaryPath  string
    healthy     bool
    lastSeen    time.Time
}
```

#### 2. Plugin Binary Structure (New)

Each plugin becomes a standalone executable:

```
plugins/fake-aws/
├── main.go              (NEW - entry point, Ergo node setup)
├── plugin_operator.go   (MOVED from internal/metastructure)
├── plugin_application.go (NEW - supervises PluginOperators)
├── fake_aws.go          (existing - plugin implementation)
└── go.mod               (independent dependencies)
```

**Entry Point**:
```go
// plugins/fake-aws/main.go
package main

func main() {
    // Read config from env
    agentNode := os.Getenv("FORMAE_AGENT_NODE")
    pluginNode := os.Getenv("FORMAE_PLUGIN_NODE")
    cookie := os.Getenv("FORMAE_NETWORK_COOKIE")

    // Setup Ergo node
    options := gen.NodeOptions{}
    options.Network.Mode = gen.NetworkModeEnabled
    options.Network.Cookie = cookie
    options.Security.ExposeEnvRemoteSpawn = true

    // Plugin-specific env
    options.Env = map[gen.Env]any{
        gen.Env("Plugin"): Plugin,  // The FakeAWS plugin
    }

    // Start node
    node, _ := ergo.StartNode(gen.Atom(pluginNode), options)

    // Spawn application that supervises PluginOperators
    node.SpawnRegister("plugin-app", "plugin-app",
        gen.ProcessOptions{}, PluginApplication{})

    // Wait for shutdown
    <-signals.Wait()
    node.Stop()
}
```

#### 3. Modified ResourceUpdater Flow

**Before**:
```go
// Call local supervisor
proc.Call(pluginOperatorSupervisorProcess(proc),
    EnsurePluginOperator{...})

// Call local PluginOperator
result := proc.CallWithTimeout(
    pluginOperatorProcess(proc, uri, op, id),
    operation,
    timeout)
```

**After**:
```go
// Get remote PID from PluginCoordinator
remotePID, _ := proc.Call(
    gen.ProcessID{Name: "plugin-coordinator", Node: proc.Node().Name()},
    GetPluginOperator{
        Namespace:   namespace,
        ResourceURI: uri,
        Operation:   op,
        OperationID: id,
    })

pid := remotePID.(PluginOperatorPID).PID

// Call remote PluginOperator (network transparent)
result, _ := proc.CallWithTimeout(pid, operation, timeout)
```

#### 4. Environment Sharing

**Agent to Plugin** (via meta.Port ENV):
- `FORMAE_AGENT_NODE` - "agent@localhost"
- `FORMAE_PLUGIN_NODE` - "fake-aws-plugin@localhost"
- `FORMAE_NETWORK_COOKIE` - shared secret
- `AWS_REGION` - plugin-specific config

**Agent to PluginOperator** (via Ergo RemoteSpawn):
- `RetryConfig` - retry settings (can change at runtime)
- `LoggingConfig` - logging levels
- Inherited via `ExposeEnvRemoteSpawn = true`

### Message Flow: Create Resource

```
1. User runs: formae apply

2. CLI → Agent API → Metastructure → ChangesetExecutor

3. ChangesetExecutor spawns ResourceUpdater

4. ResourceUpdater:
   ┌────────────────────────────────────────────────────┐
   │ Need to create AWS::S3::Bucket                     │
   │ Namespace = "FakeAWS" (or "AWS")                   │
   └────────────────┬───────────────────────────────────┘
                    │
                    ▼
   ┌────────────────────────────────────────────────────┐
   │ Call PluginCoordinator.GetPluginOperator           │
   │   Namespace: "FakeAWS"                             │
   │   ResourceURI: "formae://ksuid"                    │
   │   Operation: "Create"                              │
   │   OperationID: "uuid"                              │
   └────────────────┬───────────────────────────────────┘
                    │
                    ▼
   ┌────────────────────────────────────────────────────┐
   │ PluginCoordinator returns:                         │
   │   ProcessID{                                       │
   │     Name: "formae://ksuid/Create/uuid"             │
   │     Node: "fake-aws-plugin@localhost"              │
   │   }                                                │
   └────────────────┬───────────────────────────────────┘
                    │
                    ▼
   ┌────────────────────────────────────────────────────┐
   │ ResourceUpdater.Call(remotePID, CreateResource{}) │
   │                                                    │
   │ Ergo handles:                                      │
   │ - Resolve "fake-aws-plugin@localhost" via registrar│
   │ - Establish TCP connection if needed               │
   │ - Serialize CreateResource message                 │
   │ - Send over network                                │
   └────────────────┬───────────────────────────────────┘
                    │
                    ▼
   ┌────────────────────────────────────────────────────┐
   │ Plugin Process receives message                    │
   │                                                    │
   │ PluginApplication ensures PluginOperator exists    │
   │ If not, spawns new PluginOperator actor            │
   │                                                    │
   │ PluginOperator FSM handles CreateResource:        │
   │  - Calls plugin.Create(ctx, request)              │
   │  - Returns ProgressResult                          │
   └────────────────┬───────────────────────────────────┘
                    │
                    ▼
   ┌────────────────────────────────────────────────────┐
   │ Response sent back over Ergo network               │
   │                                                    │
   │ ResourceUpdater receives ProgressResult            │
   │ Continues with normal flow                         │
   └────────────────────────────────────────────────────┘
```

### Logging Flow

```
Plugin Process:
├─ Plugin logs to stdout → slog
│  └─ meta.Port captures stdout
│     └─ Sends MessagePortStdout to PluginCoordinator
│        └─ PluginCoordinator logs with namespace prefix
│           └─ Appears in unified agent log stream
│
├─ Plugin logs to stderr → error messages
│  └─ meta.Port captures stderr
│     └─ Sends MessagePortStderr to PluginCoordinator
│        └─ PluginCoordinator logs as ERROR with namespace
│
└─ Plugin exits
   └─ meta.Port sends MessagePortExit
      └─ PluginCoordinator detects crash
         └─ Restarts plugin
         └─ Logs crash event
```

**Unified Log Output**:
```
2025-11-18T10:30:00Z INFO  [agent] Starting metastructure
2025-11-18T10:30:01Z INFO  [agent] Spawning plugin: FakeAWS
2025-11-18T10:30:02Z INFO  [FakeAWS] Plugin node started: fake-aws-plugin@localhost
2025-11-18T10:30:03Z INFO  [agent] PluginCoordinator ready
2025-11-18T10:30:10Z INFO  [agent] Creating resource: formae://ksuid
2025-11-18T10:30:10Z DEBUG [FakeAWS] Received CreateResource request
2025-11-18T10:30:10Z DEBUG [FakeAWS] Creating S3 bucket: my-bucket
2025-11-18T10:30:11Z INFO  [FakeAWS] Resource created successfully
2025-11-18T10:30:11Z INFO  [agent] Resource creation complete
```

---

## Implementation Phases

### Phase 0: Preparation
**Goal**: Enable Ergo networking and basic infrastructure
**Duration**: 1-2 days
**Checkpoint**: Agent starts with networking enabled

### Phase 1: FakeAWS as Distributed Plugin
**Goal**: Convert FakeAWS to standalone binary
**Duration**: 2-3 days
**Checkpoint**: FakeAWS plugin binary runs and responds to messages

### Phase 2: Distributed Workflow Tests
**Goal**: Create distributed test suite with FakeAWS
**Duration**: 3-4 days
**Checkpoint**: All distributed tests pass

### Phase 3: AWS Plugin Conversion
**Goal**: Convert AWS plugin to distributed architecture
**Duration**: 2-3 days
**Checkpoint**: AWS SDK tests pass

### Phase 4: Azure Plugin Integration
**Goal**: Add Azure plugin (solves original problem)
**Duration**: 1-2 days
**Checkpoint**: Azure plugin loads without dependency conflicts

**Total Estimated Duration**: 9-14 days

---

## Phase 0: Preparation

### Goal
Enable Ergo networking and create foundational infrastructure

### Tasks

#### Task 0.1: Enable Ergo Networking
**File**: `internal/metastructure/metastructure.go`

**Test First** (write failing test):
```go
// internal/metastructure/metastructure_test.go (new file)
func TestMetastructure_NetworkingEnabled(t *testing.T) {
    cfg := &model.Config{
        Agent: model.Agent{
            Server: model.Server{
                Nodename: "test-agent",
                Hostname: "localhost",
                Secret:   "test-secret",
            },
        },
    }

    m, _ := NewMetastructure(context.Background(), cfg, plugin.NewManager(), "test")

    // Verify networking is enabled
    assert.NotEqual(t, gen.NetworkModeDisabled, m.options.Network.Mode)
    assert.Equal(t, "test-secret", m.options.Network.Cookie)
}
```

**Implementation**:
```go
// internal/metastructure/metastructure.go
func NewMetastructureWithDataStoreAndContext(...) (*Metastructure, error) {
    // ... existing code ...

    // CHANGE: Enable networking
    metastructure.options.Network.Mode = gen.NetworkModeEnabled  // ← Was NetworkModeDisabled

    // Enable environment sharing for RemoteSpawn
    metastructure.options.Security.ExposeEnvRemoteSpawn = true

    // ... rest unchanged ...
}
```

**Checkpoint**: ✅ Test passes, agent starts with networking enabled

#### Task 0.2: Register EDF Types
**File**: `internal/metastructure/edf_types.go` (new)

**Purpose**: Register all message types for network serialization

**Test First**:
```go
// internal/metastructure/edf_types_test.go
func TestEDF_AllTypesRegistered(t *testing.T) {
    // Attempt to encode all message types
    messages := []any{
        resource.CreateRequest{},
        resource.CreateResult{},
        resource.ReadRequest{},
        resource.ReadResult{},
        resource.UpdateRequest{},
        resource.UpdateResult{},
        resource.DeleteRequest{},
        resource.DeleteResult{},
        resource.ListRequest{},
        resource.ListResult{},
        resource.StatusRequest{},
        resource.StatusResult{},
        resource.ProgressResult{},
    }

    for _, msg := range messages {
        data, err := edf.Encode(msg)
        assert.NoError(t, err, "Failed to encode %T", msg)

        // Verify we can decode back
        decoded := reflect.New(reflect.TypeOf(msg)).Interface()
        err = edf.Decode(data, decoded)
        assert.NoError(t, err, "Failed to decode %T", msg)
    }
}
```

**Implementation**:
```go
// internal/metastructure/edf_types.go
package metastructure

import (
    "ergo.services/ergo/net/edf"
    "github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func init() {
    registerEDFTypes()
}

func registerEDFTypes() {
    // Resource operation messages
    edf.RegisterTypeOf(resource.CreateRequest{})
    edf.RegisterTypeOf(resource.CreateResult{})
    edf.RegisterTypeOf(resource.ReadRequest{})
    edf.RegisterTypeOf(resource.ReadResult{})
    edf.RegisterTypeOf(resource.UpdateRequest{})
    edf.RegisterTypeOf(resource.UpdateResult{})
    edf.RegisterTypeOf(resource.DeleteRequest{})
    edf.RegisterTypeOf(resource.DeleteResult{})
    edf.RegisterTypeOf(resource.ListRequest{})
    edf.RegisterTypeOf(resource.ListResult{})
    edf.RegisterTypeOf(resource.StatusRequest{})
    edf.RegisterTypeOf(resource.StatusResult{})
    edf.RegisterTypeOf(resource.ProgressResult{})

    // Internal messages
    edf.RegisterTypeOf(GetPluginOperator{})
    edf.RegisterTypeOf(PluginOperatorPID{})

    // Model types
    edf.RegisterTypeOf(model.RetryConfig{})
    edf.RegisterTypeOf(model.LoggingConfig{})
}

// Internal message types
type GetPluginOperator struct {
    Namespace   string
    ResourceURI string
    Operation   string
    OperationID string
}

type PluginOperatorPID struct {
    PID gen.ProcessID
}
```

**Checkpoint**: ✅ All message types can be encoded/decoded

#### Task 0.3: Create PluginCoordinator Skeleton
**File**: `internal/metastructure/plugin_coordinator/plugin_coordinator.go` (new)

**Test First**:
```go
// internal/metastructure/plugin_coordinator/plugin_coordinator_test.go
func TestPluginCoordinator_Init(t *testing.T) {
    // Setup test node
    options := gen.NodeOptions{}
    options.Network.Mode = gen.NetworkModeEnabled
    node, _ := ergo.StartNode("test-node@localhost", options)
    defer node.Stop()

    // Spawn PluginCoordinator
    pid, err := node.Spawn("coordinator", gen.ProcessOptions{}, PluginCoordinator{})

    assert.NoError(t, err)
    assert.NotEqual(t, gen.PID{}, pid)
}
```

**Implementation**:
```go
// internal/metastructure/plugin_coordinator/plugin_coordinator.go
package plugin_coordinator

import (
    "ergo.services/ergo/act"
    "ergo.services/ergo/gen"
)

type PluginCoordinator struct {
    act.Actor

    plugins map[string]*PluginInfo
}

type PluginInfo struct {
    namespace   string
    metaPortPID gen.PID
    nodeName    string
    healthy     bool
}

func (p *PluginCoordinator) Init(args ...any) error {
    p.plugins = make(map[string]*PluginInfo)

    p.Log().Info("PluginCoordinator started")

    return nil
}

func (p *PluginCoordinator) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
    // TODO: Implement message handling
    return nil, nil
}

func (p *PluginCoordinator) HandleMessage(from gen.PID, message any) error {
    // TODO: Handle meta.Port messages
    return nil
}
```

**Checkpoint**: ✅ PluginCoordinator spawns successfully

#### Task 0.4: Add PluginCoordinator to Application
**File**: `internal/metastructure/application.go`

**Test**: Extend existing metastructure tests

**Implementation**:
```go
// internal/metastructure/application.go
func CreateApplication() gen.ApplicationBehavior {
    return &Application{}
}

type Application struct {
    act.Application
}

func (a *Application) Load(args ...any) (gen.ApplicationSpec, error) {
    return gen.ApplicationSpec{
        Name: "formae",
        Children: []gen.ApplicationChildSpec{
            // Existing actors
            {Name: "MetastructureBridge", Factory: NewMetastructureBridge},
            {Name: "PluginOperatorSupervisor", Factory: NewPluginOperatorSupervisor},
            // ...

            // NEW: PluginCoordinator
            {
                Name:    "PluginCoordinator",
                Factory: plugin_coordinator.New,
            },
        },
    }, nil
}
```

**Checkpoint**: ✅ PluginCoordinator starts with agent

### Phase 0 Acceptance Criteria

- [ ] Agent starts with `NetworkModeEnabled`
- [ ] Network cookie is set from config
- [ ] `ExposeEnvRemoteSpawn` is enabled
- [ ] All message types registered with EDF
- [ ] PluginCoordinator spawns on agent startup
- [ ] Agent log shows "PluginCoordinator started"

**Checkpoint**: ✅ Run agent, verify network is enabled

---

## Phase 1: FakeAWS as Distributed Plugin

### Goal
Convert FakeAWS plugin to standalone binary that runs as separate process

### Tasks

#### Task 1.1: Create FakeAWS Plugin Main Entry Point
**File**: `plugins/fake-aws/main.go` (new)

**Test Strategy**: Integration test (will be in Phase 2)

**Implementation**:
```go
// plugins/fake-aws/main.go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "os/signal"
    "syscall"

    "ergo.services/ergo"
    "ergo.services/ergo/gen"

    fakeaws "github.com/platform-engineering-labs/formae/plugins/fake-aws/internal"
)

func main() {
    // Read configuration from environment
    agentNode := os.Getenv("FORMAE_AGENT_NODE")
    if agentNode == "" {
        log.Fatal("FORMAE_AGENT_NODE environment variable required")
    }

    pluginNode := os.Getenv("FORMAE_PLUGIN_NODE")
    if pluginNode == "" {
        log.Fatal("FORMAE_PLUGIN_NODE environment variable required")
    }

    cookie := os.Getenv("FORMAE_NETWORK_COOKIE")
    if cookie == "" {
        log.Fatal("FORMAE_NETWORK_COOKIE environment variable required")
    }

    // Setup Ergo node
    options := gen.NodeOptions{}
    options.Network.Mode = gen.NetworkModeEnabled
    options.Network.Cookie = cookie
    options.Security.ExposeEnvRemoteSpawn = true

    // Register plugin in environment
    options.Env = map[gen.Env]any{
        gen.Env("Plugin"): fakeaws.Plugin,  // The FakeAWS plugin instance
    }

    // Start Ergo node
    node, err := ergo.StartNode(gen.Atom(pluginNode), options)
    if err != nil {
        log.Fatalf("Failed to start node: %v", err)
    }

    fmt.Printf("FakeAWS plugin node started: %s\n", pluginNode)
    fmt.Printf("Connecting to agent: %s\n", agentNode)

    // Spawn the plugin application
    _, err = node.SpawnRegister("plugin-app", "plugin-app",
        gen.ProcessOptions{}, fakeaws.PluginApplication{})
    if err != nil {
        log.Fatalf("Failed to spawn plugin application: %v", err)
    }

    // Wait for shutdown signal
    sig := make(chan os.Signal, 1)
    signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
    <-sig

    fmt.Println("Shutting down FakeAWS plugin...")
    node.Stop()
}
```

**Checkpoint**: ✅ Binary compiles

#### Task 1.2: Move PluginOperator to Shared Package
**Files**:
- `internal/metastructure/plugin_operation/plugin_operator.go` → `pkg/plugin/operator/plugin_operator.go`

**Why**: Both agent and plugins need this code

**Test**: Move existing tests, verify they still pass

**Implementation**:
```bash
# Move files
mkdir -p pkg/plugin/operator
mv internal/metastructure/plugin_operation/plugin_operator.go pkg/plugin/operator/
mv internal/metastructure/plugin_operation/plugin_operator_test.go pkg/plugin/operator/

# Update package declaration
# Change: package plugin_operation
# To: package operator

# Update imports across codebase
```

**Changes needed**:
```go
// pkg/plugin/operator/plugin_operator.go
package operator  // ← Changed from plugin_operation

// Remove dependency on PluginManager
func (o *PluginOperator) Init(args ...any) error {
    requester := args[0].(gen.PID)

    // CHANGE: Get plugin from env instead of manager
    plugin := o.Env("Plugin").(plugin.ResourcePlugin)
    retryConfig := o.Env("RetryConfig").(model.RetryConfig)

    o.plugin = plugin
    o.retryConfig = retryConfig
    o.requester = requester

    return nil
}

// Update all methods to use o.plugin directly
func read(data *PluginUpdateData, ...) (*resource.ProgressResult, error) {
    // CHANGE: Direct plugin call instead of manager lookup
    result := data.plugin.Read(data.context, &resource.ReadRequest{...})
    return handlePluginResult(data, operation, proc, result.ProgressResult)
}
```

**Checkpoint**: ✅ Existing tests pass with new location

#### Task 1.3: Create PluginApplication (Supervisor)
**File**: `plugins/fake-aws/internal/plugin_application.go` (new)

**Purpose**: Supervises PluginOperator actors in plugin process

**Test First**:
```go
// plugins/fake-aws/internal/plugin_application_test.go
func TestPluginApplication_Init(t *testing.T) {
    options := gen.NodeOptions{}
    node, _ := ergo.StartNode("test@localhost", options)
    defer node.Stop()

    pid, err := node.Spawn("app", gen.ProcessOptions{}, PluginApplication{})

    assert.NoError(t, err)
    assert.NotEqual(t, gen.PID{}, pid)
}
```

**Implementation**:
```go
// plugins/fake-aws/internal/plugin_application.go
package internal

import (
    "ergo.services/ergo/act"
    "ergo.services/ergo/gen"

    "github.com/platform-engineering-labs/formae/pkg/plugin/operator"
)

type PluginApplication struct {
    act.Supervisor
}

func (p *PluginApplication) Init(args ...any) (act.SupervisorSpec, error) {
    return act.SupervisorSpec{
        Type: act.SupervisorTypeSimpleOneForOne,
        Children: []act.SupervisorChildSpec{
            {
                Name:    "plugin-operator",
                Factory: operator.NewPluginOperator,
            },
        },
        Restart: act.SupervisorRestart{
            Strategy:  act.SupervisorStrategyTransient,  // Only restart on crash
            Intensity: 5,
            Period:    10,
        },
    }, nil
}
```

**Checkpoint**: ✅ PluginApplication spawns

#### Task 1.4: Update Makefile for Plugin Binary
**File**: `Makefile`

**Implementation**:
```makefile
# Add FakeAWS plugin binary target
.PHONY: build-fake-aws-plugin
build-fake-aws-plugin:
	@echo "Building FakeAWS plugin binary..."
	go build -o fake-aws-plugin ./plugins/fake-aws

# Add to main build target
build: build-agent build-fake-aws-plugin

# Clean target
clean:
	rm -f formae fake-aws-plugin aws-plugin azure-plugin
```

**Checkpoint**: ✅ `make build-fake-aws-plugin` creates binary

#### Task 1.5: Test Plugin Binary Manually
**Manual test**:

```bash
# Terminal 1: Start agent
make build
./formae agent start

# Terminal 2: Start FakeAWS plugin manually
export FORMAE_AGENT_NODE=agent@localhost
export FORMAE_PLUGIN_NODE=fake-aws-plugin@localhost
export FORMAE_NETWORK_COOKIE=test-secret
./fake-aws-plugin

# Expected output:
# FakeAWS plugin node started: fake-aws-plugin@localhost
# Connecting to agent: agent@localhost
```

**Checkpoint**: ✅ Plugin binary starts, connects to agent

### Phase 1 Acceptance Criteria

- [ ] FakeAWS plugin compiles to standalone binary
- [ ] Binary accepts env vars: FORMAE_AGENT_NODE, FORMAE_PLUGIN_NODE, FORMAE_NETWORK_COOKIE
- [ ] Binary starts Ergo node successfully
- [ ] PluginApplication supervisor starts
- [ ] Plugin registers with built-in registrar
- [ ] Agent can resolve plugin node
- [ ] Plugin logs to stdout (visible when run manually)

---

## Phase 2: Distributed Workflow Tests

### Goal
Create comprehensive distributed tests using FakeAWS plugin

### Tasks

#### Task 2.1: Create Distributed Test Helpers
**File**: `internal/workflow_tests/distributed_helpers/helpers.go` (new)

**Plugin Binary Location for Tests**: 
- Test plugins are placed in: `~/.pel/formae/plugins/{namespace}/test/{namespace}-plugin`
- Example: `~/.pel/formae/plugins/fake-aws/test/fake-aws-plugin`
- PluginCoordinator scans this directory structure to discover and start plugins
- Tests compile the plugin binary with hardcoded behavior (no context-based overrides in distributed mode)

**Test First**: We'll use these in actual distributed tests

**Implementation**:
```go
// internal/workflow_tests/distributed_helpers/helpers.go
package distributed_helpers

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "testing"
    "time"

    "ergo.services/ergo"
    "ergo.services/ergo/gen"
    "github.com/stretchr/testify/require"

    "github.com/platform-engineering-labs/formae/internal/metastructure"
    "github.com/platform-engineering-labs/formae/pkg/plugin"
)

type DistributedTestContext struct {
    Agent          *metastructure.Metastructure
    PluginProcess  *exec.Cmd
    PluginNode     string
    Cleanup        func()
}

func NewDistributedTest(t *testing.T, namespace string) *DistributedTestContext {
    // 1. Start plugin process
    pluginNode := fmt.Sprintf("%s-plugin@localhost", namespace)
    cookie := "test-secret-" + t.Name()

    cmd := exec.Command("./fake-aws-plugin")
    cmd.Env = append(os.Environ(),
        "FORMAE_AGENT_NODE=agent@localhost",
        fmt.Sprintf("FORMAE_PLUGIN_NODE=%s", pluginNode),
        fmt.Sprintf("FORMAE_NETWORK_COOKIE=%s", cookie),
    )

    // Capture output for debugging
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr

    err := cmd.Start()
    require.NoError(t, err, "Failed to start plugin process")

    // Wait for plugin to be ready
    time.Sleep(2 * time.Second)  // TODO: Use health check instead

    // 2. Create agent with networking
    cfg := &model.Config{
        Agent: model.Agent{
            Server: model.Server{
                Nodename: "agent",
                Hostname: "localhost",
                Secret:   cookie,
            },
            // ... other config
        },
    }

    pluginManager := plugin.NewManager()
    // Don't load plugins - they're external now

    agent, err := metastructure.NewMetastructure(
        context.Background(), cfg, pluginManager, "test")
    require.NoError(t, err)

    go agent.Start()

    // Wait for agent to be ready
    time.Sleep(1 * time.Second)

    // 3. Setup cleanup
    cleanup := func() {
        agent.Stop(true)
        cmd.Process.Kill()
        cmd.Wait()
    }

    return &DistributedTestContext{
        Agent:         agent,
        PluginProcess: cmd,
        PluginNode:    pluginNode,
        Cleanup:       cleanup,
    }
}

// WaitForPluginReady polls until plugin node is reachable
func WaitForPluginReady(t *testing.T, node gen.Node, pluginNode string, timeout time.Duration) {
    deadline := time.Now().Add(timeout)

    for time.Now().Before(deadline) {
        remoteNode, err := node.Network().GetNode(gen.Atom(pluginNode))
        if err == nil && remoteNode != nil {
            return  // Plugin is ready
        }
        time.Sleep(100 * time.Millisecond)
    }

    t.Fatalf("Plugin node %s did not become ready within %v", pluginNode, timeout)
}
```

**Checkpoint**: ✅ Helper compiles

#### Task 2.2: First Distributed Test - Happy Path Create
**File**: `internal/workflow_tests/distributed/create_resource_test.go` (new)

**Test First** (this IS the test):
```go
// internal/workflow_tests/distributed/create_resource_test.go
package distributed

import (
    "testing"
    "context"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "github.com/platform-engineering-labs/formae/internal/workflow_tests/distributed_helpers"
    "github.com/platform-engineering-labs/formae/pkg/model"
    "github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestDistributed_CreateResource_HappyPath(t *testing.T) {
    // CHECKPOINT: Review test structure before implementing

    // Setup distributed test environment
    ctx := distributed_helpers.NewDistributedTest(t, "fake-aws")
    defer ctx.Cleanup()

    // Wait for plugin to be ready
    distributed_helpers.WaitForPluginReady(t,
        ctx.Agent.Node, ctx.PluginNode, 10*time.Second)

    // Create a Forma command with a resource
    forma := &model.Forma{
        Resources: []model.Resource{
            {
                Type:  "FakeAWS::S3::Bucket",
                Label: "test-bucket",
                Properties: json.RawMessage(`{
                    "BucketName": "my-test-bucket"
                }`),
                Targets: []model.Target{
                    {Label: "default", Namespace: "FakeAWS"},
                },
            },
        },
    }

    // Apply the Forma
    commandID, err := ctx.Agent.ApplyForma(context.Background(),
        forma, model.CommandApply, model.ModeReconcile)
    require.NoError(t, err)

    // Wait for command to complete
    command := waitForCommandCompletion(t, ctx.Agent, commandID, 30*time.Second)

    // Verify success
    assert.Equal(t, model.StatusSuccess, command.Status)
    assert.Len(t, command.ResourceUpdates, 1)

    update := command.ResourceUpdates[0]
    assert.Equal(t, resource.OperationStatusSuccess, update.Status)
    assert.Equal(t, "test-bucket", update.Resource.Label)
}

// CHECKPOINT: Test should fail here - not implemented yet
```

**Implementation Steps**:

1. **Implement PluginCoordinator.GetPluginOperator**:
```go
// internal/metastructure/plugin_coordinator/plugin_coordinator.go
func (p *PluginCoordinator) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
    switch req := request.(type) {
    case GetPluginOperator:
        pluginInfo, exists := p.plugins[req.Namespace]
        if !exists {
            return nil, fmt.Errorf("plugin not found: %s", req.Namespace)
        }

        // Return remote PID
        return PluginOperatorPID{
            PID: gen.ProcessID{
                Name: actornames.PluginOperator(req.ResourceURI, req.Operation, req.OperationID),
                Node: gen.Atom(pluginInfo.nodeName),
            },
        }, nil
    }

    return nil, fmt.Errorf("unknown message: %T", request)
}
```

2. **Implement PluginCoordinator.SpawnPlugins** (called from Init):
```go
func (p *PluginCoordinator) Init(args ...any) error {
    p.plugins = make(map[string]*PluginInfo)

    // For now, hardcode FakeAWS
    // Later: read from config
    p.plugins["FakeAWS"] = &PluginInfo{
        namespace: "FakeAWS",
        nodeName:  "fake-aws-plugin@localhost",
        healthy:   true,
    }

    p.Log().Info("PluginCoordinator initialized", "plugins", len(p.plugins))

    return nil
}
```

3. **Update ResourceUpdater to use PluginCoordinator**:
```go
// internal/metastructure/resource_update/resource_updater.go
func doPluginOperation(resourceURI, operation, proc) (*resource.ProgressResult, error) {
    operationID := uuid.New().String()
    namespace := extractNamespace(operation)  // From resource type

    // CHANGE: Call PluginCoordinator instead of local supervisor
    response, err := proc.Call(
        gen.ProcessID{Name: "PluginCoordinator", Node: proc.Node().Name()},
        GetPluginOperator{
            Namespace:   namespace,
            ResourceURI: resourceURI,
            Operation:   operation.Operation(),
            OperationID: operationID,
        })

    if err != nil {
        return nil, err
    }

    remotePID := response.(PluginOperatorPID).PID

    // CHANGE: Call remote PID instead of local
    result, err := proc.CallWithTimeout(remotePID, operation, PluginOperationCallTimeout)
    if err != nil {
        return nil, err
    }

    return result.(*resource.ProgressResult), nil
}
```

**Checkpoint**: ✅ Test passes

#### Task 2.3: Test Plugin Crash Recovery
**File**: `internal/workflow_tests/distributed/plugin_crash_test.go` (new)

**Test First**:
```go
func TestDistributed_PluginCrash_Recovery(t *testing.T) {
    ctx := distributed_helpers.NewDistributedTest(t, "fake-aws")
    defer ctx.Cleanup()

    // 1. Create a resource successfully
    forma := &model.Forma{
        Resources: []model.Resource{
            {Type: "FakeAWS::S3::Bucket", Label: "test-bucket", ...},
        },
    }

    commandID1, _ := ctx.Agent.ApplyForma(context.Background(), forma, ...)
    command1 := waitForCommandCompletion(t, ctx.Agent, commandID1, 30*time.Second)
    assert.Equal(t, model.StatusSuccess, command1.Status)

    // 2. Kill the plugin process
    err := ctx.PluginProcess.Process.Kill()
    require.NoError(t, err)

    // Wait for process to die
    ctx.PluginProcess.Wait()

    // 3. PluginCoordinator should detect crash via meta.Port
    // Wait for restart
    time.Sleep(5 * time.Second)

    // 4. Try to create another resource
    forma2 := &model.Forma{
        Resources: []model.Resource{
            {Type: "FakeAWS::S3::Bucket", Label: "test-bucket-2", ...},
        },
    }

    commandID2, _ := ctx.Agent.ApplyForma(context.Background(), forma2, ...)
    command2 := waitForCommandCompletion(t, ctx.Agent, commandID2, 30*time.Second)

    // Should succeed after restart
    assert.Equal(t, model.StatusSuccess, command2.Status)
}
```

**Implementation**: Requires meta.Port integration in PluginCoordinator

**Checkpoint**: ✅ Test passes (plugin restarts automatically)

#### Task 2.4: Test Multiple Concurrent Operations
**File**: `internal/workflow_tests/distributed/concurrent_ops_test.go` (new)

**Test First**:
```go
func TestDistributed_ConcurrentOperations(t *testing.T) {
    ctx := distributed_helpers.NewDistributedTest(t, "fake-aws")
    defer ctx.Cleanup()

    // Create 10 resources concurrently
    var wg sync.WaitGroup
    commandIDs := make([]string, 10)

    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()

            forma := &model.Forma{
                Resources: []model.Resource{
                    {
                        Type:  "FakeAWS::S3::Bucket",
                        Label: fmt.Sprintf("bucket-%d", idx),
                        ...
                    },
                },
            }

            commandID, err := ctx.Agent.ApplyForma(context.Background(), forma, ...)
            require.NoError(t, err)
            commandIDs[idx] = commandID
        }(i)
    }

    wg.Wait()

    // Wait for all to complete
    for _, cmdID := range commandIDs {
        cmd := waitForCommandCompletion(t, ctx.Agent, cmdID, 30*time.Second)
        assert.Equal(t, model.StatusSuccess, cmd.Status)
    }
}
```

**Checkpoint**: ✅ All concurrent operations succeed

#### Task 2.5: Implement meta.Port Integration
**File**: `internal/metastructure/plugin_coordinator/plugin_coordinator.go`

**Purpose**: Spawn plugins via meta.Port, handle stdout/stderr/exit

**Test First**: Use existing distributed tests

**Implementation**:
```go
import (
    "ergo.services/meta"
)

func (p *PluginCoordinator) Init(args ...any) error {
    p.plugins = make(map[string]*PluginInfo)

    // Spawn FakeAWS plugin via meta.Port
    err := p.spawnPlugin("FakeAWS", "./fake-aws-plugin")
    if err != nil {
        return err
    }

    return nil
}

func (p *PluginCoordinator) spawnPlugin(namespace, binaryPath string) error {
    nodeName := fmt.Sprintf("%s-plugin@localhost", strings.ToLower(namespace))
    cookie := p.Env("ServerConfig").(*model.ServerConfig).Secret

    // Configure meta.Port
    portOptions := meta.PortOptions{
        Cmd: binaryPath,
        Env: []string{
            fmt.Sprintf("FORMAE_AGENT_NODE=%s", p.Node().Name()),
            fmt.Sprintf("FORMAE_PLUGIN_NODE=%s", nodeName),
            fmt.Sprintf("FORMAE_NETWORK_COOKIE=%s", cookie),
        },
        Tag:     namespace,
        Process: p.PID(),  // Send messages to PluginCoordinator
    }

    // Spawn meta.Port process
    metaport, err := meta.CreatePort(portOptions)
    if err != nil {
        return err
    }

    pid, err := p.SpawnMeta(metaport, gen.MetaOptions{})
    if err != nil {
        return err
    }

    p.plugins[namespace] = &PluginInfo{
        namespace:   namespace,
        metaPortPID: pid,
        nodeName:    nodeName,
        healthy:     false,  // Will be true when we receive stdout
    }

    p.Log().Info("Spawned plugin", "namespace", namespace, "node", nodeName)

    return nil
}

func (p *PluginCoordinator) HandleMessage(from gen.PID, message any) error {
    switch msg := message.(type) {
    case meta.MessagePortStdout:
        // Plugin stdout - log it
        p.Log().Info(fmt.Sprintf("[%s] %s", msg.Tag, string(msg.Data)))

        // Mark plugin as healthy
        if plugin, exists := p.plugins[msg.Tag]; exists {
            plugin.healthy = true
            plugin.lastSeen = time.Now()
        }

    case meta.MessagePortStderr:
        // Plugin stderr - log as error
        p.Log().Error(fmt.Sprintf("[%s] %s", msg.Tag, string(msg.Data)))

    case meta.MessagePortExit:
        // Plugin died
        p.Log().Error("Plugin exited", "namespace", msg.Tag, "code", msg.Code)

        if plugin, exists := p.plugins[msg.Tag]; exists {
            plugin.healthy = false
        }

        // Restart plugin
        go p.restartPlugin(msg.Tag)
    }

    return nil
}

func (p *PluginCoordinator) restartPlugin(namespace string) {
    p.Log().Info("Restarting plugin", "namespace", namespace)

    plugin := p.plugins[namespace]
    if plugin == nil {
        return
    }

    // Kill old meta.Port process
    p.Send(plugin.metaPortPID, gen.MessageExit{Reason: "restart"})

    // Wait a bit
    time.Sleep(1 * time.Second)

    // Spawn new instance
    // TODO: Get binary path from config
    err := p.spawnPlugin(namespace, "./fake-aws-plugin")
    if err != nil {
        p.Log().Error("Failed to restart plugin", "namespace", namespace, "error", err)
    }
}
```

**Checkpoint**: ✅ meta.Port spawns plugins, logs are unified, crashes are detected

### Phase 2 Acceptance Criteria

- [ ] PluginCoordinator spawns FakeAWS via meta.Port
- [ ] Plugin stdout appears in agent logs with namespace prefix
- [ ] Plugin stderr appears in agent logs as errors
- [ ] Plugin crashes are detected via MessagePortExit
- [ ] Crashed plugins are automatically restarted
- [ ] Distributed tests pass:
  - [ ] Create resource happy path
  - [ ] Update resource
  - [ ] Delete resource
  - [ ] Read resource
  - [ ] Plugin crash recovery
  - [ ] Concurrent operations (10+ resources)
- [ ] All operations use remote PluginOperator actors
- [ ] No .so files are loaded (FakeAWS is external process)

---

## Phase 3: AWS Plugin Conversion

### Goal
Convert AWS plugin to distributed architecture, validate with SDK tests

### Tasks

#### Task 3.1: Create AWS Plugin Main Entry Point
**File**: `plugins/aws/main.go` (new)

**Similar to FakeAWS**: Copy structure from `plugins/fake-aws/main.go`

**Implementation**:
```go
// plugins/aws/main.go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "os/signal"
    "syscall"

    "ergo.services/ergo"
    "ergo.services/ergo/gen"

    awsplugin "github.com/platform-engineering-labs/formae/plugins/aws/internal"
)

func main() {
    agentNode := os.Getenv("FORMAE_AGENT_NODE")
    pluginNode := os.Getenv("FORMAE_PLUGIN_NODE")
    cookie := os.Getenv("FORMAE_NETWORK_COOKIE")

    options := gen.NodeOptions{}
    options.Network.Mode = gen.NetworkModeEnabled
    options.Network.Cookie = cookie
    options.Security.ExposeEnvRemoteSpawn = true

    options.Env = map[gen.Env]any{
        gen.Env("Plugin"): awsplugin.Plugin,
    }

    node, err := ergo.StartNode(gen.Atom(pluginNode), options)
    if err != nil {
        log.Fatalf("Failed to start node: %v", err)
    }

    fmt.Printf("AWS plugin node started: %s\n", pluginNode)

    _, err = node.SpawnRegister("plugin-app", "plugin-app",
        gen.ProcessOptions{}, awsplugin.PluginApplication{})
    if err != nil {
        log.Fatalf("Failed to spawn plugin application: %v", err)
    }

    sig := make(chan os.Signal, 1)
    signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
    <-sig

    fmt.Println("Shutting down AWS plugin...")
    node.Stop()
}
```

**Checkpoint**: ✅ AWS plugin binary compiles

#### Task 3.2: Update Makefile for AWS Plugin
**File**: `Makefile`

**Implementation**:
```makefile
.PHONY: build-aws-plugin
build-aws-plugin:
	@echo "Building AWS plugin binary..."
	go build -o aws-plugin ./plugins/aws

build: build-agent build-fake-aws-plugin build-aws-plugin
```

**Checkpoint**: ✅ `make build` creates aws-plugin binary

#### Task 3.3: Add AWS to PluginCoordinator
**File**: `internal/metastructure/plugin_coordinator/plugin_coordinator.go`

**Implementation**:
```go
func (p *PluginCoordinator) Init(args ...any) error {
    p.plugins = make(map[string]*PluginInfo)

    // Spawn FakeAWS
    p.spawnPlugin("FakeAWS", "./fake-aws-plugin")

    // Spawn AWS
    p.spawnPlugin("AWS", "./aws-plugin")

    return nil
}
```

**Checkpoint**: ✅ Both plugins start

#### Task 3.4: Run AWS SDK Tests
**File**: `tests/integration/plugin-sdk/lifecycle_test.go`

**Command**:
```bash
# Start agent with distributed plugins
./formae agent start &

# Run SDK tests
PLUGIN_NAME=aws go test -tags=plugin_sdk -v ./tests/integration/plugin-sdk/
```

**Expected**:
- Tests use AWS plugin via distributed architecture
- All CRUD operations work
- Discovery works
- No dependency conflicts

**Checkpoint**: ✅ All SDK tests pass

### Phase 3 Acceptance Criteria

- [ ] AWS plugin compiles to standalone binary
- [ ] AWS plugin starts and connects to agent
- [ ] PluginCoordinator manages AWS plugin
- [ ] SDK lifecycle tests pass
- [ ] Create/Read/Update/Delete work via remote actors
- [ ] Discovery works
- [ ] No .so file is loaded for AWS
- [ ] Unified logging shows AWS operations

---

## Phase 4: Azure Plugin Integration

### Goal
Add Azure plugin, solve original dependency conflict issue

### Tasks

#### Task 4.1: Create Azure Plugin Main Entry Point
**File**: `plugins/azure/main.go` (new)

**Same pattern as AWS**

**Checkpoint**: ✅ Azure plugin binary compiles

#### Task 4.2: Update Makefile
**File**: `Makefile`

```makefile
.PHONY: build-azure-plugin
build-azure-plugin:
	@echo "Building Azure plugin binary..."
	go build -o azure-plugin ./plugins/azure

build: build-agent build-fake-aws-plugin build-aws-plugin build-azure-plugin
```

**Checkpoint**: ✅ All plugins build

#### Task 4.3: Add Azure to PluginCoordinator
**Implementation**: Same as AWS

**Checkpoint**: ✅ All three plugins run simultaneously

#### Task 4.4: Test Both AWS and Azure Together
**Test**:
```bash
# Create resources in both AWS and Azure
formae apply --file mixed-resources.pkl
```

**mixed-resources.pkl**:
```pkl
resources {
    new {
        type = "AWS::S3::Bucket"
        label = "aws-bucket"
        targets { new { label = "aws"; namespace = "AWS" } }
    }
    new {
        type = "Azure::Resources::ResourceGroup"
        label = "azure-rg"
        targets { new { label = "azure"; namespace = "Azure" } }
    }
}
```

**Checkpoint**: ✅ Both resources created successfully

#### Task 4.5: Verify No Dependency Conflicts
**Test**:
```bash
# Check that both plugins have different dependencies
ldd aws-plugin | grep golang.org
ldd azure-plugin | grep golang.org

# They should show different versions - this is OK now!
```

**Checkpoint**: ✅ Plugins have independent dependencies, no conflicts

### Phase 4 Acceptance Criteria

- [ ] Azure plugin builds independently
- [ ] Azure plugin runs alongside AWS plugin
- [ ] Both plugins can create resources simultaneously
- [ ] No dependency conflicts (verified by ldd)
- [ ] SDK tests pass for Azure
- [ ] Original blocker (Azure dependency conflicts) is solved

---

## Testing Strategy

### Test Pyramid

```
                    ▲
                   ╱ ╲
                  ╱   ╲
                 ╱ E2E ╲          (CLI tests, full stack)
                ╱───────╲
               ╱         ╲
              ╱Integration╲       (Distributed workflow tests)
             ╱─────────────╲
            ╱               ╲
           ╱  Unit Tests     ╲    (Actor tests, plugin tests)
          ╱───────────────────╲
         ╱                     ╲
        ╱   In-Process Tests    ╲  (Fast TDD cycle)
       ╱─────────────────────────╲
```

### Test Categories

#### 1. In-Process Tests (Fast)
**Location**: `internal/metastructure/*_test.go`

**Characteristics**:
- FakeAWS loaded as .so file
- Context-based overrides work
- Fast execution (< 1s per test)
- Good for TDD cycle

**When to use**:
- Developing new metastructure features
- Testing actor FSM logic
- Quick validation during coding

**Example**:
```go
func TestResourceUpdater_RejectsOutOfSyncResource(t *testing.T) {
    overrides := &plugin.ResourcePluginOverrides{
        Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
            return &resource.ReadResult{
                Properties: `{"changed": "out-of-band"}`,
            }, nil
        },
    }

    m, def, _ := test_helpers.NewTestMetastructure(t, overrides)
    defer def()

    // Test logic...
}
```

#### 2. Distributed Tests (Integration)
**Location**: `internal/workflow_tests/distributed/*_test.go`

**Characteristics**:
- FakeAWS runs as external process
- Tests actual network communication
- Tests plugin lifecycle (crashes, restarts)
- Slower (2-5s per test)

**When to use**:
- Validating distributed architecture
- Testing error scenarios (crashes, network issues)
- Integration validation

**Example**:
```go
func TestDistributed_PluginCrashRecovery(t *testing.T) {
    ctx := distributed_helpers.NewDistributedTest(t, "fake-aws")
    defer ctx.Cleanup()

    // Create resource
    // Kill plugin
    // Verify restart
    // Create another resource
}
```

#### 3. SDK Tests (Real Plugins)
**Location**: `tests/integration/plugin-sdk/*_test.go`

**Characteristics**:
- Test real plugins (AWS, Azure)
- Make actual cloud API calls
- Validate full CRUD + Discovery lifecycle
- Slowest (30s+ per test)

**When to use**:
- Validating real plugin implementations
- Before merging plugin changes
- CI/CD validation

**Example**:
```go
func TestPluginSDK_Lifecycle(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping SDK test in short mode")
    }

    // Full lifecycle: Create → Read → Update → Delete → Discovery
}
```

#### 4. E2E Tests (CLI)
**Location**: `tests/e2e/*_test.go`

**Characteristics**:
- Full formae CLI workflow
- Agent + plugins running
- Real PKL files
- Complete user workflow

**When to use**:
- Release validation
- User-facing feature testing
- Regression testing

### Test Execution

```bash
# Fast unit tests (in-process)
make test-unit

# Distributed integration tests
make test-distributed

# SDK tests (requires cloud credentials)
make test-sdk

# All tests
make test-all

# With coverage
make test-coverage
```

---

## Success Criteria

### Technical Success

- [ ] Agent starts with networking enabled
- [ ] All plugins run as separate processes
- [ ] No .so files are loaded
- [ ] Plugins communicate via Ergo network
- [ ] Plugin crashes are detected and recovered
- [ ] Unified logging shows all plugin output
- [ ] All existing workflow tests pass (in-process mode)
- [ ] All distributed tests pass
- [ ] AWS SDK tests pass
- [ ] Azure SDK tests pass
- [ ] AWS and Azure plugins run simultaneously
- [ ] No dependency conflicts between plugins

### User-Facing Success

- [ ] No changes to CLI usage
- [ ] formae apply/destroy/status work as before
- [ ] Error messages include plugin information
- [ ] Logs clearly show which plugin is executing
- [ ] Performance is acceptable (< 10% overhead)

### Code Quality

- [ ] All new code follows TDD approach
- [ ] Each checkpoint validated before moving forward
- [ ] Comprehensive test coverage (> 80%)
- [ ] Clear documentation for plugin developers
- [ ] Migration guide for existing plugins

---

## Rollout Plan

### Development
1. Create feature branch: `feat/distributed-plugins`
2. Implement phases sequentially
3. Checkpoint after each task
4. Review with team at end of each phase

### Testing
1. Run full test suite before each phase completion
2. Manual testing of critical paths
3. Performance benchmarking

### Deployment
1. Deploy to staging with FakeAWS first
2. Validate with synthetic workload
3. Deploy AWS plugin
4. Deploy Azure plugin
5. Monitor for issues

### Rollback Plan
- Keep .so loading code in parallel initially
- Feature flag to switch between modes
- Can revert to .so files if critical issues found

---

## Next Steps

1. **Review this plan** with the team
2. **Get approval** for approach
3. **Start Phase 0** - Enable networking
4. **Daily checkpoints** to track progress
5. **Weekly demos** to stakeholders

---

**Ready to begin? Start with Phase 0, Task 0.1!**
