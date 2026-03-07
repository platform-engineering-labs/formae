# Type-Agnostic ExecutionDAG Design

## Context

This is Phase 1 completion of the ExecutionDAG refactor. PRs #294 (rename) and
#296 (simplify DAGNode internals) established the foundation. This phase
introduces an `Update` interface that makes the DAG type-agnostic, enabling
future phases to add target updates alongside resource updates.

## Prior Work

- **PR #294**: Renamed Pipeline -> ExecutionDAG, ResourceUpdateGroup -> DAGNode,
  added test coverage (mutation score 50.6% -> 52.7%)
- **PR #296**: Collapsed `Updates []*ResourceUpdate` to `Update *ResourceUpdate`,
  removed redundant methods, renamed Upstream/Downstream to
  Dependencies/Dependents, bidirectional `Unlink`, extracted `removeNode` helper

## Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Interface name | `Update` | Both concrete types are updates (ResourceUpdate, TargetUpdate). Simple and direct. `changeset.Update` reads well for external callers. |
| Interface location | `changeset` package | Only used by DAG internals. No circular import risk. Move to `types/` later if needed. |
| Node lifecycle | Map membership | Completed/failed nodes removed from DAG. No nil sentinels, no state enum on the interface. |
| State methods | `IsReady`, `IsRunning`, `IsSuccess`, `IsFailed`, `MarkInProgress`, `MarkFailed` | Thin — only what the DAG needs. Concrete types own their full state machine. |
| Operation on interface | **Not included** | Operation is baked into `DAGNode.URI` at construction time. The DAG doesn't need to query it from the update. Avoids field/method name collision with `ResourceUpdate.Operation`. |
| OperationType enum | Already shared | `OperationType` already lives in `internal/metastructure/types` and is re-exported by `resource_update`. No move needed. |
| Constructor | `NewExecutionDAG(resourceUpdates, targetUpdates)` | Single entry point. DAG owns the construction logic for both types. |
| Replace splitting | Stays in `Init` | Resource-specific concern. Target updates won't have replace-split logic. Kept inside Init with a type check. |

## Interface

```go
// Update is the interface for items that can be scheduled in the ExecutionDAG.
// Both ResourceUpdate and TargetUpdate implement this interface.
type Update interface {
    NodeURI() pkgmodel.FormaeURI
    Resolvables() []pkgmodel.FormaeURI
    Namespace() string
    IsReady() bool
    IsRunning() bool
    IsSuccess() bool
    IsFailed() bool
    MarkInProgress()
    MarkFailed()
}
```

Method responsibilities:
- `NodeURI()` — unique identifier for dependency resolution
- `Resolvables()` — URIs this update depends on (for edge building)
- `Namespace()` — rate-limiting namespace (e.g. "AWS"). Targets return empty string to skip rate limiting.
- `IsReady()` — can this update be scheduled? (not started)
- `IsRunning()` — is this update currently executing?
- `IsSuccess()` — did this update complete successfully?
- `IsFailed()` — did this update fail or get rejected?
- `MarkInProgress()` — transition to in-progress when scheduled
- `MarkFailed()` — transition to failed (for cascading failures)

### Why Operation is NOT on the interface

The operation type (create, update, delete) is baked into `DAGNode.URI` at
construction time via `createOperationURI()`. After construction, the DAG only
needs the node URI as an opaque key. Methods like `UpdateDAG` receive the node
URI as a parameter rather than deriving it from the update. This:

1. Avoids a Go field/method name collision (`ResourceUpdate.Operation` field vs
   `Operation()` method)
2. Keeps the interface thin — the DAG doesn't need to know about operation types
3. Operation-specific logic (replace splitting, delete reversal) lives in the
   constructor where concrete types are available

## DAGNode

```go
type DAGNode struct {
    URI          pkgmodel.FormaeURI
    Update       Update
    Dependents   []*DAGNode
    Dependencies []*DAGNode
}
```

No changes to the node structure itself — just the `Update` field type widens
from `*resource_update.ResourceUpdate` to `Update`.

## ExecutionDAG

```go
type ExecutionDAG struct {
    Nodes map[pkgmodel.FormaeURI]*DAGNode
}
```

