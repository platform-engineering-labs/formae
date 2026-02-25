# Performance & Correctness Test Suite Design

**Date**: 2026-02-21
**Status**: Draft

## Problem Statement

Formae lacks a systematic approach to testing correctness under concurrency and
measuring performance at scale. The existing property-based test
(`internal/workflow_tests/local/property_test.go`) only explores the happy-path
input space with sequential operations against an in-process FakeAWS plugin. It
does not test failure scenarios, concurrent operations, or the realistic plugin
execution path (external processes with Ergo IPC).

Meanwhile, manual performance testing against real AWS (documented in
`docs/design/perf-test-plan.md`) has been ad-hoc, uncovering bugs but without a
reproducible, automated framework.

## Goals

1. **Correctness under concurrency**: Verify that interleaving operations
   (user apply/destroy, sync, auto-reconcile, TTL expiry, out-of-band cloud
   changes) produce correct results.
2. **Correctness under failure**: Verify that plugin errors, timeouts, and
   async status transitions leave the system in a consistent state.
3. **Performance characterization**: Measure throughput, latency overhead, and
   resource usage at scale (targets: 500 to 50K resources).
4. **Regression detection**: Track performance metrics over time; detect
   degradation.

## Non-Goals

- Full deterministic simulation testing (DST). Ergo's scheduler is
  non-deterministic and retrofitting DST would require invasive changes.
  Evaluated and deferred; see research notes.
- Replacing unit or integration tests. This suite complements existing tests.
- Testing the real AWS plugin's API interaction correctness (that is the AWS
  plugin's own concern).

## Architecture Overview

Two complementary test suites sharing common infrastructure:

```
Suite A: Property-Based Correctness Tests (blackbox)
  - Rapid-generated operation sequences
  - Test plugin (external process) with Ergo-based test controller
  - Failure injection via test controller messages
  - Assertions via formae REST API
  - FakeAWS-equivalent cloud simulation in the test plugin
  - Runs on every PR (fast, no cloud costs)

Suite B: Performance at Scale (real cloud)
  - Real AWS (+ GCP when available) with external plugin processes
  - Unified harness recording timestamped operation histories
  - Correctness checks + performance metric capture
  - Scale profiles: 500, 5K, 20K, 50K resources
  - Runs on significant PRs or manual trigger
```

Both suites are **blackbox**: they interact with the formae agent via the REST
API and CLI, never calling internal APIs directly. This aligns with the
extensions design (`docs/plans/2026-02-09-extensions-design.md`) which
eliminates `.so` plugin loading in favor of external processes.

---

## Relationship to Existing Tests and the Extensions Refactoring

FakeAWS serves different roles at different test levels:

- **Local workflow tests** (`internal/workflow_tests/local/`): FakeAWS is an
  in-process struct instance, retrieved from the Ergo environment (e.g.,
  `s.Env("Plugin")`). After the extensions refactoring, this changes from
  `.so` loading to direct struct registration, but the tests remain in-process
  and fast. These tests continue to exist and serve their purpose for
  unit-level actor testing.

- **Distributed workflow tests** (`internal/workflow_tests/distributed/`):
  FakeAWS is compiled as a standalone binary and runs as a real external
  process with Ergo IPC. This exercises the full inter-process communication
  path. This approach is unchanged by the extensions refactoring.

- **New blackbox property tests** (this design, Suite A): Same external process
  model as the distributed workflow tests, but with an added TestController
  actor for failure injection and cloud state manipulation. Tests drive the
  system entirely via the REST API.

The new blackbox property tests are orthogonal to the extensions refactoring.
They use the existing plugin SDK to build a standalone binary and do not depend
on how the agent loads or discovers plugins internally. This makes them a safe
foundation to build before the extensions refactoring, and they serve as a
safety net during that refactoring.

---

## Suite A: Property-Based Correctness Tests

### Test Plugin Architecture

The test plugin builds on the FakeAWS concept but is compiled as a standalone
binary using the plugin SDK (`pkg/plugin/sdk`). It runs as a real external
process that the formae agent spawns and manages like any other plugin. It
additionally hosts a **TestController** actor that accepts control messages from
the test harness.

```
Test Harness (Go test binary)
  |
  +-- Ergo node -----> Test Plugin Ergo node
  |                        |
  |                        +-- PluginOperator actors (standard CRUD)
  |                        +-- TestController actor
  |                              |
  |                              +-- Cloud state (in-memory)
  |                              +-- Failure injection rules
  |                              +-- Latency injection rules
  |
  +-- REST client ---> Formae Agent
                           |
                           +-- Agent Ergo node
                           +-- Echo HTTP server
```

