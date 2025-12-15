# FormaCommandPersister Performance Optimization Design

## Problem Statement

The `FormaCommandPersister` actor is experiencing timeouts (5s default) when processing high volumes of progress updates. Profiling shows that `json.Unmarshal` and the methods `updateCommandFromProgress` and `markResourceUpdateAsComplete` consume almost all CPU time.

### Root Cause Analysis

1. **Large JSON blobs**: FormaCommands average 430KB, with large commands reaching 1.5MB (196 resources)
2. **Full deserialization on every update**: Each progress update requires:
   - `GetFormaCommandByCommandID` → DB read → `json.Unmarshal` (1.5MB)
   - Modify single ResourceUpdate in memory
   - `json.Marshal` (1.5MB) → DB write
3. **O(n) resource lookup**: Finding the right ResourceUpdate requires iterating through all resources
4. **High update frequency**: Each resource may generate multiple progress updates during execution

### Current Hot Path

```
updateCommandFromProgress / markResourceUpdateAsComplete
    └── GetFormaCommandByCommandID
            └── json.Unmarshal (EXPENSIVE: 1.5MB)
    └── for loop to find resource by Ksuid (O(n), n=196)
    └── modify resource
    └── StoreFormaCommand
            └── json.Marshal (EXPENSIVE: 1.5MB)
            └── DB write
```

## Proposed Solution

Two-pronged approach:
1. **In-memory caching** of active FormaCommands with O(1) resource lookup
2. **Faster JSON library** (`github.com/goccy/go-json`) for remaining marshal/unmarshal operations

### Design Goals

- Eliminate repeated unmarshaling of the same FormaCommand
- O(1) resource lookup by Ksuid instead of O(n) iteration
- Maintain checkpoint semantics (immediate persistence on every update)
- Memory-bounded: only cache active (in-progress) commands
- No schema changes required
- Works for both SQLite and PostgreSQL

### Non-Goals

- Batching updates (violates checkpoint requirement)
- Schema normalization (future consideration, higher risk)
- Reducing DB write frequency (checkpointing is critical)

## Detailed Design

### 1. CachedCommand Structure

```go
// cachedCommand holds an in-memory FormaCommand with optimized lookup structures
type cachedCommand struct {
    command      *forma_command.FormaCommand
    ksuidToIndex map[string]int  // O(1) lookup: ksuid -> ResourceUpdates index
    lastAccessed time.Time       // For potential LRU eviction
}

// buildKsuidIndex creates the ksuid->index mapping for O(1) lookups
func buildKsuidIndex(cmd *forma_command.FormaCommand) map[string]int {
    index := make(map[string]int, len(cmd.ResourceUpdates))
    for i, ru := range cmd.ResourceUpdates {
        index[ru.Resource.Ksuid] = i
    }
    return index
}
```

### 2. Updated FormaCommandPersister

```go
type FormaCommandPersister struct {
    act.Actor
    datastore datastore.Datastore

    // New: in-memory cache for active commands
    activeCommands map[string]*cachedCommand
}

func NewFormaCommandPersister(datastore datastore.Datastore) *FormaCommandPersister {
    return &FormaCommandPersister{
        datastore:      datastore,
        activeCommands: make(map[string]*cachedCommand),
    }
}
```

### 3. Cache Operations

#### 3.1 Get or Load Command

```go
// getOrLoadCommand retrieves from cache or loads from DB
func (f *FormaCommandPersister) getOrLoadCommand(commandID string) (*cachedCommand, error) {
    // Check cache first
    if cached, ok := f.activeCommands[commandID]; ok {
        cached.lastAccessed = time.Now()
        return cached, nil
    }

    // Load from DB
    cmd, err := f.datastore.GetFormaCommandByCommandID(commandID)
    if err != nil {
        return nil, err
    }

    // Build cache entry
    cached := &cachedCommand{
        command:      cmd,
        ksuidToIndex: buildKsuidIndex(cmd),
        lastAccessed: time.Now(),
    }
    f.activeCommands[commandID] = cached

    return cached, nil
}
```

#### 3.2 Find Resource Update by Ksuid (O(1))

```go
// findResourceUpdateIndex returns the index of the ResourceUpdate with the given ksuid
// Returns -1 if not found
func (c *cachedCommand) findResourceUpdateIndex(ksuid string) int {
    if idx, ok := c.ksuidToIndex[ksuid]; ok {
        return idx
    }
    return -1
}
```

#### 3.3 Persist and Potentially Evict

