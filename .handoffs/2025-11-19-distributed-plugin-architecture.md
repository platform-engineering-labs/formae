# Distributed Plugin Architecture - Handoff

**Date:** 2025-11-19
**Branch:** `feat/distributed-plugins`
**Status:** 📋 Planning Complete - Ready for Implementation
**Previous Session:** 2025-11-18 (Azure plugin rebase - dependency conflicts)

---

## Session Summary

Completed comprehensive research and planning for converting Formae's plugin architecture from Go's `plugin.Open()` (.so files) to a distributed actor-based system using Ergo's networking capabilities. This solves the Azure plugin dependency conflict while positioning us for multi-cloud support and future host agent features.

**Key Decision**: Start with FakeAWS distributed tests to validate architecture before converting production plugins.

---

## What We Accomplished

### 1. Architecture Research ✅

**Created**: `.research/distributed-plugin-architecture.md` (comprehensive design doc)

**Key Findings**:
- Ergo framework provides all necessary primitives for distributed actors
- `meta.Port` can spawn external processes AND manage bidirectional IPC
- Current codebase is already well-architected (message-based, no pointer sharing)
- Network transparency makes remote calls look identical to local calls
- Built-in registrar sufficient for localhost, etcd available for multi-host

### 2. Clarifications Document ✅

**Created**: `.research/distributed-plugin-architecture-clarifications.md`

**Addressed**:
- ✅ meta.Port capabilities (spawns processes + IPC via stdin/stdout)
- ✅ Three-layer architecture (actornames, Registrar, PluginCoordinator)
- ✅ Registry interaction and route resolution
- ✅ Configuration approach (PKL → agent, env vars → plugins)
- ✅ Plugin discovery with versioning (semantic versioning + manifests)
- ✅ FakeAWS testing strategy (3-phase: in-process → distributed → gradual migration)

### 3. Implementation Plan ✅

**Created**: `.plans/distributed-plugin-implementation.md` (detailed implementation guide)

**Structure**:
- Complete design document with architecture diagrams
- 5 phases with specific tasks and checkpoints
- TDD approach (London School) with failing test → implementation → passing test
- Estimated 9-14 days total effort
- Clear acceptance criteria for each phase

**Phases**:
1. **Phase 0: Preparation** (1-2 days) - Enable Ergo networking
2. **Phase 1: FakeAWS Binary** (2-3 days) - Convert FakeAWS to standalone
3. **Phase 2: Distributed Tests** (3-4 days) - Create comprehensive test suite
4. **Phase 3: AWS Plugin** (2-3 days) - Convert AWS, validate with SDK tests
5. **Phase 4: Azure Plugin** (1-2 days) - Solve original blocker

---

## Architecture Overview

### Current State (Broken)

```
Agent Process
├─ PluginManager.Load() → plugin.Open("aws.so")
├─ PluginManager.Load() → plugin.Open("azure.so")  ❌ FAILS
│   └─ Error: different version of golang.org/x/net/idna
└─ AWS and Azure SDKs have conflicting dependencies
```

### Target State (Distributed)

```
┌─────────────────────────────────────────────────────────┐
│ Agent Process (Ergo Node: agent@localhost)             │
│                                                         │
│  ResourceUpdater → PluginCoordinator                    │
│                         ├─ Spawns: fake-aws-plugin     │
│                         │   (via meta.Port)             │
│                         ├─ Spawns: aws-plugin          │
│                         │   (via meta.Port)             │
│                         └─ Spawns: azure-plugin        │
│                             (via meta.Port)             │
│                                                         │
│  Provides remote PIDs to ResourceUpdater               │
└────────────────────┬────────────────────────────────────┘
                     │ Ergo Network (TCP)
        ┌────────────┼────────────┐
        │            │            │
        ▼            ▼            ▼
┌─────────────┐ ┌─────────────┐ ┌─────────────┐
│ FakeAWS     │ │ AWS Plugin  │ │ Azure Plugin│
│ Plugin      │ │ Process     │ │ Process     │
│ Process     │ │             │ │             │
│ (Ergo Node) │ │ (Ergo Node) │ │ (Ergo Node) │
│             │ │             │ │             │
│ - Own deps  │ │ - AWS SDK   │ │ - Azure SDK │
│ - Plugin    │ │ - Plugin    │ │ - Plugin    │
│   Operators │ │   Operators │ │   Operators │
└─────────────┘ └─────────────┘ └─────────────┘
  ✅ No conflicts - each has independent dependencies!
```