**Three Ergo nodes** participate in each test:
1. **Agent node** - the standard formae agent
2. **Test plugin node** - the test plugin process
3. **Test harness node** - the Go test binary, used to send control messages to
   the test plugin

The test harness communicates with:
- The **formae agent** via REST API (apply, destroy, inventory, status, admin
  endpoints for triggering sync/discovery)
- The **test plugin** via Ergo messages to the TestController actor (failure
  injection, cloud state manipulation)

### TestController Actor

The TestController actor runs on the test plugin's Ergo node and handles:

**Failure injection messages:**
- `InjectError{Operation, ResourceType, ErrorType, Count}` - make the next N
  operations of the given type return an error
- `InjectLatency{Operation, ResourceType, Duration}` - add latency to
  operations
- `InjectAsyncStatus{Operation, ResourceType, Statuses}` - return a sequence of
  async statuses (PENDING, IN_PROGRESS) before the final result
- `ClearInjections{}` - reset all failure injections

**Cloud state manipulation messages:**
- `ModifyCloudState{NativeID, Properties}` - simulate an out-of-band property
  change
- `DeleteCloudState{NativeID}` - simulate an out-of-band deletion
- `CreateCloudState{NativeID, ResourceType, Properties}` - simulate an
  externally created resource (for discovery testing)
- `GetCloudState{}` - return the full cloud state (for assertion)

**Query messages:**
- `GetOperationLog{}` - return the timestamped log of all plugin operations
  (for post-hoc analysis)
- `GetCloudState{}` - return current cloud state snapshot

### Operation Model

Operations are generated by `rapid` and dispatched by the test harness.

**User operations** (via REST API):
- `ApplyForma{Resources, Mode, Blocking}` - apply with patch or reconcile
- `DestroyForma{Resources, Blocking}` - destroy selected resources
- `CancelCommand{CommandID}` - cancel in-progress command

**System operations** (via admin API endpoints):
- `TriggerSync{}` - force a synchronization run
- `TriggerDiscovery{}` - force a discovery run
- `TriggerAutoReconcile{}` - force an auto-reconcile check
- `TriggerTTLCheck{}` - force a TTL expiration check

**Cloud operations** (via Ergo messages to TestController):
- `CloudModify{NativeID, Properties}` - out-of-band modification
- `CloudDelete{NativeID}` - out-of-band deletion
- `CloudCreate{NativeID, Type, Properties}` - external resource creation

**Fault operations** (via Ergo messages to TestController):
- `InjectPluginError{Operation, ErrorType}` - transient or permanent error
- `InjectPluginLatency{Operation, Duration}` - add delay
- `InjectPluginTimeout{Operation}` - hang past timeout threshold
- `InjectPluginAsyncStatus{Operation, StatusSequence}` - async progression

**Dispatch modes:**
- `Blocking` - wait for command completion before next operation
- `FireAndForget` - dispatch and immediately proceed

`rapid` generates sequences of these operations. The generator controls:
- Which operations appear and in what proportion
- Which resources are affected
- Whether operations are blocking or fire-and-forget
- What failures are injected and when

### Expected State Model

The test harness maintains a **model** of what the system state should be after
each operation. This model tracks:

- **Resource existence**: which resources should exist, with what properties
- **Acceptable states**: after a failure injection, a resource might be in
  multiple valid states (e.g., created OR failed - both are acceptable)
- **Pending operations**: which commands are in flight

After each operation (or batch of operations), the harness queries the agent via
the REST API and checks the actual state against the model. The assertion is not
"state equals X" but "state is one of the acceptable states given the operations
that have been executed."

### Correctness Invariants

These properties must hold after every operation sequence, regardless of
failures and concurrent operations:

1. **No phantom resources**: every resource in the datastore either exists in
   the cloud state or has an in-progress delete operation.
2. **No orphaned cloud resources**: every resource in the cloud state is either
   tracked in the datastore or is genuinely unmanaged (and discoverable).
3. **Property consistency**: for managed, synced resources, the datastore
   properties match the cloud state properties.
4. **Command completeness**: every command eventually reaches a terminal state
   (Success, Failed, Canceled). No commands hang indefinitely.
5. **Stack integrity**: if a stack is destroyed, all its resources are eventually
   cleaned up (in both datastore and cloud).
6. **Dependency ordering**: resources are created in dependency order and deleted
   in reverse dependency order.
7. **Idempotency**: re-applying the same forma file without changes produces no
   resource updates.

