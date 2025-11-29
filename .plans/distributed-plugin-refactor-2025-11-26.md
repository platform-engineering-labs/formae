# Distributed Plugin Architecture Refactor

**Date:** 2025-11-26 (Updated: 2025-11-28)
**Branch:** `feat/distributed-plugins`
**Status:** Core architecture complete, AWS plugin working, hardening in progress

---

## Current State Summary

The distributed plugin architecture is **fully functional**:
- FakeAWS plugin runs as external process, passes distributed workflow tests
- AWS plugin converted to external binary, passes SDK tests (S3 bucket lifecycle)
- PluginCoordinator handles both remote (distributed) and local (.so fallback) plugins
- EDF serialization working for all operation types

**Remaining work:**
- Process lifecycle management (graceful shutdown, crash recovery)
- Azure plugin conversion
- Multi-host support

---

## Architecture Overview

### Component Diagram

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           Agent Process                                  │
│  ┌───────────────────────────────────────────────────────────────────┐  │
│  │                 Ergo Node (formae@localhost)                      │  │
│  │                                                                   │  │
│  │  ┌─────────────────┐    ┌─────────────────┐                      │  │
│  │  │ ResourceUpdater │───▶│PluginCoordinator│                      │  │
│  │  │ (FSM)           │    │                 │                      │  │
│  │  └─────────────────┘    │ • Plugin registry                      │  │
│  │         │               │ • SpawnPluginOperator()                │  │
│  │         │               │ • Remote vs local dispatch             │  │
│  │         │               └────────┬────────┘                      │  │
│  │         │                        │                               │  │
│  │         │         ┌──────────────┴───────────────┐               │  │
│  │         │         │                              │               │  │
│  │         │    [Remote Plugin]              [Local Fallback]       │  │
│  │         │         │                              │               │  │
│  │         │    RemoteSpawn on              LocalSpawn with         │  │
│  │         │    plugin node                 plugin in Env           │  │
│  │         │         │                              │               │  │
│  │         ▼         ▼                              ▼               │  │
│  │    PluginOperator (ProcessID)                                    │  │
│  │    • Create/Read/Update/Delete/List/Status operations            │  │
│  │    • Runs on plugin node (remote) or agent node (local)          │  │
│  │                                                                   │  │
│  │  ┌─────────────────────────────────────────────────────────────┐ │  │
│  │  │              PluginProcessSupervisor                        │ │  │
│  │  │  • Discovers plugins at ~/.pel/formae/plugins/              │ │  │
│  │  │  • Spawns plugin binaries via meta.Port                     │ │  │
│  │  │  • Captures stdout/stderr for unified logging               │ │  │
│  │  │  • Detects process termination (MessagePortTerminate)       │ │  │
│  │  └─────────────────────────────────────────────────────────────┘ │  │
│  └───────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
              │                              │
         meta.Port                      meta.Port
         (OS process)                   (OS process)
              │                              │
              ▼                              ▼