### Key Components

#### 1. PluginCoordinator Actor (New)
**Location**: `internal/metastructure/plugin_coordinator/plugin_coordinator.go`

**Responsibilities**:
- Spawn plugin processes via `meta.Port`
- Monitor plugin health (stdout, stderr, exit)
- Provide remote PluginOperator PIDs to ResourceUpdater
- Restart crashed plugins
- Unified logging

**Messages**:
```go
type GetPluginOperator struct {
    Namespace   string
    ResourceURI string
    Operation   string
    OperationID string
}

type PluginOperatorPID struct {
    PID gen.ProcessID  // Points to remote plugin node
}
```

#### 2. Plugin Binary Structure (New)
```
plugins/fake-aws/
├── main.go              (NEW - entry point, Ergo node)
├── plugin_operator.go   (MOVED from metastructure)
├── plugin_application.go (NEW - supervisor)
└── fake_aws.go          (existing)
```

#### 3. Environment Sharing
**Agent → Plugin** (via meta.Port ENV):
- `FORMAE_AGENT_NODE=agent@localhost`
- `FORMAE_PLUGIN_NODE=fake-aws-plugin@localhost`
- `FORMAE_NETWORK_COOKIE=shared-secret`
- Plugin-specific vars (AWS_REGION, etc.)

**Agent → PluginOperator** (via Ergo RemoteSpawn):
- `RetryConfig` - inherited via `ExposeEnvRemoteSpawn = true`
- `LoggingConfig` - can change at runtime
- Plugin instance from plugin node's env

### Message Flow Example

```
ResourceUpdater needs to create AWS::S3::Bucket
  ↓
Call PluginCoordinator.GetPluginOperator("AWS", ...)
  ↓
Returns: ProcessID{Name: "op-xyz", Node: "aws-plugin@localhost"}
  ↓
Call remote ProcessID with CreateResource message
  ↓
Ergo network:
  - Resolves node via registrar
  - Establishes TCP connection
  - Serializes message via EDF
  - Sends to plugin
  ↓
Plugin's PluginOperator receives message
  ↓
Executes plugin.Create(ctx, request)
  ↓
Returns ProgressResult over network
  ↓
ResourceUpdater continues normal flow
```

---

## Why This Approach?

### Problems Solved

1. **Dependency Hell** ✅
   - Each plugin has independent dependencies
   - No version conflicts between cloud SDKs
   - Azure plugin can now load alongside AWS

2. **Fault Isolation** ✅
   - Plugin crashes don't crash agent
   - Auto-restart on failure
   - Better error messages

3. **Future-Proofing** ✅
   - Aligns with host agent vision
   - Enables multi-cloud without pain
   - Sets up for HA clustering

4. **Developer Experience** ✅
   - Plugin developers don't coordinate dependencies
   - Independent release cycles
   - Third-party plugins become feasible

### Why Start with FakeAWS Tests?

- **Validates architecture** before touching production
- **Fast iteration** (no cloud API calls, < 5s per test)
- **Debuggable** (controlled behavior via overrides)
- **Establishes patterns** for real plugins
- **Low risk** (tests only, no production impact)

---

## Implementation Strategy

### Phase-by-Phase Approach

Each phase has:
- Clear goal and acceptance criteria
- TDD checkpoints (write test → see it fail → implement → see it pass)
- Specific files to create/modify
- Manual verification steps

### Phase 0: Preparation (1-2 days)
**Goal**: Enable Ergo networking

**Tasks**:
1. Change `NetworkModeDisabled` → `NetworkModeEnabled` in metastructure
2. Enable `ExposeEnvRemoteSpawn` for env sharing
3. Register all message types with EDF (serialization)
4. Create PluginCoordinator skeleton
5. Add to application startup

**Checkpoint**: Agent starts with networking enabled

### Phase 1: FakeAWS as Distributed Plugin (2-3 days)
**Goal**: Convert FakeAWS to standalone binary

**Tasks**:
1. Create `plugins/fake-aws/main.go` (Ergo node entry point)
2. Move PluginOperator to `pkg/plugin/operator` (shared code)
3. Create PluginApplication supervisor
4. Update Makefile for binary build
5. Manual test: start plugin binary, verify it connects