```go
// persistCommand writes the command to DB and evicts from cache if in final state
func (f *FormaCommandPersister) persistCommand(cached *cachedCommand) error {
    cmd := cached.command

    // Perform finalization if needed
    if cmd.IsInFinalState() {
        if _, err := f.hashSensitiveDataIfComplete(cmd); err != nil {
            return err
        }

        if shouldDeleteSyncCommand(cmd) {
            if err := f.datastore.DeleteFormaCommand(cmd, cmd.ID); err != nil {
                return err
            }
            delete(f.activeCommands, cmd.ID)
            return nil
        }
    }

    // Persist to DB
    if err := f.datastore.StoreFormaCommand(cmd, cmd.ID); err != nil {
        return err
    }

    // Evict from cache if command is complete
    if cmd.IsInFinalState() {
        delete(f.activeCommands, cmd.ID)
    }

    return nil
}
```

### 4. Updated Hot Path Methods

#### 4.1 updateCommandFromProgress (Before/After)

**Before:**
```go
func (f *FormaCommandPersister) updateCommandFromProgress(progress *messages.UpdateResourceProgress) (bool, error) {
    command, err := f.datastore.GetFormaCommandByCommandID(progress.CommandID)  // DB + Unmarshal
    // ... O(n) loop to find resource ...
    err = f.datastore.StoreFormaCommand(command, command.ID)  // Marshal + DB
}
```

**After:**
```go
func (f *FormaCommandPersister) updateCommandFromProgress(progress *messages.UpdateResourceProgress) (bool, error) {
    cached, err := f.getOrLoadCommand(progress.CommandID)  // Cache hit or single unmarshal
    if err != nil {
        return false, fmt.Errorf("failed to load Forma command: %w", err)
    }

    command := cached.command

    // Don't update if command is already canceled
    if command.State == forma_command.CommandStateCanceled {
        return true, nil
    }

    // O(1) lookup instead of O(n) iteration
    idx := cached.findResourceUpdateIndex(progress.ResourceURI.KSUID())
    if idx == -1 {
        return true, nil  // Resource not found in this command
    }

    res := &command.ResourceUpdates[idx]

    // Don't update if resource is already canceled
    if res.State == types.ResourceUpdateStateCanceled {
        return true, nil
    }

    if !shouldUpdateResourceUpdate(*res, progress.Progress) {
        return true, nil
    }

    // Update the resource
    res.State = progress.ResourceState
    res.StartTs = progress.ResourceStartTs
    res.ModifiedTs = progress.ResourceModifiedTs

    // Update progress result
    progIdx := slices.IndexFunc(res.ProgressResult, func(p resource.ProgressResult) bool {
        return p.Operation == progress.Progress.Operation
    })
    if progIdx == -1 {
        res.ProgressResult = append(res.ProgressResult, progress.Progress)
    } else {
        res.ProgressResult[progIdx] = progress.Progress
    }
    res.MostRecentProgressResult = progress.Progress

    if progress.ResourceProperties != nil {
        res.Resource.Properties = progress.ResourceProperties
    }
    if progress.ResourceReadOnlyProperties != nil {
        res.Resource.ReadOnlyProperties = progress.ResourceReadOnlyProperties
    }
    if progress.Version != "" {
        res.Version = progress.Version
    }

    // Update command timestamps
    if command.StartTs.IsZero() {
        command.StartTs = progress.ResourceStartTs
    }
    command.ModifiedTs = progress.ResourceModifiedTs
    command.State = overallCommandState(command)

    // Persist immediately (checkpoint)
    return true, f.persistCommand(cached)
}
```

#### 4.2 markResourceUpdateAsComplete (Similar Pattern)

```go
func (f *FormaCommandPersister) markResourceUpdateAsComplete(msg *messages.MarkResourceUpdateAsComplete) (bool, error) {
    cached, err := f.getOrLoadCommand(msg.CommandID)
    if err != nil {
        return false, fmt.Errorf("failed to load Forma command: %w", err)
    }

    command := cached.command

    // O(1) lookup
    idx := cached.findResourceUpdateIndex(msg.ResourceURI.KSUID())
    if idx == -1 {
        return false, fmt.Errorf("resource not found: %s", msg.ResourceURI)
    }

    res := &command.ResourceUpdates[idx]
    res.State = msg.FinalState
    res.StartTs = msg.ResourceStartTs
    res.ModifiedTs = msg.ResourceModifiedTs

    if msg.ResourceProperties != nil {
        res.Resource.Properties = msg.ResourceProperties
    }
    if msg.ResourceReadOnlyProperties != nil {
        res.Resource.ReadOnlyProperties = msg.ResourceReadOnlyProperties
    }
    if msg.Version != "" {
        res.Version = msg.Version
    }

    command.ModifiedTs = msg.ResourceModifiedTs
    command.State = overallCommandState(command)

    return true, f.persistCommand(cached)
}
```