┌─────────────────────────┐    ┌─────────────────────────┐
│   AWS Plugin Process    │    │  Azure Plugin Process   │
│                         │    │                         │
│  Ergo Node              │    │  Ergo Node              │
│  (aws-plugin@localhost) │    │  (azure-plugin@...)     │
│                         │    │                         │
│  plugin.Run(Plugin)     │    │  plugin.Run(Plugin)     │
│  • Registers EDF types  │    │  • Registers EDF types  │
│  • Enables RemoteSpawn  │    │  • Enables RemoteSpawn  │
│  • Announces to agent   │    │  • Announces to agent   │
│                         │    │                         │
│  PluginOperator FSM     │    │  PluginOperator FSM     │
│  (spawned on demand)    │    │  (spawned on demand)    │
└─────────────────────────┘    └─────────────────────────┘
```

### Key Components

**PluginCoordinator** (`internal/metastructure/plugin_coordinator/`)
- Maintains registry of available plugins (namespace → node mapping)
- Receives `PluginAnnouncement` messages from plugin processes
- Handles `SpawnPluginOperator` requests from ResourceUpdater
- Decides remote vs local spawn based on plugin registration

**PluginProcessSupervisor** (`internal/metastructure/plugin_process_supervisor/`)
- Discovers external plugins at `~/.pel/formae/plugins/{namespace}/v{version}/{namespace}-plugin`
- Spawns plugin binaries as OS processes via Ergo's `meta.Port`
- Captures stdout/stderr and logs with namespace prefix
- Detects process termination via `MessagePortTerminate`

**PluginOperator** (`pkg/plugin/plugin_operator.go`)
- FSM that executes CRUD operations against ResourcePlugin
- Runs on plugin node (remote) or agent node (local fallback)
- Gets ResourcePlugin instance from Ergo environment
- Handles retries, status checks, and progress reporting

**plugin.Run()** (`pkg/plugin/run.go`)
- Entry point for plugin binaries
- Sets up Ergo node with networking enabled
- Registers EDF types for message serialization
- Enables RemoteSpawn for PluginOperator factory
- Announces plugin to agent's PluginCoordinator

### Plugin Discovery Flow

1. Agent starts, creates `PluginManager` which scans `~/.pel/formae/plugins/`
2. `PluginProcessSupervisor` gets list of external plugins from PluginManager
3. For each plugin, spawns binary via `meta.Port` with environment:
   - `FORMAE_AGENT_NODE=formae@localhost`
   - `FORMAE_PLUGIN_NODE={namespace}-plugin@localhost`
   - `FORMAE_NETWORK_COOKIE={secret}`
4. Plugin binary starts, calls `plugin.Run(resourcePlugin)`
5. Plugin's `PluginActor` sends `PluginAnnouncement` to agent's PluginCoordinator
6. PluginCoordinator registers plugin (namespace → node mapping)
7. PluginCoordinator registers namespace with RateLimiter

### Operation Flow (e.g., Create Resource)

1. ResourceUpdater needs to create resource with type `AWS::S3::Bucket`
2. ResourceUpdater calls `PluginCoordinator.SpawnPluginOperator("AWS", uri, "Create", opID)`
3. PluginCoordinator checks if "AWS" is registered (remote plugin)
4. If registered: `RemoteSpawn` on `aws-plugin@localhost` node
5. If not registered: fallback to local spawn with .so plugin (if available)
6. Returns `ProcessID` (registered name + node)
7. ResourceUpdater sends `CreateResource{...}` message to ProcessID
8. PluginOperator FSM handles operation, calls `plugin.Create()`
9. Returns `ProgressResult` to ResourceUpdater

---

## Completed Work

### Phase 1: Core Architecture ✅

- [x] Enable Ergo networking in metastructure
- [x] Register EDF types for message serialization (dependency order matters!)
- [x] Create PluginCoordinator actor
- [x] Create PluginProcessSupervisor actor
- [x] Move PluginOperator to `pkg/plugin/` for sharing between agent and plugins
- [x] Implement `plugin.Run()` entry point for plugin binaries
- [x] Plugin announcement and registration flow
- [x] Remote spawn of PluginOperator on plugin nodes
- [x] Local fallback for .so plugins

### Phase 2: FakeAWS Distributed Tests ✅

- [x] Convert FakeAWS to standalone binary with `main.go`
- [x] Distributed workflow test passing (`TestMetastructure_ApplyForma_DistributedPlugin`)
- [x] EDF serialization working for all operation types

### Phase 3: AWS Plugin Conversion ✅

- [x] Add `main.go` to `plugins/aws/` calling `plugin.Run(Plugin)`
- [x] Update Makefile: `build-aws-plugin` target builds to `~/.pel/formae/plugins/aws/v${VERSION}/aws-plugin`
- [x] Remove .so build for AWS plugin
- [x] AWS SDK tests passing (S3 bucket lifecycle test)

---

## Next Steps

### Phase 4: Process Lifecycle Management

#### 4.1 Graceful Shutdown (Agent → Plugins)

**Problem:** When the agent stops, plugin processes are NOT automatically terminated.
meta.Port spawns independent OS processes that continue running after parent actor exits.

**Solution:**

1. Add `ShutdownPlugins` message type handled by PluginProcessSupervisor

2. PluginProcessSupervisor needs to track spawned process PIDs:
   - Option A: Parse `/proc` to find child processes
   - Option B: Have plugin report its PID via announcement
   - Option C: Use Ergo network to send shutdown message to plugin node

3. On shutdown, send SIGTERM to each plugin process

4. In `Metastructure.Stop()`:
   ```go
   // Before stopping the node
   m.sendShutdownToPlugins()
   // Wait for plugins to exit (with reasonable timeout)
   m.waitForPluginShutdown(30 * time.Second)
   // Then stop the node
   m.Node.Stop() // or StopForce()
   ```

**Note:** Hold off on SIGKILL fallback for now - prefer graceful shutdown only.

#### 4.2 Plugin Process Restart on Crash

**Problem:** When a plugin crashes, PluginProcessSupervisor detects it via
`MessagePortTerminate` but doesn't restart it.

**Solution:**

1. In `HandleMessage` for `MessagePortTerminate`, trigger restart:
   ```go
   case meta.MessagePortTerminate:
       p.Log().Error("Plugin terminated", "namespace", msg.Tag)
       if pluginInfo, ok := p.plugins[msg.Tag]; ok {
           pluginInfo.healthy = false
           go p.restartPluginWithBackoff(msg.Tag, pluginInfo)
       }
   ```

2. Implement `restartPluginWithBackoff()`:
   - Track restart count and timestamps per plugin
   - Implement exponential backoff (e.g., 1s, 2s, 4s, 8s, max 60s)
   - Max restart attempts before marking plugin as permanently failed
   - Log each restart attempt

3. When plugin restarts successfully (new announcement received):
   - Reset backoff state
   - Update plugin registry with new node info (if changed)

#### 4.3 Plugin Self-Shutdown on Agent Disconnect

**Problem:** If the agent crashes or network disconnects, plugin processes
become orphans with no way to know they should exit.

**Solution:**

1. In `plugin.Run()`, start a watchdog goroutine:
   ```go
   go func() {
       ticker := time.NewTicker(30 * time.Second)
       missedPings := 0
       maxMissedPings := 3

       for range ticker.C {
           // Try to reach agent node
           _, err := node.Network().GetNode(gen.Atom(agentNode))
           if err != nil {
               missedPings++
               log.Printf("Agent unreachable (%d/%d)", missedPings, maxMissedPings)
               if missedPings >= maxMissedPings {
                   log.Printf("Agent unreachable for too long, shutting down")
                   node.Stop()
                   return
               }
           } else {
               missedPings = 0
           }
       }
   }()
   ```

2. Configuration options (future):
   - Ping interval (default 30s)
   - Max missed pings (default 3)
   - Could be passed via environment variables

### Phase 5: Re-enable Remote Env Spawning (DEFERRED)

**Problem:** `ExposeEnvRemoteSpawn` is currently disabled because some types in
the node environment (Datastore, PluginManager, Context) are not serializable.

**Status:** Deferred - requires refactoring to ensure all env types are serializable.
User will handle this refactoring work.

### Phase 6: Commit & Merge

After Phase 4 is complete:
- [ ] Run full test suite (`make test-all`)
- [ ] Verify distributed plugin tests pass
- [ ] Verify AWS SDK tests pass
- [ ] Clean up any orphan processes from failed test runs
- [ ] Create commit with descriptive message
- [ ] Consider squashing/rebasing for clean history before merge

### Phase 7: Azure Plugin Conversion

Same pattern as AWS:
- [ ] Add `main.go` to `plugins/azure/` calling `plugin.Run(Plugin)`
- [ ] Add `build-azure-plugin` target to Makefile
- [ ] Build to `~/.pel/formae/plugins/azure/v${VERSION}/azure-plugin`
- [ ] Update `test-plugin-sdk-azure` to use new target
- [ ] Verify Azure SDK tests pass

### Phase 8: Multi-Host Support Design

**Goal:** Enable distributed deployment where:
- One agent acts as **primary** (handles API requests, coordinates work)
- Other agents act as **relay** agents (can host plugins, execute operations)

**Design considerations to explore:**

1. **Discovery & Registration**
   - How do relay agents discover the primary?
   - Static configuration vs dynamic discovery (mDNS, consul, etc.)?
   - How does primary track relay agent health?

2. **Work Distribution**
   - Which agent runs which plugins?
   - Plugin affinity (e.g., Azure plugin only on agents with Azure credentials)
   - Load balancing across relay agents

3. **Plugin Placement**
   - Can a plugin run on multiple agents simultaneously?
   - How to handle plugin version differences across agents?
   - Plugin migration when agent fails?

4. **State Management**
   - Shared datastore (all agents connect to same DB)?
   - Primary-only datastore with RPC to relays?
   - State synchronization requirements?

5. **Failure Handling**
   - Primary failure → relay takes over?
   - Relay failure → redistribute plugins?
   - Network partition handling?

6. **Security**
   - Authentication between agents
   - Authorization for plugin operations
   - Credential isolation (plugin creds stay on specific agents)

**This requires significant design work before implementation. Consider creating
a separate design document with diagrams and trade-off analysis.**

---

## Technical Notes

### EDF Type Registration Order

Types must be registered in dependency order. Types embedded in other types
must be registered first. Example:

```go
// 1. Basic types first
edf.RegisterTypeOf(model.FieldHint{})
// 2. Schema depends on FieldHint
edf.RegisterTypeOf(model.Schema{})
// 3. Resource depends on Schema
edf.RegisterTypeOf(model.Resource{})
// 4. Operation messages depend on Resource
edf.RegisterTypeOf(CreateResource{})
```

### meta.Port Behavior

- Spawns independent OS process
- Process continues running after parent actor exits
- `MessagePortTerminate` sent when process exits (not before)
- No built-in graceful shutdown mechanism
- Must explicitly send signals (SIGTERM) to stop process
- Does not directly expose spawned process PID

### Plugin Binary Location

External plugins are discovered at:
```
~/.pel/formae/plugins/{namespace}/v{version}/{namespace}-plugin
```

Example:
```
~/.pel/formae/plugins/aws/v0.75.1/aws-plugin
~/.pel/formae/plugins/azure/v0.75.1/azure-plugin
```

### Build Commands

```bash
# Build AWS plugin to standard location
make build-aws-plugin

