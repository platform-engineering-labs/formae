# Plan: Process Lifecycle Management for Distributed Plugins

**Date**: 2025-11-29
**Branch**: `feat/distributed-plugins`
**Status**: ✅ COMPLETED

---

## Overview

Implement robust process lifecycle management for distributed plugins:
1. ✅ **Plugin crash → Agent restarts it** (with max retry limit)
2. ✅ **Agent crash → Plugin detects and shuts down** (via process monitoring)
3. ✅ **Plugin crash → PluginCoordinator unregisters it** (for clean re-registration)

---

## Implementation Summary

### Phase 1: Plugin Restart on Crash ✅

**Files modified**:
- `internal/metastructure/plugin_process_supervisor/plugin_process_supervisor.go`

**Changes**:
- Added `restartCount` and `lastCrashTime` to `PluginInfo`
- Added `MaxPluginRestarts = 5` constant
- Implemented restart logic in `HandleMessage` for `meta.MessagePortTerminate`

**Test**: `TestPluginRestart_AfterCrash` - PASSING

---

### Phase 2: Plugin Unregistration on Crash ✅

**Files modified**:
- `internal/metastructure/messages/plugin.go` - Added `UnregisterPlugin` message
- `internal/metastructure/plugin_coordinator/plugin_coordinator.go` - Handle unregistration
- `internal/metastructure/plugin_process_supervisor/plugin_process_supervisor.go` - Send unregistration

**Changes**:
- Added `UnregisterPlugin` message with `Namespace` and `Reason` fields
- PluginCoordinator removes plugin from registry on `UnregisterPlugin`
- PluginProcessSupervisor sends `UnregisterPlugin` before restart attempt

---

### Phase 3: Plugin Self-Shutdown on Agent Failure ✅

**Files modified**:
- `pkg/plugin/actor.go` - Add process monitoring

**Key Learning**: `MonitorProcessID` cannot be called from `Init()` because Ergo requires
the process to be in `Running` state. The solution is to send a message to self at the
end of `Init()`, then call `MonitorProcessID` from `HandleMessage`.

**Changes**:
- Added `setupMonitoring` struct for deferred monitoring setup
- In `Init()`: `p.Send(p.PID(), setupMonitoring{})` after announcement
- In `HandleMessage`:
  - Handle `setupMonitoring` → call `MonitorProcessID(PluginCoordinator)`
  - Handle `gen.MessageDownProcessID` → shut down if PluginCoordinator terminates
  - Handle `gen.MessageDownNode` → shut down if agent node goes down

**Test**: `TestPluginShutdown_AfterAgentStop` - PASSING

---

## Test Results

All tests pass:
- `TestPluginRestart_AfterCrash` ✅
- `TestPluginShutdown_AfterAgentStop` ✅
- `TestMetastructure_ApplyForma_DistributedPlugin_HappyPath` ✅

---

## Open Questions (Resolved)

1. ✅ Backoff strategy: Immediate restart (no backoff for simplicity)
2. ✅ Max restart count: 5 attempts (const in PluginProcessSupervisor)
3. ✅ Notify PluginCoordinator: Yes, via `UnregisterPlugin` message
4. ✅ Test approach: Integration tests with real processes, `pkill -9` acceptable
5. ✅ MonitorNode "not allowed": Use deferred `MonitorProcessID` via self-send

---

## Success Criteria (All Met)

1. ✅ `TestPluginRestart_AfterCrash` passes consistently
2. ✅ `TestPluginShutdown_AfterAgentStop` passes consistently
3. ✅ Existing distributed plugin tests still pass
4. ✅ No orphan plugin processes after test runs
