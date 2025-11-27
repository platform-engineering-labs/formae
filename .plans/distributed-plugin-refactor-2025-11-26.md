# Distributed Plugin Architecture Refactor

**Date:** 2025-11-26
**Branch:** `feat/distributed-plugins`
**Status:** In Progress

---

## Architectural Decision

### Problem

The previous implementation attempted to bypass the existing PluginOperator/ResourceUpdater
flow by creating a simplified `pkg/plugin/operator.go` and modifying `doPluginOperation`
to directly remote-spawn and call this simplified operator. This approach:

1. Duplicated logic between the FSM-based PluginOperator and the simplified version
2. Required new request/response types for network serialization
3. Bypassed the existing PluginOperatorSupervisor without clear benefit
4. Created friction when trying to support both local and remote modes

### Insight: PluginOperatorSupervisor is an Anti-Pattern

The PluginOperatorSupervisor supervises PluginOperator FSMs. However:

- PluginOperator is a **stateful FSM** - if it crashes mid-operation, restarting it
  in the initial state doesn't help recovery
- ResourceUpdater already handles missing operators gracefully via `PluginMissingInAction`
- The supervision overhead adds complexity without meaningful benefit

### New Architecture

**Remove PluginOperatorSupervisor entirely.** Instead:

```
ResourceUpdater
  → Call PluginCoordinator.SpawnPluginOperator(namespace, resourceURI, operation, operationID)
  → PluginCoordinator: RemoteSpawnRegister (or local SpawnRegister), uses actornames for stable name
  → PluginCoordinator: Returns ProcessID (name from actornames + node)
  → ResourceUpdater: Sends operation messages (ReadResource, CreateResource, etc.) to ProcessID
```

**Key principles:**
1. **Network transparency**: ResourceUpdater doesn't know if operator is local or remote
2. **Single dispatch point**: PluginCoordinator handles local vs remote decision
3. **Registered names**: Use actornames package + RemoteSpawnRegister for stable ProcessIDs
4. **Graceful degradation**: Local fallback for testing, remote for production
5. **Property resolution on agent**: ResourceUpdater calls `resolver.ConvertToPluginFormat` BEFORE
   sending operation messages, so PluginOperator receives already-resolved properties

### How PluginOperator Gets the Plugin Instance

**Remote case (distributed plugin binary):**
- Plugin binary calls `plugin.Run(resourcePlugin)`
- `plugin.Run()` stores the ResourcePlugin in Ergo environment
- PluginOperator retrieves plugin via `Env("Plugin")`

**Local case (fallback with .so plugins):**
- PluginCoordinator gets plugin from PluginManager
- PluginCoordinator spawns PluginOperator with `gen.ProcessOptions{Env: map[string]any{"Plugin": plugin}}`
- PluginOperator retrieves plugin via `Env("Plugin")` (same code path as remote)

### Local vs Remote Mode

**Implicit fallback logic in PluginCoordinator:**

```go
func (c *PluginCoordinator) SpawnPluginOperator(namespace, ...) (gen.ProcessID, error) {
    name := actornames.PluginOperator(resourceURI, operation, operationID)

    // 1. Check if plugin is registered (remote announcement received)
    if plugin, ok := c.plugins[namespace]; ok {
        // Remote spawn on plugin node using RemoteSpawnRegister
        return c.remoteSpawnRegister(plugin.NodeName, name, ...)
    }

    // 2. Fallback: Check if plugin exists in-process (local testing)
    if p := c.pluginManager.GetResourcePlugin(namespace); p != nil {
        // Local spawn with plugin passed via Env
        opts := gen.ProcessOptions{Env: map[string]any{"Plugin": p}}
        return c.localSpawnRegister(name, opts, ...)
    }

    // 3. Error: plugin not found
    return gen.ProcessID{}, fmt.Errorf("plugin not found: %s", namespace)
}
```

**Safeguards:**
- Local plugins scanned from project-relative path (not `~/.pel/plugins/`)
- Distributed tests MUST use `require.Eventually` to wait for plugin registration
- This ensures distributed tests actually test distributed mode (no silent fallback)

---

## Task Breakdown for Happy Path Test

### Phase 1: Clean Up (Revert Poor Implementation) ✅ COMPLETE

- [x] **1.1** Revert `internal/metastructure/resource_update/resource_updater.go`
  - Remove `doPluginOperation` changes that bypass PluginOperatorSupervisor
  - Remove `toNetworkRequest` function
  - Restore original flow that calls PluginOperatorSupervisor

- [x] **1.2** Delete `pkg/plugin/operator.go`
  - This was the incorrectly created simplified operator
  - Also remove references from `pkg/plugin/run.go` (EDF registrations, EnableSpawn)

### Phase 2: Move PluginOperator to pkg/plugin/ ✅ COMPLETE

- [x] **2.1** Move `internal/metastructure/plugin_operation/plugin_operator.go` to `pkg/plugin/operator/`
  - Keep FSM structure intact
  - Update imports
  - Remove dependency on `internal/metastructure/resolver` (property resolution moves to ResourceUpdater)
  - Remove dependency on `internal/metastructure/util` (replace `util.TimeNow()` with `time.Now()`)

