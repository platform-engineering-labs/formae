# Tests

This directory contains the integration and property-based test suites for
formae. All tests in this directory exercise the full system — agent, plugin,
CLI — rather than individual units.

## Directory Structure

```
tests/
  blackbox/       Property-based tests that exercise the full system through the REST API
  e2e/            End-to-end tests that exercise the CLI against a running agent
  testplugin/     A controllable test plugin (separate binary) for the blackbox tests
  testcontrol/    Shared message types for harness ↔ test plugin communication
```

## End-to-End Tests (`tests/e2e/`)

The e2e tests exercise the formae CLI against a running agent with the test
plugin. They validate user-facing workflows (apply, destroy, extract, status)
and are the closest tests to the actual user experience.

```bash
make test-e2e
```

## Property-Based Tests (`tests/blackbox/`)

The property-based tests use [rapid](https://github.com/flyingmutant/rapid) to
generate random sequences of operations and verify that the system maintains
correctness invariants after each sequence. They are the primary mechanism for
finding bugs in the agent's orchestration logic.

### Architecture

The property tests run the formae agent as an external subprocess with the test
plugin. The test harness communicates with both over their respective interfaces:

```
  Test Harness (Go test process)
    ├── REST API ──────────────→ Formae Agent (subprocess)
    │                                 │
    └── Ergo cross-node calls ──→ Test Plugin (child of agent)
         (TestController actor)       │
                                      └── PluginOperator actors
                                           (CRUD operations)
```

**Test harness** (`harness.go`): Manages the agent lifecycle, communicates with
the TestController for response programming/injection, and provides the
`ExecuteOperation` dispatcher.

**Test plugin** (`testplugin/`): A fake resource plugin with controllable
behavior. Supports:
- Programmed response sequences (success, failure, throttling per-resource)
- Error and latency injection (global)
- Cloud state tracking (in-memory, queryable via TestController)
- Operation logging (every CRUD call recorded)
- Plugin gate (blocks all CRUD until the harness signals readiness)

**State model** (`state_model.go`): A deterministic model that tracks expected
resource states. Updated from:
1. Optimistic predictions at command submission time
2. Command response corrections from the agent API
3. Inventory existence checks at assertion boundaries (crash recovery, TTL)

The model explicitly does NOT use inventory to drive state updates during
normal operation — it relies on command responses to maintain an independent
view that can be compared against reality at assertion time.

### Resource Topology

Tests use a hierarchical resource pool with parent-child-grandchild
relationships across three resource types:

```
Test::Generic::Resource (parent)
  └── Test::Generic::ChildResource (child, references parent via $res)
        └── Test::Generic::GrandchildResource (grandchild, references child)
```

Multi-stack tests (3 stacks) add cross-stack children — resources on consumer
stacks (1, 2) that reference parents on the provider stack (0) via resolvable
properties.

### Test Variants

| Test | Stacks | Failures | OOB Changes | Cancel | TTL | Crash | Checks |
|------|--------|----------|-------------|--------|-----|-------|--------|
| SequentialHappyPath | 1 | - | - | - | - | - | 50 |
| SequentialWithFailures | 1 | yes | - | - | - | - | 50 |
| ConcurrentMultiStack | 3 | - | - | - | - | - | 50 |
| ConcurrentWithFailures | 3 | yes | - | - | - | - | 50 |
| FullChaos | 3 | yes | yes | yes | yes | yes | 100 |

### Operations

Each iteration generates a random sequence of operations drawn from:

- **Apply** (reconcile or patch mode) — create/update resources on a stack
- **Destroy** (default, abort, cascade) — delete resources
- **TriggerSync** — trigger sync and process drift
- **TriggerDiscovery** — trigger discovery of unmanaged resources
- **VerifyState** — assert invariants mid-sequence
- **CloudCreate/CloudModify/CloudDelete** — out-of-band cloud changes
- **ForceReconcile** — trigger auto-reconcile
- **SetTTLPolicy** — attach TTL with cascade-on-dependents
- **CheckTTL** — force TTL expiry check
- **Cancel** — cancel an in-flight command
- **CrashAgent** — SIGKILL the agent and restart

### Invariants Checked

After each iteration, `AssertAllInvariants` verifies:

1. **Command completeness** — all commands reached terminal state
2. **Inventory stabilization** — ResourcePersister finished async persists
3. **Resource invariants** — every inventory resource exists in cloud state
   (no phantoms), every cloud resource exists in inventory (no orphans)
4. **Model vs inventory** — model's existence predictions match inventory
5. **Unmanaged resources** — discovered resources tracked correctly
6. **Managed drift** — out-of-band changes detected and ingested

### Running

```bash
# Standard validation (50 checks + FullChaos at 100)
make test-property

# Individual test at custom check count
go test -C tests/blackbox -tags=property -run TestProperty_FullChaos \
  -v -count=1 -rapid.checks=500 -timeout=120m

# Smoke test (quick sanity check, not property-based)
go test -C tests/blackbox -tags=integration -run TestSmoke -v
```

### Deterministic Model Design

The state model tracks resource existence and properties without relying on
inventory polling during normal operation. This makes the model deterministic
and fast, but requires careful handling of edge cases:

**Command response corrections**: After each command completes, the model
processes per-resource states from the response. For failed commands, resources
are reverted to their pre-command snapshot state.

**Authoritative slots**: TTL destroy marks slots as authoritative — subsequent
stale commands cannot override the destroyed state. Only explicit creates
(from new commands or SetupStacks) clear the authoritative flag.

**Crash recovery**: After a crash+restart, the model reconciles against
inventory because command responses during crash recovery are unreliable
(ReRunIncompleteCommands may re-execute operations the model didn't track).

**Cross-stack resources**: The model skips cross-stack slots in model-vs-
inventory assertions because their persistence behavior in failed commands
is non-deterministic from the command response alone.

**Property tracking**: Model-vs-inventory property comparison is only done
for simple (non-pool) tests. With resource pools, property tracking through
concurrent patch/reconcile commands is unreliable without inventory reads.

### Ergo Cross-Node Communication

The test harness communicates with the test plugin's TestController via Ergo
cross-node RPC calls. Under high CRUD load, Ergo message delivery can be
delayed. Mitigations:

- Test plugin rate limit reduced to 20 RPS (prevents CRUD burst congestion)
- Ergo TCP pool size set to 1 (avoids silent message loss on secondary connections)
- TestController calls retry once on timeout
