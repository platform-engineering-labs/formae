# Type-Agnostic ExecutionDAG Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the ExecutionDAG work through an `Update` interface so it can schedule both ResourceUpdates and (future) TargetUpdates.

**Architecture:** Define a thin `Update` interface in the changeset package. ResourceUpdate implements it via adapter methods. DAGNode.Update widens from `*resource_update.ResourceUpdate` to `Update`. The ChangesetExecutor uses type assertions where it needs resource-specific fields. Operation type is NOT on the interface — it's baked into `DAGNode.URI` at construction time.

**Tech Stack:** Go interfaces, no new dependencies.

**Design doc:** `docs/plans/2026-03-06-type-agnostic-execution-dag-design.md`

**Branch:** `refactor/simplify-execution-dag` (worktree `.worktrees/simplify-dag`, stacked on PR #296)

**Important:** This is a pure refactoring — no behavioral changes. All existing tests must keep passing.

---

### Task 1: Establish green baseline

**Step 1: Run changeset tests**

Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && go test ./internal/metastructure/changeset/... -count=1 2>&1 | tail -5`
Expected: All tests PASS

**Step 2: Run full test suite**

Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && make test-all 2>&1 | tail -10`
Expected: All tests PASS

---

### Task 2: Define Update interface and implement on ResourceUpdate

**Files:**
- Modify: `internal/metastructure/changeset/changeset.go` (add interface)
- Modify: `internal/metastructure/resource_update/resource_update.go` (add adapter methods)
- Modify: `internal/metastructure/changeset/changeset_test.go` (add compile-time check)

**Step 1: Add the Update interface to changeset.go**

Add before the `Changeset` struct definition:

```go
// Update is the interface for items that can be scheduled in the ExecutionDAG.
// Both ResourceUpdate and (future) TargetUpdate implement this interface.
//
// Operation type is intentionally NOT part of this interface. It is baked into
// DAGNode.URI at construction time via createOperationURI(). After construction,
// the DAG treats node URIs as opaque keys.
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

**Step 2: Add adapter methods to ResourceUpdate**

Add to `resource_update.go`:

```go
// Update interface implementation for ExecutionDAG integration

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

**Step 3: Add compile-time interface test**

Add to `changeset_test.go`:

```go
func TestResourceUpdate_ImplementsUpdateInterface(t *testing.T) {
	var _ Update = (*resource_update.ResourceUpdate)(nil)
}
```

**Step 4: Run test**

Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && go test ./internal/metastructure/changeset/... -run TestResourceUpdate_ImplementsUpdateInterface -v`
Expected: PASS

**Step 5: Commit**

```
refactor(changeset): define Update interface and implement on ResourceUpdate
```

---

### Task 3: Widen DAGNode.Update and fix changeset.go

Changing `DAGNode.Update` from `*resource_update.ResourceUpdate` to `Update`
breaks compilation throughout changeset.go. All fixes must be applied before
the code compiles again.

**Key principle:** Operation is NOT on the interface. The DAG uses `DAGNode.URI`
(which already encodes the operation) as the key. Methods that previously
derived the URI from update fields now receive it as a parameter or use the
node's URI directly.

**Files:**
- Modify: `internal/metastructure/changeset/changeset.go`

**Step 1: Change DAGNode.Update field type**

```go
type DAGNode struct {
	URI          pkgmodel.FormaeURI
	Update       Update  // was: *resource_update.ResourceUpdate
	Dependents   []*DAGNode
	Dependencies []*DAGNode
}
```

**Step 2: Fix DAGNode methods**

```go
func (n *DAGNode) IsRunning() bool {
	return n.Update.IsRunning()
}

func (n *DAGNode) IsReady() bool {
	return n.Update.IsReady()
}
```

**Step 3: Fix getResourceUpdateIdentifier → getUpdateIdentifier**

Use `DAGNode.URI` instead of computing from update fields. Since this function
is called where we have the node (or can look it up), change to use the node
URI directly. The simplest approach: pass the node URI as parameter.

```go
func getUpdateIdentifier(nodeURI pkgmodel.FormaeURI) string {
	return string(nodeURI)
}
```

Or even simpler — inline it, since it's now just `string(nodeURI)`. But keeping
a named function preserves the intent for readability.

Update callers in `GetExecutableUpdates` and `AvailableExecutableUpdates` to
pass `node.URI` instead of `node.Update`.

**Step 4: Fix GetExecutableUpdates**

```go
func (c *Changeset) GetExecutableUpdates(namespace string, max int) []Update {
	var executable []Update
	total := 0

	for _, node := range c.DAG.Nodes {
		if !node.IsReady() || len(node.Dependencies) > 0 {
			continue
		}

		updateKey := getUpdateIdentifier(node.URI)
		if total < max && node.Update.Namespace() == namespace && !c.trackedUpdates[updateKey] {
			executable = append(executable, node.Update)
			c.trackedUpdates[updateKey] = true
			node.Update.MarkInProgress()
			total++
		}
	}

	sort.Slice(executable, func(i, j int) bool {
		return string(executable[i].NodeURI()) < string(executable[j].NodeURI())
	})

	return executable
}
```

**Step 5: Fix AvailableExecutableUpdates**

```go
func (c *Changeset) AvailableExecutableUpdates() map[string]int {
	result := make(map[string]int)
	for _, node := range c.DAG.Nodes {
		if !node.IsReady() || len(node.Dependencies) > 0 {
			continue
		}

		updateKey := getUpdateIdentifier(node.URI)
		if !c.trackedUpdates[updateKey] {
			ns := node.Update.Namespace()
			result[ns]++
		}
	}

	return result
}
```

**Step 6: Fix UpdateDAG**

Takes `nodeURI` as parameter instead of deriving from the update:

```go
func (c *Changeset) UpdateDAG(nodeURI pkgmodel.FormaeURI, update Update) ([]Update, error) {
	node, exists := c.DAG.Nodes[nodeURI]
	if !exists {
		return nil, fmt.Errorf("DAG node not found for URI: %s", nodeURI)
	}

	if update.IsSuccess() {
		c.removeNode(node, nodeURI)
		return nil, nil
	}

	if update.IsFailed() {
		failedUpdates := c.failUpdate(node)

		if len(failedUpdates) > 1 {
			slog.Debug("Cascading failure detected",
				"originalFailure", update.NodeURI(),
				"cascadingCount", len(failedUpdates)-1)
		}

		// Remove each cascading failed node from the DAG
		for _, failedNode := range failedUpdates {
			c.removeNode(failedNode, failedNode.URI)
		}

		// Remove the original failed node
		c.removeNode(node, nodeURI)

		// Collect the updates from the failed nodes for the caller
		var failedNodeUpdates []Update
		for _, fn := range failedUpdates {
			failedNodeUpdates = append(failedNodeUpdates, fn.Update)
		}
		failedNodeUpdates = append(failedNodeUpdates, update)
		return failedNodeUpdates, nil
	}

	// Any other state reaching here is unexpected — treat as failure
	slog.Warn("Unexpected update state in UpdateDAG, treating as failure",
		"uri", update.NodeURI())
	update.MarkFailed()
	return c.UpdateDAG(nodeURI, update)
}
```

**Step 7: Fix failResourceUpdate → failUpdate**

Since we now work with nodes (which have URIs), change to accept and return
`*DAGNode` instead of deriving URIs from updates:

```go
func (c *Changeset) failUpdate(node *DAGNode) []*DAGNode {
	var failedNodes []*DAGNode
	visited := make(map[pkgmodel.FormaeURI]bool)

	c.recursivelyFailDependents(node, &failedNodes, visited)

	return failedNodes
}

func (c *Changeset) recursivelyFailDependents(node *DAGNode, failedNodes *[]*DAGNode, visited map[pkgmodel.FormaeURI]bool) {
	if visited[node.URI] {
		return
	}
	visited[node.URI] = true

	for _, downstream := range node.Dependents {
		if downstream.Update.IsReady() {
			downstream.Update.MarkFailed()
			*failedNodes = append(*failedNodes, downstream)

			c.recursivelyFailDependents(downstream, failedNodes, visited)
		}
	}
}
```

**Step 8: Fix PrintDAG**

Operation info is already in `node.URI` (which includes the operation suffix).
No need to access update fields:

```go
func (c *Changeset) PrintDAG() string {
	var result strings.Builder
	fmt.Fprintf(&result, "Changeset DAG: %s\n", c.CommandID)
	result.WriteString("========================\n")

	for uri, node := range c.DAG.Nodes {
		fmt.Fprintf(&result, "Node: %s\n", uri)

		result.WriteString("  Dependencies:\n")
		for _, dep := range node.Dependencies {
			fmt.Fprintf(&result, "    - %s\n", dep.URI)
		}

		result.WriteString("  Dependents:\n")
		for _, dep := range node.Dependents {
			fmt.Fprintf(&result, "    - %s\n", dep.URI)
		}

		result.WriteString("\n")
	}

	return result.String()
}
```

**Step 9: Fix IsComplete**

```go
func (c *Changeset) IsComplete() bool {
	if len(c.DAG.Nodes) == 0 {
		return true
	}

	for _, node := range c.DAG.Nodes {
		if !node.Update.IsFailed() {
			return false
		}
	}

	return true
}
```

**Step 10: Note on Init and edge builders**

`Init()` and the edge builder methods (`buildDeleteDependencies`,
`buildCreateUpdateDependencies`, `connectDeleteToCreate`) work on
`[]resource_update.ResourceUpdate` directly — they have the concrete type. They
store `*resource_update.ResourceUpdate` into `DAGNode.Update` which satisfies
the `Update` interface. These methods should need minimal changes beyond
ensuring the pointer stored in `DAGNode.Update` is `*resource_update.ResourceUpdate`.

Verify that in `Init`, when creating nodes:

```go
p.Nodes[operationURI] = &DAGNode{
    URI:          operationURI,
    Update:       update,  // *resource_update.ResourceUpdate satisfies Update
    Dependents:   []*DAGNode{},
    Dependencies: []*DAGNode{},
}
```

**Step 11: Verify compilation**

Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && go build ./internal/metastructure/changeset/...`
Expected: Compiles

**Step 12: Run changeset tests**

Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && go test ./internal/metastructure/changeset/... -count=1 2>&1 | tail -10`
Expected: All tests PASS

**Step 13: Commit**

```
refactor(changeset): widen DAGNode.Update to Update interface
```

---

### Task 4: Fix changeset_executor.go

The executor accesses resource-specific fields through `node.Update`. After the
interface change, these need type assertions.

**Files:**
- Modify: `internal/metastructure/changeset/changeset_executor.go`

**Step 1: Fix resourceUpdateFinished — canceling state**

Use interface methods where possible, type-assert for resource-specific state
setting:

```go
// In the StateCanceling block:
for _, node := range data.changeset.DAG.Nodes {
    if node.Update.NodeURI() == message.Uri && node.Update.IsRunning() {
        if ru, ok := node.Update.(*resource_update.ResourceUpdate); ok {
            ru.State = message.State
        }
        proc.Log().Debug("In-progress resource finished during cancellation",
            "uri", message.Uri,
            "finalState", message.State)
        break
    }
}

// In-progress count check uses interface:
for _, node := range data.changeset.DAG.Nodes {
    if node.IsRunning() {
        inProgressCount++
    }
}
```

**Step 2: Fix resourceUpdateFinished — find finished update**

```go
var finishedUpdate *resource_update.ResourceUpdate

for _, node := range data.changeset.DAG.Nodes {
    if node.Update.NodeURI() == message.Uri && node.Update.IsRunning() {
        if ru, ok := node.Update.(*resource_update.ResourceUpdate); ok {
            finishedUpdate = ru
        }
        break
    }
}

// "Still tracked" check uses interface:
for _, node := range data.changeset.DAG.Nodes {
    if node.Update.NodeURI() == message.Uri {
        stillTracked = true
        break
    }
}
```

**Step 3: Fix resourceUpdateFinished — UpdateDAG call**

`UpdateDAG` now takes `nodeURI` parameter. Compute it from the finished update
(we have the concrete type here):

```go
nodeURI := createOperationURI(finishedUpdate.URI(), finishedUpdate.Operation)
cascadingFailures, err := data.changeset.UpdateDAG(nodeURI, finishedUpdate)
```

**Step 4: Fix resourceUpdateFinished — cascading failures**

Cascading failures are now `[]Update`. Type-assert for resource-specific fields:

```go
if len(cascadingFailures) > 0 {
    var failedResources []forma_persister.ResourceUpdateRef
    for _, failedUpdate := range cascadingFailures {
        ru, ok := failedUpdate.(*resource_update.ResourceUpdate)
        if !ok {
            continue // skip non-resource updates (future: handle target updates)
        }
        if ru.URI() == finishedUpdate.URI() && ru.Operation == finishedUpdate.Operation {
            continue // skip original failure
        }
        failedResources = append(failedResources, forma_persister.ResourceUpdateRef{
            URI:       ru.URI(),
            Operation: ru.Operation,
        })
    }
    // ... rest unchanged
}
```

**Step 5: Fix startResourceUpdates**

Change parameter type and type-assert inside:

```go
func startResourceUpdates(updates []Update, commandID string, proc gen.Process) error {
	for _, update := range updates {
		ru, ok := update.(*resource_update.ResourceUpdate)
		if !ok {
			proc.Log().Error("Unexpected update type in startResourceUpdates", "uri", update.NodeURI())
			continue
		}
		// ... rest uses `ru` instead of `update`
	}
	return nil
}
```

**Step 6: Fix cancel**

```go
for _, node := range data.changeset.DAG.Nodes {
    if node.Update.IsReady() {
        if ru, ok := node.Update.(*resource_update.ResourceUpdate); ok {
            resourcesToCancel = append(resourcesToCancel, forma_persister.ResourceUpdateRef{
                URI:       ru.URI(),
                Operation: ru.Operation,
            })
        }
    } else if node.Update.IsRunning() {
        inProgressCount++
    }
}
```

**Step 7: Fix changesetHasUserUpdates**

```go
func changesetHasUserUpdates(changeset Changeset) bool {
	for _, node := range changeset.DAG.Nodes {
		if ru, ok := node.Update.(*resource_update.ResourceUpdate); ok {
			if ru.Source == resource_update.FormaCommandSourceUser {
				return true
			}
		}
	}
	return false
}
```

**Step 8: Fix collectStacksWithDeletes**

```go
func collectStacksWithDeletes(dag *ExecutionDAG) []string {
	stackSet := make(map[string]struct{})
	for _, node := range dag.Nodes {
		if ru, ok := node.Update.(*resource_update.ResourceUpdate); ok {
			if ru.Operation == resource_update.OperationDelete || ru.Operation == resource_update.OperationReplace {
				stackLabel := ru.StackLabel
				if stackLabel != "" && stackLabel != constants.UnmanagedStack {
					stackSet[stackLabel] = struct{}{}
				}
			}
		}
	}

	stacks := make([]string, 0, len(stackSet))
	for stack := range stackSet {
		stacks = append(stacks, stack)
	}
	return stacks
}
```

**Step 9: Verify compilation**

Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && go build ./internal/metastructure/changeset/...`
Expected: Compiles

**Step 10: Run changeset tests**

Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && go test ./internal/metastructure/changeset/... -count=1 2>&1 | tail -10`
Expected: All tests PASS

**Step 11: Commit**

```
refactor(changeset): update ChangesetExecutor for Update interface
```

---

### Task 5: Fix tests and external callers

**Files:**
- Modify: `internal/metastructure/changeset/changeset_test.go`
- Possibly modify: files outside changeset that call `GetExecutableUpdates`, `UpdateDAG`, etc.

**Step 1: Search for external callers**

Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && grep -r "GetExecutableUpdates\|UpdateDAG\|AvailableExecutableUpdates\|failResourceUpdate" internal/ --include="*.go" | grep -v changeset/`

Fix any external compilation errors found.

**Step 2: Fix test assertions on GetExecutableUpdates results**

`GetExecutableUpdates` now returns `[]Update`. Tests that access
resource-specific fields need type assertions:

```go
// Before:
updates := changeset.GetExecutableUpdates("AWS", 100)
assert.Equal(t, "test-subnet-1", updates[0].DesiredState.Label)

// After:
updates := changeset.GetExecutableUpdates("AWS", 100)
ru := updates[0].(*resource_update.ResourceUpdate)
assert.Equal(t, "test-subnet-1", ru.DesiredState.Label)
```

Apply this pattern throughout the test file.

**Step 3: Fix test calls to UpdateDAG**

`UpdateDAG` now takes `(nodeURI, update)`. Tests that call UpdateDAG need to
compute the nodeURI:

```go
// Before:
update.State = resource_update.ResourceUpdateStateSuccess
changeset.UpdateDAG(update)

// After:
update.State = resource_update.ResourceUpdateStateSuccess
nodeURI := createOperationURI(update.URI(), update.Operation)
changeset.UpdateDAG(nodeURI, update)
```

Note: `createOperationURI` is unexported. If tests are in the same package
(`package changeset`), this works. If not, export it or add a test helper.

**Step 4: Run changeset tests**

Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && go test ./internal/metastructure/changeset/... -v -count=1 2>&1 | tail -30`
Expected: All tests PASS

**Step 5: Run full test suite and linter**

Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && make test-all 2>&1 | tail -10`
Run: `cd /home/jeroen/dev/pel/formae/.worktrees/simplify-dag && make lint 2>&1 | tail -10`
Expected: All PASS, no lint errors

**Step 6: Commit**

```
refactor(changeset): update tests for Update interface
```