# Run AWS SDK tests (builds plugin first)
make test-plugin-sdk-aws

# Run distributed workflow tests
go test -tags integration -run TestMetastructure_ApplyForma_DistributedPlugin ./internal/workflow_tests/distributed/...

# Kill orphan plugin processes (useful after test failures)
pkill -f "aws-plugin"
pkill -f "azure-plugin"
```

---

## Known Issues

### Orphan Plugin Processes

When tests fail or the agent crashes, plugin processes may be left running.
Use `pgrep -f "aws-plugin"` to check and `pkill -f "aws-plugin"` to clean up.

This will be addressed by Phase 4.3 (plugin self-shutdown).

### DynamoDB Test Timeout

The AWS SDK test for DynamoDB table creation sometimes times out. This appears
to be an AWS CloudControl API issue (slow provisioning), not related to the
distributed plugin architecture. The S3 bucket test passes consistently.

### Logging Style Mismatch

Ergo's `Log().Info()` uses Printf-style formatting, not slog-style key-value pairs.
Current workaround: converted visible INFO logs to Printf style. Full rework needed
to properly handle structured logging.

---

## Files Changed (Summary)

**New files:**
- `plugins/aws/main.go` - Entry point for AWS plugin binary
- `plugins/fake-aws/main.go` - Entry point for FakeAWS plugin binary
- `pkg/plugin/run.go` - Plugin entry point and EDF registration
- `pkg/plugin/plugin_operator.go` - PluginOperator FSM (moved from internal)
- `pkg/plugin/actor.go` - PluginActor for announcement
- `pkg/plugin/application.go` - PluginApplication supervisor
- `internal/metastructure/plugin_coordinator/` - Plugin registry and spawn coordination
- `internal/metastructure/plugin_process_supervisor/` - OS process management
- `internal/metastructure/edf_types.go` - EDF type registration for agent

**Modified files:**
- `Makefile` - Added `build-aws-plugin`, removed AWS .so build
- `internal/metastructure/metastructure.go` - Enable networking
- `internal/metastructure/application.go` - Add new actors
- Various FSM handlers - Updated signatures for statemachine library changes