**Checkpoint**: FakeAWS plugin runs as separate process

### Phase 2: Distributed Workflow Tests (3-4 days)
**Goal**: Create comprehensive test suite

**Tasks**:
1. Create distributed test helpers
2. Write happy path tests (Create/Read/Update/Delete)
3. Test plugin crash recovery
4. Test concurrent operations
5. Implement meta.Port integration in PluginCoordinator
6. Update ResourceUpdater to use PluginCoordinator

**Checkpoint**: All distributed tests pass ✅

### Phase 3: AWS Plugin Conversion (2-3 days)
**Goal**: Convert AWS plugin, validate with SDK tests

**Tasks**:
1. Create AWS plugin main.go
2. Update Makefile
3. Add AWS to PluginCoordinator
4. Run SDK tests against distributed AWS plugin

**Checkpoint**: AWS SDK tests pass

### Phase 4: Azure Plugin Integration (1-2 days)
**Goal**: Solve original blocker

**Tasks**:
1. Create Azure plugin main.go
2. Update Makefile
3. Add Azure to PluginCoordinator
4. Test AWS + Azure simultaneously
5. Verify no dependency conflicts

**Checkpoint**: Original problem solved! 🎉

---

## Current Branch State

### Branch: `feat/distributed-plugins`

**Created from**: `feat/azure-plugin` (commit a79374b)

**New Files** (staged but not committed yet):
```
.research/
  ├── distributed-plugin-architecture.md
  └── distributed-plugin-architecture-clarifications.md
.plans/
  └── distributed-plugin-implementation.md
.handoffs/
  └── 2025-11-19-distributed-plugin-architecture.md (this file)
```

**No Code Changes Yet**: All research and planning only

### Git Status
```
On branch feat/distributed-plugins
Untracked files:
  .plans/distributed-plugin-implementation.md
  .research/distributed-plugin-architecture-clarifications.md
  .research/distributed-plugin-architecture.md
  .handoffs/2025-11-19-distributed-plugin-architecture.md

nothing added to commit but untracked files present
```

---

## Testing Strategy

### Three Test Layers

1. **In-Process Tests** (Fast, TDD cycle)
   - Location: `internal/metastructure/*_test.go`
   - FakeAWS loaded as .so file
   - Context-based overrides
   - < 1s per test
   - Good for: TDD, unit testing, quick validation

2. **Distributed Tests** (Integration)
   - Location: `internal/workflow_tests/distributed/*_test.go` (NEW)
   - FakeAWS runs as separate process
   - Tests actual network communication
   - 2-5s per test
   - Good for: Architecture validation, crash recovery, integration

3. **SDK Tests** (Real Cloud)
   - Location: `tests/integration/plugin-sdk/*_test.go`
   - Real plugins (AWS, Azure)
   - Actual cloud API calls
   - 30s+ per test
   - Good for: Production validation, CI/CD

### Test Commands

```bash
# Fast unit tests (in-process)
make test-unit

# Distributed integration tests
make test-distributed

# SDK tests (requires cloud credentials)
PLUGIN_NAME=aws make test-plugin-sdk-aws

# All tests
make test-all
```

---

## Key Design Decisions

### 1. meta.Port for Process Management
**Decision**: Use `meta.Port` instead of direct `os/exec`

**Rationale**:
- Integrated with Ergo supervision tree
- Actor-native message handling
- Automatic stdout/stderr capture
- Clean shutdown handling

### 2. Start with FakeAWS Tests
**Decision**: Validate architecture with tests before converting production plugins

**Rationale**:
- De-risks implementation
- Fast feedback loop
- Proves concept before investment
- Establishes patterns

### 3. PluginOperator Moves to Plugin Process
**Decision**: PluginOperator FSM runs in plugin process, not agent

**Rationale**:
- Plugin crashes don't affect agent state machines
- Plugin state lives with the plugin
- Simpler - no PluginManager needed in operator
- Better isolation

### 4. Environment Sharing via RemoteSpawn
**Decision**: Use Ergo's `ExposeEnvRemoteSpawn` for RetryConfig, LoggingConfig

**Rationale**:
- Can change at runtime
- Shared with agent
- No need to pass via CLI args
- Standard Ergo pattern