### 5. JSON Library Swap

Replace `encoding/json` with `github.com/goccy/go-json` for 2-3x faster marshal/unmarshal.

#### 5.1 Changes Required

**In `internal/metastructure/datastore/sqlite.go`:**
```go
import (
    json "github.com/goccy/go-json"  // Drop-in replacement
)
```

**In `internal/metastructure/datastore/postgres.go`:**
```go
import (
    json "github.com/goccy/go-json"  // Drop-in replacement
)
```

#### 5.2 Compatibility Notes

- `goccy/go-json` is API-compatible with `encoding/json`
- Supports all standard struct tags (`json:"name"`, `omitempty`, etc.)
- Well-maintained, used in production by many companies
- Falls back gracefully for edge cases

### 6. Memory Considerations

#### 6.1 Cache Size Estimation

- Typical active commands: 1-10 concurrent
- Command size: ~1.5MB max
- Cache overhead: ~10% (index maps)
- **Expected memory usage: 2-20MB** (negligible)

#### 6.2 Cache Eviction Strategy

**Primary eviction**: Commands are removed when they reach a final state (Success, Failed, Canceled).

This is sufficient because:
- Active commands are bounded by rate limiting
- Commands naturally complete and get evicted
- Memory footprint is small (2-20MB expected)

**Future consideration**: If memory issues arise in practice, we can add:
- Time-based cleanup for abandoned commands
- LRU eviction with a cap (e.g., 50 commands)

### 7. Other Methods to Update

The following methods also load commands and should use the cache:

| Method | Current Pattern | Update Required |
|--------|----------------|-----------------|
| `storeNewFormaCommand` | Creates new command | Add to cache after store |
| `loadFormaCommand` | Direct DB load | Use cache |
| `markResourcesAsRejected` | Load + iterate + store | Use cache + O(1) lookup |
| `markResourcesAsFailed` | Load + iterate + store | Use cache + O(1) lookup |
| `markResourcesAsCanceled` | Load + iterate + store | Use cache + O(1) lookup |
| `bulkUpdateResourceState` | Load + iterate + store | Use cache + O(1) lookups |

### 8. Testing Strategy

#### 8.1 Unit Tests

1. Cache hit/miss behavior
2. O(1) lookup correctness
3. Cache eviction on final state
4. Concurrent access safety (actor is single-threaded, but good to verify)

#### 8.2 Integration Tests

1. Full workflow with caching enabled
2. Agent restart with in-flight commands (cache rebuild from DB)
3. Memory usage under load

#### 8.3 Performance Tests

1. Benchmark: Current vs cached implementation
2. Measure: Time per progress update
3. Measure: Memory usage over time

### 9. Rollout Plan

1. **Phase 1**: JSON library swap (low risk, immediate benefit)
2. **Phase 2**: Add caching infrastructure (cache struct, get/load helper)
3. **Phase 3**: Update hot path methods one at a time
4. **Phase 4**: Update remaining methods
5. **Phase 5**: Add metrics/observability for cache hit rate

### 10. Expected Performance Improvement

| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| Unmarshal per update | 1 | 0 (cache hit) or 1 (cache miss) | ~95% reduction |
| Resource lookup | O(n) | O(1) | ~196x faster |
| Marshal per update | 1 | 1 | No change (checkpoint required) |
| JSON speed | 1x | 2-3x | 2-3x faster |

**Combined effect**: For a command with 196 resources and 5 updates per resource (980 total updates):
- Before: 980 unmarshals + 980 O(196) lookups + 980 marshals
- After: 1 unmarshal + 980 O(1) lookups + 980 marshals (2-3x faster)

**Conservative estimate**: 60-70% reduction in CPU time for the persister.

## Design Decisions

1. **No cache size limits for now** - Commands evict naturally on final state; memory footprint is small. Can add LRU/time-based eviction later if needed.
2. **No cache warming on startup** - Incomplete commands are rare, agent restarts are rare. Can optimize later if needed.
3. **Metrics** - Consider adding cache hit rate monitoring in future iteration.

## Appendix: Files to Modify

1. `internal/metastructure/forma_persister/forma_command_persister.go` - Main changes
2. `internal/metastructure/datastore/sqlite.go` - JSON library import
3. `internal/metastructure/datastore/postgres.go` - JSON library import
4. `go.mod` - Add `github.com/goccy/go-json` dependency