- [x] **2.2** Modify PluginOperator to get plugin from Ergo environment
  - In Init(), retrieve plugin via `Env("Plugin")`
  - Store as field on PluginUpdateData or similar
  - Remove PluginManager dependency entirely (both remote and local use Env)

- [x] **2.3** Update `pkg/plugin/run.go`
  - Store ResourcePlugin in Ergo environment so PluginOperator can access it
  - Register PluginOperator factory with `EnableSpawn` for remote spawning
  - Register necessary EDF types for FSM messages (operation types like ReadResource, CreateResource, etc.)

#### Phase 2 Implementation Details (for reference)

**Circular Import Solution:**
The `pkg/plugin/operator` package needed `ResourcePlugin` interface but couldn't import `pkg/plugin`
(would create cycle: `plugin` → `operator` → `plugin`). Solution: Define a local `ResourcePlugin`
interface in operator package with the exact methods needed. Go's implicit interface satisfaction
means `plugin.ResourcePlugin` automatically satisfies `operator.ResourcePlugin`.

**Key Files Created/Modified:**
- `pkg/plugin/operator/operator.go` - New file with PluginOperator FSM
- `pkg/plugin/run.go` - Added EnableSpawn and EDF type registrations
- `pkg/plugin/go.mod` - Added `ergo.services/actor/statemachine` dependency
- All plugin `go.sum` files updated via `go mod tidy`

**Namespace Validation:**
Added `validateNamespace()` helper that checks `operation.Namespace == data.plugin.Namespace()`.
Returns `NamespaceMismatchError` (reused error code from old `PluginNotFoundError`).

**StatusCheck Methods:**
Removed all `resolver.ConvertToPluginFormat` calls from StatusCheck methods. Properties are
expected to arrive already resolved. StatusCheck now just calls `GetMetadata()` directly.

### Phase 3: Rename & Extend PluginRegistry → PluginCoordinator

- [ ] **3.1** Rename `plugin_registry/` to `plugin_coordinator/`
  - Update all references
  - Update actornames

- [ ] **3.2** Add `SpawnPluginOperator` method
  - Takes: namespace, resourceURI, operation, operationID
  - Returns: gen.ProcessID (registered name)
  - Implements remote spawn + local fallback logic

- [ ] **3.3** Remove PluginOperatorSupervisor
  - Delete `plugin_operation/supervisor.go`
  - Update application.go to not spawn it
  - Update ResourceUpdater to call PluginCoordinator instead

### Phase 4: Update ResourceUpdater

- [ ] **4.1** Update `doPluginOperation` to use PluginCoordinator
  - Call `PluginCoordinator.SpawnPluginOperator` instead of PluginOperatorSupervisor
  - Use returned ProcessID to send operation messages
  - Keep `PluginMissingInAction` handling intact

- [ ] **4.2** Move property resolution to ResourceUpdater
  - Call `resolver.ConvertToPluginFormat` on properties BEFORE sending operation messages
  - Modify message construction in `doPluginOperation` to use resolved properties
  - This removes resolver dependency from PluginOperator (which now runs on plugin node)

### Phase 5: FakeAWS & Test

- [ ] **5.1** Update FakeAWS to return dummy properties
  - When no override in context, return identifiable dummy data
  - E.g., `{"bucket_name": "fake-bucket-12345", "region": "fake-region"}`

- [ ] **5.2** Fix happy-path test assertions
  - Wait for plugin registration with `require.Eventually`
  - Verify forma command status is SUCCESS
  - Verify resource exists in database
  - Verify dummy properties from FakeAWS are in database

---

## Test Verification Checklist

The happy-path test should verify:

1. [ ] Plugin binary builds successfully
2. [ ] Plugin process starts and announces to PluginCoordinator
3. [ ] `require.Eventually` confirms plugin is registered
4. [ ] ApplyForma creates a forma command
5. [ ] Forma command completes with SUCCESS status
6. [ ] Resource is persisted in database
7. [ ] Resource properties match FakeAWS dummy data

---

## Important Notes

### Do NOT Publish .so Files for AWS/Azure Plugins

When refactoring the AWS and Azure plugins to use the distributed architecture, ensure that
we **do not publish .so files** for them anymore. The .so loading path in PluginManager is
only kept for local testing (FakeAWS). Production plugins should always be distributed as
standalone binaries that run as separate processes.

---

## Future Work (Not in Scope)

- Process robustness: restart on crash, graceful shutdown, health checks
- Discovery integration with distributed plugins
- Multi-node cluster support
- Plugin version negotiation
- **Logging rework**: Ergo's `Log().Info()` uses Printf-style formatting, not slog-style key-value pairs.
  Current workaround: converted visible INFO logs to Printf style. Full rework needed to either:
  - Update ErgoLogger to properly handle structured fields via `gen.LogField`
  - Or create a wrapper that converts slog-style calls to Printf-style
  - See `internal/logging/substitute_ergo.go` for the current implementation