Some invariants are relaxed when failures are injected. For example, after an
injected Create error, invariant 1 may temporarily be violated (resource exists
in datastore as "Failed" but not in cloud). The expected state model captures
these relaxations.

### Reproduction on Failure

When a property test fails, the harness generates:
1. The `rapid` seed for the failing sequence
2. A standalone Go test file that replays the exact operation sequence
3. The operation log from the TestController (timestamped plugin calls)
4. The cloud state snapshot at the time of failure

This preserves the existing reproduction test generation capability but extends
it with richer debugging information.

### Test Configuration

```go
type PropertyTestConfig struct {
    ResourceCount       int           // Number of resources (default: 10)
    OperationCount      Range         // Min/max operations per sequence
    ResourceTypes       []string      // Resource types to use
    StackCount          int           // Number of stacks (default: 1)
    EnableDependencies  bool          // Generate resource dependencies
    EnableFailures      bool          // Include fault injection operations
    EnableCloudChanges  bool          // Include out-of-band cloud operations
    EnableConcurrency   bool          // Include fire-and-forget operations
    SyncInterval        time.Duration // 0 = manual trigger only
    AutoReconcile       bool          // Enable auto-reconcile policy
    TTL                 time.Duration // 0 = no TTL
}
```

Individual test functions compose the configuration they need by toggling these
fields. For example, a sequential happy-path test disables failures, concurrency,
and cloud changes, while a full chaos test enables everything.

---

## Suite B: Performance at Scale

### Test Harness

The performance harness is a Go test binary that:
1. Starts a formae agent (or connects to a running one)
2. Configures the target cloud plugins (AWS, GCP when available)
3. Executes a predefined scenario
4. Records a timestamped operation history
5. Asserts correctness invariants
6. Computes and reports performance metrics

### Scenarios

#### Scenario 1: Discovery at Scale

**Setup**: A cloud environment with N existing (unmanaged) resources, created
beforehand via CloudFormation, Terraform, or the cloud provider's CLI.

**Test**: Start the formae agent and trigger discovery. Measure time to discover
all N resources and ingest them as unmanaged.

**Metrics**:
- Total discovery time
- Discovery rate (resources/minute)
- API calls made vs resources discovered (efficiency)
- Memory usage during discovery
- Error rate (API throttling, transient failures)

**Correctness**: All resources in the cloud account are discovered and present
in formae inventory with correct properties.

#### Scenario 2: Full Lifecycle (Apply → Steady-State → Destroy)

A single end-to-end scenario that exercises the complete resource lifecycle.

**Phase A - Large Apply**:
Apply a PKL forma file declaring N resources across multiple types via
`formae apply --mode reconcile`.

**Phase B - Steady-State Under Load**:
With N managed resources in a stable state, run for a fixed duration (e.g.,
30 minutes) with simultaneous:
- User applies modifications to subsets of resources
- Synchronizer runs periodically
- Auto-reconcile policy is active (triggered via admin endpoint)
- Out-of-band changes occur (via cloud CLI/API)

**Phase C - Destroy**:
Destroy all resources via `formae destroy`.

**Metrics** (captured across all phases):
- Total time per phase and end-to-end
- Orchestration overhead: `(actual_time - theoretical_minimum) / theoretical_minimum`
  where theoretical minimum = N resources / rate_limit_RPS
- Resource creation/destruction rate (resources/minute)
- Changeset execution parallelism (resources in-progress simultaneously)
- Operation throughput during steady-state (commands completed/minute)
- p50, p95, p99 command latency
- Sync cycle time and drift detection rate
- Destroy ordering correctness (dependencies respected)
- Memory and CPU usage over time (detect leaks)
- Error rate, retry count, and API call efficiency

**Correctness**: After each phase, all resources are in a consistent state. At
the end: all resources deleted from both cloud and formae inventory, stacks
cleaned up, no orphaned resources, all commands in terminal states.

### Scale Profiles

| Profile   | Resources | Expected Run Time | Use Case           |
|-----------|-----------|-------------------|--------------------|
| Small     | 500       | ~10 minutes       | PR gate (optional) |
| Medium    | 5,000     | ~1 hour           | Weekly regression   |
| Large     | 20,000    | ~4 hours          | Pre-release         |
| XL        | 50,000    | ~8+ hours         | Quarterly capacity  |

Times are rough estimates assuming AWS 2 RPS rate limit and include
orchestration overhead.

### Multi-Cloud

The harness is provider-agnostic. Scenarios are defined in terms of "resource
count" and "resource types" without hardcoding AWS specifics. When GCP (or other
providers) become available:
- Add their resource types to the scenario configuration
- The harness creates resources across multiple providers simultaneously
- Metrics are reported per-provider and aggregate