## Constructor

```go
func NewExecutionDAG(
    resourceUpdates []resource_update.ResourceUpdate,
    targetUpdates []target_update.TargetUpdate,
) (*ExecutionDAG, error)
```

This replaces the current `NewExecutionDAG()` + `Init()` pattern. Construction
logic:

1. Split resource replace operations into delete + create (resource-specific)
2. Create DAGNode for each operation (both resource and target updates)
3. Build dependency edges from resolvables (works via `Update` interface)
4. Build delete dependency edges (reversed, resource-specific)
5. Connect delete-to-create for replacements (resource-specific)
6. Build resource-to-target implicit edges (Phase 2)
7. Check for cycles

> **Phase 4 note**: When target config mutability is introduced, target updates
> will also need replace splitting (immutable field change -> delete + create).
> At that point, consider adding `OperationType()` and `CloneWithOperation()` to
> the `Update` interface to make replace splitting generic, or keep it as
> type-specific logic in the constructor.

## Changeset Method Changes

### UpdateDAG

Takes node URI as a parameter (instead of deriving from the update):

```go
func (c *Changeset) UpdateDAG(nodeURI FormaeURI, update Update) ([]Update, error)
```

The executor already knows the node URI from scheduling. This avoids needing
operation on the interface.

### GetExecutableUpdates

Returns `[]Update` instead of `[]*resource_update.ResourceUpdate`:

```go
func (c *Changeset) GetExecutableUpdates(namespace string, max int) []Update
```

The ChangesetExecutor uses a type switch to spawn the right actor:

```go
for _, update := range changeset.GetExecutableUpdates(namespace, tokens) {
    switch u := update.(type) {
    case *resource_update.ResourceUpdate:
        // spawn ResourceUpdater (existing)
    case *target_update.TargetUpdate:
        // spawn TargetUpdater (Phase 2)
    }
}
```

### Tracking updates

`getResourceUpdateIdentifier` becomes `getUpdateIdentifier` and uses the
`DAGNode.URI` (which already encodes the operation) instead of computing from
update fields.

## ResourceUpdate Implementation

`ResourceUpdate` already has most of what's needed. The interface methods are
thin wrappers:

```go
func (r *ResourceUpdate) NodeURI() pkgmodel.FormaeURI      { return r.URI() }
func (r *ResourceUpdate) Resolvables() []pkgmodel.FormaeURI { return r.RemainingResolvables }
func (r *ResourceUpdate) Namespace() string                 { return string(r.DesiredState.Namespace()) }
func (r *ResourceUpdate) IsReady() bool                     { return r.State == ResourceUpdateStateNotStarted }
func (r *ResourceUpdate) IsRunning() bool                   { return r.State == ResourceUpdateStateInProgress }
func (r *ResourceUpdate) IsSuccess() bool                   { return r.State == ResourceUpdateStateSuccess }
func (r *ResourceUpdate) IsFailed() bool {
    return r.State == ResourceUpdateStateFailed || r.State == ResourceUpdateStateRejected
}
func (r *ResourceUpdate) MarkInProgress() { r.State = ResourceUpdateStateInProgress }
func (r *ResourceUpdate) MarkFailed()     { r.State = ResourceUpdateStateFailed }
```

## What Changes in This Phase

1. Define `Update` interface in `changeset` package
3. Add interface methods to `ResourceUpdate`
4. Change `DAGNode.Update` from `*resource_update.ResourceUpdate` to `Update`
5. Change `GetExecutableUpdates` return type to `[]Update`
6. Change `UpdateDAG` to take `nodeURI` parameter
7. Add type assertions in `ChangesetExecutor` where it needs the concrete type
8. Update `NewChangesetFromResourceUpdates` signature (still only takes resource
   updates — target updates added in Phase 2)

## What Does NOT Change

- DAG construction logic (edge building, cycle detection)
- Node lifecycle (map membership)
- Failure cascading
- Rate limiting logic
- Replace splitting

## Testing Strategy

All existing tests must pass with no behavioral changes. New tests:
- Verify `ResourceUpdate` satisfies the `Update` interface (compile-time check)
- Verify type assertions work correctly in executor paths