### 5. Built-in Registrar for MVP
**Decision**: Use Ergo's built-in registrar initially, etcd later

**Rationale**:
- Sufficient for localhost
- Zero configuration
- Upgrade path to etcd for multi-host
- Lower barrier to entry

---

## Next Steps for Implementing Engineer

### Immediate (First Session)

1. **Review Documentation**
   - Read `.plans/distributed-plugin-implementation.md` (full plan)
   - Read `.research/distributed-plugin-architecture-clarifications.md` (Q&A)
   - Skim `.research/distributed-plugin-architecture.md` (comprehensive research)

2. **Environment Setup**
   ```bash
   # Checkout branch
   git checkout feat/distributed-plugins
   git pull origin feat/distributed-plugins

   # Build current state (should work as before)
   make build

   # Run existing tests (should pass)
   make test-all
   ```

3. **Start Phase 0, Task 0.1**
   - File: `internal/metastructure/metastructure_test.go` (new)
   - Write failing test for networking enabled
   - See it fail
   - Implement: Change `NetworkModeDisabled` → `NetworkModeEnabled`
   - See it pass
   - Move to next task

### Daily Workflow

1. **Morning**: Review plan for current phase
2. **TDD Cycle**:
   - Write failing test
   - **CHECKPOINT**: Review test with team lead (if unclear)
   - Implement minimum code to pass
   - **CHECKPOINT**: See test pass, review implementation
   - Refactor if needed
3. **End of Day**: Update handoff doc with progress

### Weekly Milestones

- **Week 1**: Complete Phase 0 & Phase 1 (FakeAWS binary)
- **Week 2**: Complete Phase 2 (Distributed tests)
- **Week 3**: Complete Phase 3 & 4 (AWS, Azure plugins)

### When to Ask Questions

- **Before implementing**: If acceptance criteria unclear
- **After checkpoint fails**: If test doesn't behave as expected
- **When stuck > 2 hours**: Don't spin wheels, ask for help
- **Before major decisions**: Architecture changes not in plan

---

## Important Constraints & Patterns

### From CLAUDE.md

1. **No Pointer Sharing Between Actors**
   - All messages must be serializable
   - Actors communicate via messages only
   - Critical for multi-node setups

2. **Separation of Concerns**
   - Never share internal metastructure types with API layer
   - Use separate model types in `internal/api/model/`
   - All API errors in `internal/api/errors.go`

3. **TDD Approach (London School)**
   - Write test first
   - See it fail for the right reason
   - **CHECKPOINT**: Review
   - Implement minimum code
   - See it pass
   - **CHECKPOINT**: Review
   - Refactor
   - Move to next test

### Testing Checkpoints

From CLAUDE.md:
- **Every failing test**: Pause, review, validate failure reason
- **Every passing test**: Pause, review, validate implementation
- **Only proceed after checkpoint approval**

---

## Potential Issues & Mitigations

### Issue 1: Ergo Learning Curve
**Symptom**: Confusion about gen.PID vs gen.ProcessID, Node vs RemoteNode

**Mitigation**:
- Refer to `.research/distributed-plugin-architecture-clarifications.md`
- Search existing codebase for patterns (ResourceUpdater, ChangesetExecutor)
- Ergo docs: https://docs.ergo.services/

### Issue 2: meta.Port Not Working
**Symptom**: Plugin process starts but doesn't connect

**Mitigation**:
- Check environment variables are set correctly
- Verify network cookie matches
- Check registrar logs: `agent@localhost` and `plugin@localhost` both registered
- Use `gen.Node.Network().GetNode()` to verify connection

### Issue 3: Tests Timing Out
**Symptom**: Distributed tests hang or timeout

**Mitigation**:
- Increase timeouts (30s → 60s)
- Add debug logging to see where it's stuck
- Check if plugin process is actually running (`ps aux | grep plugin`)
- Verify Ergo nodes are connected

### Issue 4: Message Serialization Errors
**Symptom**: `unknown type` or encoding errors

**Mitigation**:
- Check EDF type registration in `internal/metastructure/edf_types.go`
- Verify type is registered on BOTH agent and plugin
- Check for pointer types (must be values)

---

## Success Criteria

### Phase Completion