### Metric Collection and Reporting

**Metrics captured per run:**
- Wall-clock timing for each scenario phase
- formae agent memory/CPU usage (sampled periodically)
- Per-command timing (from command submission to terminal state)
- Plugin operation counts (from agent stats endpoint)
- Error counts and types
- Rate limiter utilization

**Storage and trending:**
- Metrics written to a JSON file per run
- File format supports comparison across runs (diffable)
- Future: ingest into a time-series database for dashboarding

**Regression detection:**
- Compare key metrics against a baseline file checked into the repo
- Flag regressions beyond configurable thresholds (e.g., >20% overhead
  increase)
- Initially: manual review of metric reports
- Future: automated CI gate based on thresholds

### Test Data Generation

Build on the existing `scripts/generate-stress-test-env.py` for creating
pre-existing cloud resources (for discovery scenarios). For apply scenarios,
generate PKL forma files programmatically.

Key improvements over the existing script:
- Support for 20K-50K resources (handle AWS quotas via multi-region/multi-account)
- Robust cleanup on failure
- Configurable resource type distribution
- Dependency generation for realistic resource graphs

---

## Shared Infrastructure

### Test Plugin (shared between Suite A and Suite B)

The test plugin is useful beyond Suite A. In Suite B, it could serve as a
"sidecar" that monitors plugin operations and records timing data, even when
using real cloud plugins.

However, the primary use of the test plugin is in Suite A. Suite B uses real
cloud plugins directly.

### Operation History

Both suites record a timestamped operation history. The format is shared:

```go
type OperationRecord struct {
    Timestamp   time.Time
    Operation   string        // "Apply", "Destroy", "Sync", etc.
    CommandID   string
    Resources   []string      // Resource identifiers affected
    Result      string        // "Success", "Failed", "Pending"
    Duration    time.Duration // Time from dispatch to completion
    Error       string        // Error message if failed
    Metadata    map[string]string // Additional context
}
```

This history feeds both:
- **Correctness analysis**: verify invariants hold at each point
- **Performance analysis**: compute latency distributions, throughput, overhead

### Admin API Extensions

The formae agent needs admin endpoints for test controllability:
- `POST /admin/sync` - already exists, triggers sync
- `POST /admin/discover` - already exists, triggers discovery
- `POST /admin/auto-reconcile` - NEW: trigger auto-reconcile check
- `POST /admin/ttl-check` - NEW: trigger TTL expiration check

These endpoints should only be available in test/debug mode.

---

## Implementation Phases

### Phase 1: Test Plugin Foundation
- Build the test plugin as an external process using the plugin SDK
- Implement the TestController actor with cloud state and failure injection
- Implement the test harness with Ergo node for control messages
- Basic end-to-end: harness starts agent, starts test plugin, applies a forma,
  queries inventory

### Phase 2: Property Test Redesign (Suite A)
- Implement the operation model and rapid generators
- Implement the expected state model
- Wire up correctness invariant assertions
- Sequential tests with and without failure injection

### Phase 3: Concurrent Property Tests
- Add fire-and-forget dispatch mode
- Add system operation triggers (sync, auto-reconcile, TTL)
- Concurrent operation tests and full chaos tests
- Add admin API endpoints for auto-reconcile and TTL triggers

### Phase 4: Performance Suite (Suite B)
- Build the performance harness
- Implement Scenario 1 (discovery at scale)
- Implement Scenario 2 (full lifecycle: apply → steady-state → destroy)
- Metric collection and reporting
- Integrate with existing test data generation script

### Phase 5: Multi-Cloud and Regression Baselines
- Add GCP (or second provider) support
- Performance baseline and regression detection
- CI integration for automated metric comparison

---

## Open Questions

1. **Test plugin location**: `tests/testplugin/` (alongside e2e tests) or
   `plugins/test/` (alongside other plugins)?
2. **Agent lifecycle in tests**: Should the test harness start/stop the agent
   binary, or connect to a pre-running agent?
3. **Multi-region for large scale**: How to handle AWS quotas at 20K-50K
   resources? Multi-region? Multiple accounts?
4. **GCP plugin availability**: When will the GCP plugin be ready for inclusion?
5. **Existing workflow tests**: Local workflow tests (in-process FakeAWS) remain
   valuable for fast actor-level testing. Distributed workflow tests share the
   same external-process approach as the new blackbox tests. Clarify which
   scenarios belong at which level to avoid duplication.