Each phase is complete when:
- [ ] All tasks finished
- [ ] All tests pass
- [ ] Manual verification done
- [ ] Acceptance criteria met
- [ ] Code reviewed (if team process requires)
- [ ] Handoff doc updated with progress

### Overall Success

Project is complete when:
- [ ] All 5 phases complete
- [ ] AWS and Azure plugins run simultaneously
- [ ] No dependency conflicts (verified)
- [ ] All SDK tests pass
- [ ] All distributed tests pass
- [ ] All existing tests still pass
- [ ] Documentation updated
- [ ] Ready for code review

---

## Resources

### Documentation Created

| Document | Purpose | Location |
|----------|---------|----------|
| **Implementation Plan** | Step-by-step guide | `.plans/distributed-plugin-implementation.md` |
| **Research Document** | Architecture research | `.research/distributed-plugin-architecture.md` |
| **Clarifications** | Q&A and details | `.research/distributed-plugin-architecture-clarifications.md` |
| **This Handoff** | Session summary | `.handoffs/2025-11-19-distributed-plugin-architecture.md` |

### External References

| Resource | URL |
|----------|-----|
| Ergo Framework Docs | https://docs.ergo.services/ |
| Ergo GitHub | https://github.com/ergo-services/ergo |
| meta.Port Docs | https://docs.ergo.services/meta-processes/port |
| Ergo Examples | https://github.com/ergo-services/examples |

### Key Files to Understand

| File | Purpose |
|------|---------|
| `internal/metastructure/metastructure.go` | Agent setup, Ergo node creation |
| `internal/metastructure/application.go` | Actor supervision tree |
| `internal/metastructure/resource_update/resource_updater.go` | Calls PluginOperator |
| `internal/metastructure/plugin_operation/plugin_operator.go` | FSM for CRUD ops (will move) |
| `plugins/fake-aws/fake_aws.go` | FakeAWS plugin implementation |

---

## Questions for Next Engineer

If you have questions:

1. **About the plan**: Review implementation plan, check clarifications doc
2. **About Ergo**: Check docs.ergo.services, search existing codebase for patterns
3. **About testing**: Refer to testing strategy section
4. **Stuck on task**: Review checkpoint criteria, ask for help after 2 hours

**Communication**:
- Update this handoff doc with daily progress
- Create new handoff doc when passing to another engineer
- Use commit messages to track progress per task

---

## Environment Info

**Working Directory**: `/home/jeroen/dev/pel/formae-internal`
**Branch**: `feat/distributed-plugins`
**Go Version**: (check with `go version`)
**Ergo Version**: `ergo.services/ergo v1.999.310`

**Azure Subscription**: `b161e970-a04d-48a4-ac91-18d06e8d8047` (for SDK tests)

---

## Commit Strategy

### This Session's Commit

**Message**:
```
docs: add distributed plugin architecture plan

Add comprehensive planning documents for converting plugin system
from Go's plugin.Open() to distributed actor-based architecture
using Ergo framework.

Documents added:
- .plans/distributed-plugin-implementation.md (detailed plan)
- .research/distributed-plugin-architecture.md (research)
- .research/distributed-plugin-architecture-clarifications.md (Q&A)
- .handoffs/2025-11-19-distributed-plugin-architecture.md (handoff)

This solves the Azure plugin dependency conflict (golang.org/x/net/idna)
while positioning us for multi-cloud support and host agent features.

Next step: Phase 0 - Enable Ergo networking
```

### Future Commits (Per Phase)

- `feat(phase-0): enable Ergo networking`
- `feat(phase-1): add FakeAWS plugin binary`
- `test(phase-2): add distributed workflow tests`
- `feat(phase-3): convert AWS plugin to distributed`
- `feat(phase-4): add Azure plugin (resolve dependency conflicts)`

---

## Summary

This session completed comprehensive planning for the distributed plugin architecture. The research validates that Ergo provides all necessary primitives, the current codebase is well-positioned for this change, and the phased approach starting with FakeAWS tests de-risks the implementation.

**Status**: ✅ Planning complete, ready for implementation
**Next Owner**: Engineer picking up Phase 0
**Estimated Time to Complete**: 9-14 days
**Risk Level**: Medium (new architecture, but well-planned and testable)
**Value**: High (solves blocker, enables multi-cloud, improves developer experience)

**The stage is set. The plan is clear. Let's build this! 🚀**

---

**End of Handoff Document**
